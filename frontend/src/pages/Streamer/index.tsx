import {useState, useRef, useEffect, useLayoutEffect, useCallback} from "react";
import {useParams, useNavigate} from "react-router-dom";
import {useQuery, useMutation} from "@tanstack/react-query";
import {streamsApi, type Tier} from "../../api/streams";
import {apiPostBinary} from "../../api/client";
import {useWebSocket} from "../../hooks/useWebSocket";

// Resolution tiers — defined by the long side only. The short side is computed
// from the camera's native aspect ratio so no 16:9 cropping is forced.
const TIERS: { name: Tier; longSide: number }[] = [
  {name: "QHD", longSide: 2560},
  {name: "FHD", longSide: 1920},
  {name: "HD", longSide: 1280},
];

const ROTATION_LABELS: Record<number, string> = {
  0: "0°", 90: "90°", 180: "180°", 270: "270°",
};

type FacingMode = "environment" | "user";
type Rotation = 0 | 90 | 180 | 270;

interface CamCaps {
  maxWidth: number;
  maxHeight: number;
  maxFrameRate: number;
}

async function probeCameraCapabilities(facingMode: FacingMode): Promise<CamCaps> {
  const tmp = await navigator.mediaDevices.getUserMedia({video: {facingMode}});
  const caps = tmp.getVideoTracks()[0].getCapabilities();
  tmp.getTracks().forEach(t => t.stop());
  return {
    maxWidth: caps.width?.max ?? 1280,
    maxHeight: caps.height?.max ?? 720,
    maxFrameRate: caps.frameRate?.max ?? 30,
  };
}

// Returns true only when the device has distinct front and back cameras, i.e.
// requesting "environment" and "user" yields two different physical devices.
// On a desktop with a single or multiple same-facing webcams both constraints
// resolve to the same deviceId, so the toggle is correctly hidden.
async function hasFrontAndBackCamera(): Promise<boolean> {
  try {
    const env = await navigator.mediaDevices.getUserMedia({video: {facingMode: "environment"}});
    const envId = env.getVideoTracks()[0].getSettings().deviceId;
    env.getTracks().forEach(t => t.stop());

    const user = await navigator.mediaDevices.getUserMedia({video: {facingMode: "user"}});
    const userId = user.getVideoTracks()[0].getSettings().deviceId;
    user.getTracks().forEach(t => t.stop());

    return envId !== userId;
  } catch {
    return false;
  }
}

export default function StreamerPage() {
  const {id} = useParams<{ id: string }>();
  const navigate = useNavigate();
  const {data: stream} = useQuery({
    queryKey: ["streams", id],
    queryFn: () => streamsApi.get(id!),
    enabled: !!id,
    // Prevent React Query from re-fetching when the tab regains focus.
    // A re-fetch would change the `stream` object reference, re-trigger
    // Effect 1's cleanup, and stop the camera tracks mid-broadcast.
    refetchOnWindowFocus: false,
  });

  const startMutation = useMutation({
    mutationFn: ({rotation, tier, aspectRatio}: { rotation: number; tier: Tier; aspectRatio: number }) =>
      streamsApi.start(id!, rotation, tier, aspectRatio),
  });
  const stopMutation = useMutation({
    mutationFn: () => streamsApi.stop(id!),
    onSuccess: () => navigate("/"),
  });

  const videoRef = useRef<HTMLVideoElement>(null);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const chunkWorkerRef = useRef<{ worker: Worker; url: string } | null>(null);
  // Timestamp of the last successfully sent chunk (ms). Used to detect stalls.
  const lastChunkSentRef = useRef<number>(0);
  // Ref-stable stop callback so the stall-detection interval can call it
  // without capturing a stale closure.
  const handleStopRef = useRef<() => Promise<void>>(async () => {});
  // Kept as a ref so the tier-preview effect can read it without becoming a dep.
  const isLiveRef = useRef(false);

  const [isLive, setIsLive] = useState(false);
  const [viewerCount, setViewerCount] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [cameraReady, setCameraReady] = useState(false);
  const [elapsed, setElapsed] = useState(0);
  const [bytesSent, setBytesSent] = useState(0);
  const [facingMode, setFacingMode] = useState<FacingMode>("environment");
  const [rotation, setRotation] = useState<Rotation>(0);
  const [camDims, setCamDims] = useState<{ w: number; h: number } | null>(null);
  const [camCaps, setCamCaps] = useState<CamCaps | null>(null);
  const [availTiers, setAvailTiers] = useState<Tier[]>([]);
  const [selectedTier, setSelectedTier] = useState<Tier>("FHD");
  const [hasMultipleCameras, setHasMultipleCameras] = useState(false);
  const [force169, setForce169] = useState(false);

  // Keep ref in sync so the tier effect can check it without adding it to its deps.
  useEffect(() => {
    isLiveRef.current = isLive;
  }, [isLive]);

  useEffect(() => {
    const base = stream?.title ?? "Streamer";
    document.title = isLive ? `● ${base} – Streamer` : `${base} – Streamer`;
  }, [stream?.title, isLive]);

  // Connect to the WebSocket while live to receive viewer count updates.
  // The streamer=true flag excludes this connection from the count itself.
  useWebSocket(isLive ? id : undefined, (msg) => {
    if (msg.type === "viewer.count") {
      setViewerCount((msg.payload as {count: number}).count);
    }
  }, {query: "streamer=true"});

  // ── Effect 1: capability probe ────────────────────────────────────────────
  // Runs when the camera or facing changes. Does NOT open the camera itself;
  // that is done by Effect 2 once caps are known.
  useEffect(() => {
    if (!stream) return;

    probeCameraCapabilities(facingMode)
      .then((caps) => {
        // Restore rotation when rejoining an already-live stream (e.g. page refresh).
        if (stream.status === "live") {
          setRotation((stream.rotation as Rotation) || 0);
        }
        setCamCaps(caps);
        const longSide = Math.max(caps.maxWidth, caps.maxHeight);
        const avail = TIERS.filter(t => t.longSide <= longSide).map(t => t.name);
        setAvailTiers(avail);
        // Pick the best available tier (don't override a user selection that is
        // still valid after a camera flip; reset only when changing facing mode).
        setSelectedTier(prev =>
          avail.includes(prev) ? prev : (avail[0] ?? "HD")
        );
        // After the probe we have camera permission — now check facing modes.
        return hasFrontAndBackCamera();
      })
      .then((result) => {
        if (result !== undefined) setHasMultipleCameras(result);
      })
      .catch(() => {
        // getCapabilities() is unsupported (e.g. Firefox) — fall back gracefully.
        const fallback: CamCaps = {maxWidth: 1920, maxHeight: 1080, maxFrameRate: 30};
        setCamCaps(fallback);
        setAvailTiers(["FHD", "HD"]);
        setSelectedTier("FHD");
      });

    return () => {
      // Never stop camera tracks while a broadcast is in progress. This
      // cleanup runs if `stream` or `facingMode` deps change — which can
      // happen when React Query re-fetches on window focus even though the
      // stream object is otherwise unchanged.
      if (isLiveRef.current) return;
      streamRef.current?.getTracks().forEach(t => t.stop());
      streamRef.current = null;
      setCameraReady(false);
      setCamDims(null);
      setCamCaps(null);
      setAvailTiers([]);
    };
  }, [stream, facingMode]);

  // ── Effect 2: open / reopen preview at selected tier ─────────────────────
  // Runs whenever the tier, caps, or facing mode changes. While camCaps is still
  // null (probe in progress) we open a low-constraint fallback so the user sees
  // something immediately. Once caps arrive the correct-resolution stream
  // replaces it.  During a live broadcast the effect is a no-op.
  useEffect(() => {
    if (isLiveRef.current) return;

    let cancelled = false;

    let videoConstraints: MediaTrackConstraints;
    if (camCaps) {
      const camLong = Math.max(camCaps.maxWidth, camCaps.maxHeight);
      const camShort = Math.min(camCaps.maxWidth, camCaps.maxHeight);
      const ratio = force169 ? (16 / 9) : camLong / camShort;
      const isPortrait = camCaps.maxHeight > camCaps.maxWidth;
      const tier = TIERS.find(t => t.name === selectedTier) ?? TIERS[1];
      const tLong = tier.longSide;
      const tShort = Math.round(tLong / ratio);
      const [w, h] = isPortrait ? [tShort, tLong] : [tLong, tShort];
      videoConstraints = {
        facingMode,
        width: {exact: w},
        height: {exact: h},
        //frameRate: {ideal: camCaps.maxFrameRate},
      };
    } else {
      // Caps not yet known — open a no-constraint preview as a placeholder.
      videoConstraints = {facingMode};
    }

    navigator.mediaDevices
      .getUserMedia({video: videoConstraints, audio: true})
      .then(ms => {
        if (cancelled) {
          ms.getTracks().forEach(t => t.stop());
          return;
        }
        // Stop whatever stream was open before (previous tier or placeholder).
        streamRef.current?.getTracks().forEach(t => t.stop());
        streamRef.current = ms;
        if (videoRef.current) {
          videoRef.current.srcObject = ms;
          videoRef.current.muted = true;
        }
        setCameraReady(true);
        setCamDims(null); // reset; onLoadedMetadata will repopulate
      })
      .catch(err => {
        if (!cancelled) setError(`Camera error: ${err instanceof Error ? err.message : err}`);
      });

    return () => {
      cancelled = true;
    };
  }, [selectedTier, camCaps, facingMode, force169]);

  // ── Live timer ────────────────────────────────────────────────────────────
  useEffect(() => {
    if (!isLive) return;
    const t = setInterval(() => setElapsed(e => e + 1), 1000);
    return () => clearInterval(t);
  }, [isLive]);

  // ── Background tab handling ───────────────────────────────────────────────
  // When the tab is hidden, flush the recorder buffer so ffmpeg gets a clean
  // segment boundary before frame delivery stops. Browsers stop delivering
  // camera frames when a tab is hidden; we cannot restart recording reliably,
  // so the stall-detection effect below will auto-stop the stream.
  useEffect(() => {
    if (!isLive) return;

    const onVisibilityChange = () => {
      if (document.hidden) {
        // Flush recorder buffer so ffmpeg gets a clean segment boundary before
        // frame delivery stops.
        if (mediaRecorderRef.current?.state === "recording") {
          mediaRecorderRef.current.requestData();
        }
      } else {
        // Tab became visible: reset the stall timer so the camera has a fresh
        // window to resume delivering frames. Without this, any stall time
        // accumulated in the background would persist and trigger an immediate
        // auto-stop on return.
        lastChunkSentRef.current = Date.now();
      }
    };

    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => {
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [isLive]);

  // ── Stall detection ───────────────────────────────────────────────────────
  // If no chunk has been sent for 10 seconds while live (e.g. the tab was
  // moved to the background and the browser stopped delivering camera frames),
  // automatically stop the stream so the server does not wait indefinitely.
  useEffect(() => {
    if (!isLive) return;
    lastChunkSentRef.current = Date.now();
    const id = setInterval(() => {
      if (Date.now() - lastChunkSentRef.current > 10_000) {
        clearInterval(id);
        handleStopRef.current();
      }
    }, 2000);
    return () => clearInterval(id);
  }, [isLive]);

  // ── Helpers ───────────────────────────────────────────────────────────────
  const handleVideoMetadata = useCallback(() => {
    const vid = videoRef.current;
    if (vid?.videoWidth && vid?.videoHeight)
      setCamDims({w: vid.videoWidth, h: vid.videoHeight});
  }, []);

  const sendChunk = useCallback(async (blob: Blob) => {
    try {
      await apiPostBinary(`/api/streams/${id}/ingest`, blob);
      lastChunkSentRef.current = Date.now();
      setBytesSent(b => b + blob.size);
    } catch (err) {
      console.error("ingest error:", err);
    }
  }, [id]);

  // ── Go Live ───────────────────────────────────────────────────────────────
  // The camera is already open at the correct resolution (Effect 2 set it up),
  // so we only need to start the recorder and notify the backend.
  const handleGoLive = async () => {
    setError(null);
    const ms = streamRef.current;
    if (!ms) {
      setError("Camera not ready");
      return;
    }

    const caps = camCaps ?? {maxWidth: 1280, maxHeight: 720, maxFrameRate: 30};
    const camLong = Math.max(caps.maxWidth, caps.maxHeight);
    const camShort = Math.min(caps.maxWidth, caps.maxHeight);
    const ratio = force169 ? (16 / 9) : camLong / camShort;

    if (stream?.status !== "live") {
      await startMutation.mutateAsync({rotation, tier: selectedTier, aspectRatio: ratio});
    }

    const mimeType = ["video/webm;codecs=h264,opus", "video/webm;codecs=vp8,opus", "video/webm"]
      .find(m => MediaRecorder.isTypeSupported(m));
    const recorder = new MediaRecorder(ms, {
      mimeType,
      videoBitsPerSecond: getBitrate(selectedTier),
    });
    recorder.ondataavailable = e => {
      if (e.data.size > 0) sendChunk(e.data);
    };

    // Drive requestData() from a Web Worker so the interval survives mild timer
    // throttling (e.g. Chrome in background before full frame-delivery stops).
    recorder.start();
    const workerSrc = "setInterval(() => postMessage(null), 2000)";
    const workerBlob = new Blob([workerSrc], { type: "text/javascript" });
    const workerUrl = URL.createObjectURL(workerBlob);
    const chunkWorker = new Worker(workerUrl);
    chunkWorker.onmessage = () => {
      if (mediaRecorderRef.current?.state === "recording") {
        mediaRecorderRef.current.requestData();
      }
    };
    chunkWorkerRef.current = { worker: chunkWorker, url: workerUrl };
    mediaRecorderRef.current = recorder;
    setElapsed(0);
    setBytesSent(0);
    setIsLive(true);
  };

  const handleStop = async () => {
    if (chunkWorkerRef.current) {
      chunkWorkerRef.current.worker.terminate();
      URL.revokeObjectURL(chunkWorkerRef.current.url);
      chunkWorkerRef.current = null;
    }
    mediaRecorderRef.current?.stop();
    setIsLive(false);
    setViewerCount(null);
    await stopMutation.mutateAsync();
  };
  // Keep ref in sync so the stall-detection interval always has the latest
  // version even though the function is not wrapped in useCallback.
  useLayoutEffect(() => {
    handleStopRef.current = handleStop;
  });

  const toggleFacing = () => {
    if (!isLive) {
      setForce169(false);
      setFacingMode(f => f === "environment" ? "user" : "environment");
    }
  };
  const toggleAspect = () => {
    if (!isLive) setForce169(f => !f);
  };
  const rotateLeft = () => {
    if (!isLive) setRotation(r => ((r - 90 + 360) % 360) as Rotation);
  };
  const rotateRight = () => {
    if (!isLive) setRotation(r => ((r + 90) % 360) as Rotation);
  };

  // ── Preview CSS ───────────────────────────────────────────────────────────
  const previewStyle: React.CSSProperties = (() => {
    const deg = `${rotation}deg`;
    if (rotation === 90) return {
      position: "absolute", inset: 0, width: "100vh",
      transform: `rotate(${deg})`, transformOrigin: "top left",
      marginLeft: `calc((100vw / 2) + ((100vh * ${(camDims?.h ?? 1) / (camDims?.w ?? 1)}) / 2))`,
    };
    if (rotation === 270) return {
      position: "absolute", inset: 0, width: "100vh",
      transform: `rotate(${deg})`, transformOrigin: "top left",
      marginTop: "100vh",
      marginLeft: `calc((100vw / 2) - ((100vh * ${(camDims?.h ?? 1) / (camDims?.w ?? 1)}) / 2))`,
    };
    return {
      position: "absolute", inset: 0, width: "100%", height: "100%",
      transform: rotation === 0 ? undefined : `rotate(${deg})`,
    };
  })();

  const fmt = (s: number) =>
    `${String(Math.floor(s / 3600)).padStart(2, "0")}:${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;

  const fmtBytes = (b: number) => {
    if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
    if (b < 1024 * 1024 * 1024) return `${(b / (1024 * 1024)).toFixed(1)} MB`;
    return `${(b / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  };

  // ── Aspect ratio helpers ──────────────────────────────────────────────────
  const nativeRatio = camCaps
    ? Math.max(camCaps.maxWidth, camCaps.maxHeight) / Math.min(camCaps.maxWidth, camCaps.maxHeight)
    : null;
  const isNear169 = nativeRatio !== null && Math.abs(nativeRatio - 16 / 9) / (16 / 9) < 0.02;

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <div className="fixed inset-0 bg-black overflow-hidden">
      <div className="relative w-full h-full overflow-visible">
        <video
          ref={videoRef}
          autoPlay
          playsInline
          muted
          onLoadedMetadata={handleVideoMetadata}
          style={previewStyle}
        />

        {/* Top overlay bar */}
        <div
          className="absolute top-0 left-0 right-0 flex items-center justify-between p-3 bg-linear-to-b from-black/70 to-transparent pointer-events-none">
          <button
            onClick={() => navigate("/")}
            className="pointer-events-auto text-sm px-3 py-1.5 rounded-lg bg-black/50 backdrop-blur-sm text-gray-300 hover:text-white transition-colors"
          >
            ← Back
          </button>

          <div className="pointer-events-auto flex items-center gap-2 flex-wrap justify-end">
            {isLive ? (
              <>
                <div className="flex items-center gap-2 bg-black/50 backdrop-blur-sm px-3 py-1.5 rounded-lg">
                  <span className="relative flex h-2 w-2">
                    <span
                      className="animate-ping absolute inline-flex h-full w-full rounded-full bg-red-400 opacity-75"/>
                    <span className="relative inline-flex rounded-full h-2 w-2 bg-red-500"/>
                  </span>
                  <span className="text-red-400 text-sm font-medium">LIVE</span>
                  <div className="flex flex-col items-end">
                    <span className="text-gray-300 text-sm font-mono leading-tight">{fmt(elapsed)}</span>
                    <span className="text-gray-500 text-xs font-mono leading-tight">{fmtBytes(bytesSent)}</span>
                  </div>
                </div>
                <button
                  onClick={handleStop}
                  disabled={stopMutation.isPending}
                  className="px-4 py-1.5 rounded-lg border border-red-500/60 text-red-400 bg-black/50 backdrop-blur-sm hover:bg-red-500/20 disabled:opacity-50 text-sm font-medium transition-colors"
                >
                  {stopMutation.isPending ? "Stopping…" : "End Stream"}
                </button>
              </>
            ) : (
              <>
                {/* Tier picker */}
                <select
                  value={selectedTier}
                  onChange={e => setSelectedTier(e.target.value as Tier)}
                  disabled={availTiers.length === 0}
                  className="text-xs px-2 py-1.5 rounded-lg bg-black/50 backdrop-blur-sm border border-white/10 text-gray-300 focus:outline-none focus:border-blue-500 disabled:opacity-40"
                >
                  {(availTiers.length > 0 ? availTiers : (["QHD", "FHD", "HD"] as Tier[])).map(t => (
                    <option key={t} value={t}>{t}</option>
                  ))}
                </select>
                {hasMultipleCameras && (
                  <button
                    onClick={toggleFacing}
                    title={facingMode === "environment" ? "Switch to front camera" : "Switch to back camera"}
                    className="text-xs px-3 py-1.5 rounded-lg bg-black/50 backdrop-blur-sm border border-white/10 text-gray-300 hover:text-white transition-colors"
                  >
                    {facingMode === "environment" ? "Back cam" : "Front cam"}
                  </button>
                )}
                {camCaps && !isNear169 && (
                  <button
                    onClick={toggleAspect}
                    title={force169 ? `Switch to camera aspect ratio (${formatRatioLabel(camCaps)})` : "Switch to 16:9"}
                    className={`text-xs px-3 py-1.5 rounded-lg backdrop-blur-sm border transition-colors ${
                      force169
                        ? "bg-blue-500/20 border-blue-500/40 text-blue-300"
                        : "bg-black/50 border-white/10 text-gray-300 hover:text-white"
                    }`}
                  >
                    {force169 ? formatRatioLabel(camCaps) : "16:9"}
                  </button>
                )}
                <div
                  className="flex items-center gap-1 bg-black/50 backdrop-blur-sm border border-white/10 rounded-lg overflow-hidden">
                  <button onClick={rotateLeft} title="Rotate left 90°"
                          className="text-xs px-2 py-1.5 text-gray-300 hover:text-white hover:bg-white/10 transition-colors">↺
                  </button>
                  <span className="text-xs text-gray-400 px-1 select-none">{ROTATION_LABELS[rotation]}</span>
                  <button onClick={rotateRight} title="Rotate right 90°"
                          className="text-xs px-2 py-1.5 text-gray-300 hover:text-white hover:bg-white/10 transition-colors">↻
                  </button>
                </div>
                <button
                  onClick={handleGoLive}
                  disabled={!cameraReady || startMutation.isPending}
                  className="flex items-center gap-2 px-4 py-1.5 rounded-lg bg-red-600 hover:bg-red-500 disabled:opacity-50 text-white text-sm font-medium transition-colors shadow-lg shadow-red-900/40"
                >
                  <span className="w-2 h-2 rounded-full bg-white"/>
                  {startMutation.isPending ? "Starting…" : "Go Live"}
                </button>
              </>
            )}
          </div>
        </div>

        {/* Viewer count — shown in bottom-left while live. */}
        {isLive && viewerCount !== null && (
          <div className="absolute bottom-4 left-4">
            <p className="text-xs text-gray-500 bg-black/50 backdrop-blur-sm px-2 py-1.5 rounded-lg border border-white/10">
              {viewerCount} {viewerCount === 1 ? "viewer" : "viewers"}
            </p>
          </div>
        )}

        {/* Error toast */}
        {error && (
          <div className="absolute bottom-4 left-4 right-4">
            <p
              className="text-sm text-red-400 bg-red-500/20 backdrop-blur-sm border border-red-500/30 rounded-lg px-3 py-2">
              {error}
            </p>
          </div>
        )}

        {/* Stream dimensions */}
        {camDims && (
          <div className="absolute bottom-4 right-4">
            <p
              className="text-xs text-gray-400 bg-black/50 backdrop-blur-sm px-2 py-1.5 rounded-lg border border-white/10">
              {camDims.w} × {camDims.h}
            </p>
          </div>
        )}
      </div>
    </div>
  );
}

function gcd(a: number, b: number): number {
  return b === 0 ? a : gcd(b, a % b);
}

function formatRatioLabel(caps: CamCaps): string {
  const w = Math.max(caps.maxWidth, caps.maxHeight);
  const h = Math.min(caps.maxWidth, caps.maxHeight);
  const g = gcd(w, h);
  return `${w / g}:${h / g}`;
}

function getBitrate(tier: Tier): number {
  return ({QHD: 10_000_000, FHD: 5_000_000, HD: 2_500_000})[tier] ?? 5_000_000;
}
