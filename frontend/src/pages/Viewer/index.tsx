import { useState, useEffect, useRef, useCallback } from "react";
import { useParams } from "react-router-dom";
import * as dashjs from "dashjs";
import { linksApi, type WatchInfo } from "../../api/links";
import { useWebSocket } from "../../hooks/useWebSocket";
import { useStreamPolling } from "../../hooks/useStreamPolling";

// Detect native HLS support once at module load time.
// Safari (macOS and iOS) reports a non-empty canPlayType for HLS and handles
// live playlists natively without a JavaScript player. We additionally check
// navigator.vendor so that browsers like Firefox, which may return "maybe" for
// this MIME type in recent versions, still fall through to DASH.js.
// Add ?hls to the URL to force HLS mode for testing on non-Safari browsers.
const supportsNativeHLS: boolean =
  (typeof document !== "undefined" &&
    document.createElement("video").canPlayType("application/vnd.apple.mpegurl") !== "" &&
    navigator.vendor.includes("Apple")) ||
  new URLSearchParams(window.location.search).has("hls");

export default function ViewerPage() {
  const { token } = useParams<{ token: string }>();
  const [info, setInfo] = useState<WatchInfo | null>(null);
  const [password, setPassword] = useState("");
  const [authError, setAuthError] = useState<string | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [archiveId, setArchiveId] = useState<string | null>(null);
  const [isAtLive, setIsAtLive] = useState(true);
  const videoRef = useRef<HTMLVideoElement>(null);
  const playerRef = useRef<dashjs.MediaPlayerClass | null>(null);

  const shouldPoll = !!info && info.stream_status === "scheduled" && !info.requires_password;
  const { data: polledInfo } = useStreamPolling(token!, shouldPoll);
  useEffect(() => { if (polledInfo) setInfo(polledInfo); }, [polledInfo]);

  useEffect(() => {
    if (!token) return;
    linksApi.watchInfo(token).then(setInfo).catch(() => setLoadError("Stream not found or link has expired."));
  }, [token]);

  useEffect(() => {
    const title = info?.stream_title;
    document.title = title ? `${title} – Streamer` : "Streamer";
  }, [info?.stream_title]);

  useWebSocket(info?.stream_id, (msg) => {
    if (msg.type === "stream.started") {
      setInfo((prev) => prev ? {
        ...prev,
        stream_status: "live",
        dash_url: msg.payload.dash_url as string,
        hls_url: msg.payload.hls_url as string,
      } : prev);
    }
    if (msg.type === "stream.ended") {
      setInfo((prev) => prev ? { ...prev, stream_status: "ended" } : prev);
      if (msg.payload.archive_id) setArchiveId(msg.payload.archive_id as string);
    }
  });

  // Monitor live latency for the LIVE/DVR badge.
  // Native HLS players don't expose latency via JS, so we keep the badge
  // pinned to "live" for them.
  useEffect(() => {
    if (info?.stream_status !== "live") return;
    if (supportsNativeHLS) { setIsAtLive(true); return; }
    const id = setInterval(() => {
      const player = playerRef.current;
      if (!player) return;
      try {
        const latency = player.getCurrentLiveLatency();
        setIsAtLive(latency < 15);
      } catch {
        // DVR info not yet available; keep current state
      }
    }, 1000);
    return () => clearInterval(id);
  }, [info?.stream_status]);

  const jumpToLive = useCallback(() => {
    const player = playerRef.current;
    if (!player) return;
    // seekToOriginalLive() moves to the live edge as tracked by DASH.js,
    // accounting for the configured liveDelay. Avoids the race condition of
    // setting video.currentTime directly while DASH.js is buffering.
    player.seekToOriginalLive();
    setIsAtLive(true);
  }, []);

  useEffect(() => {
    // Native HLS (Safari/iOS): the <video src> attribute handles playback
    // directly — no JavaScript player needed.
    if (supportsNativeHLS) return;

    if (info?.stream_status !== "live" || !info.dash_url || !videoRef.current) return;

    const dashUrl = info.dash_url;
    let activePlayer: dashjs.MediaPlayerClass | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let retries = 0;
    const MAX_RETRIES = 4;

    // DASH.js can emit an unhandled internal promise rejection with
    // "getCurrentDVRInfo() is null" when the live stream was just started
    // and the DVR timeline hasn't been populated yet. We detect this stall by
    // checking whether playback has actually begun a few seconds after the
    // manifest loads, and reinitialise if not.
    function initPlayer() {
      activePlayer?.reset();
      const player = dashjs.MediaPlayer().create();
      activePlayer = player;
      playerRef.current = player;

      player.updateSettings({
        streaming: {
          delay: {
            liveDelay: 4, // start 4 seconds behind live (2x segment duration)
          },
          // liveCatchup must be disabled for DVR scrubbing to work. When enabled,
          // DASH.js detects that latency has grown (because the user scrubbed back)
          // and immediately seeks forward to close the gap, overriding the user.
          liveCatchup: {
            enabled: false,
          },
          buffer: {
            bufferTimeDefault: 6,    // 3x segment duration (6 seconds)
            bufferToKeep: 30,        // ~30 seconds of DVR history
            stallThreshold: 0.5,
          },
          retryAttempts: {
            MPD: 10,
            MediaSegment: 10,
            InitializationSegment: 10,
          },
          retryIntervals: {
            MPD: 1000,
            MediaSegment: 1000,
            InitializationSegment: 1000,
          },
          abr: {
            autoSwitchBitrate: { video: false }, // single quality, no ABR needed
          },
        },
      });

      player.initialize(videoRef.current!, dashUrl, /* autoPlay */ true);

      player.on(dashjs.MediaPlayer.events.PLAYBACK_STARTED, () => {
        // Playback is running — cancel any pending retry.
        if (retryTimer) { clearTimeout(retryTimer); retryTimer = null; }
      });

      player.on(dashjs.MediaPlayer.events.MANIFEST_LOADED, () => {
        console.log("DASH manifest loaded");
        // If the video hasn't started playing after 5 s (e.g. due to the
        // DVR-null crash on a freshly-started stream), reinitialise.
        retryTimer = setTimeout(() => {
          retryTimer = null;
          const video = videoRef.current;
          if (!video || video.currentTime > 0) return; // already playing
          if (retries >= MAX_RETRIES) return;
          retries++;
          console.log(`DASH.js stalled after manifest load — retrying (${retries}/${MAX_RETRIES})`);
          initPlayer();
        }, 5000);
      });

      player.on(dashjs.MediaPlayer.events.ERROR, (e: dashjs.ErrorEvent) => {
        console.error("DASH error:", e.error);
      });
    }

    initPlayer();

    return () => {
      if (retryTimer) clearTimeout(retryTimer);
      playerRef.current = null;
      activePlayer?.reset();
    };
  }, [info?.stream_status, info?.dash_url]);

  const handlePasswordSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setAuthError(null);
    try {
      setInfo(await linksApi.watchAuth(token!, password));
    } catch {
      setAuthError("Incorrect password. Please try again.");
    }
  };

  if (loadError) return (
    <div className="flex-1 flex items-center justify-center">
      <div className="text-center">
        <p className="text-4xl mb-3">🔗</p>
        <p className="text-gray-400">{loadError}</p>
      </div>
    </div>
  );

  if (!info) return (
    <div className="flex-1 flex items-center justify-center">
      <div className="w-6 h-6 rounded-full border-2 border-blue-500 border-t-transparent animate-spin" />
    </div>
  );

  // Password gate
  if (info.requires_password) return (
    <div className="flex-1 flex items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="bg-[#1a1b23] border border-[#2e3042] rounded-xl p-6 text-center">
          <p className="text-2xl mb-3">🔒</p>
          <h2 className="text-white font-semibold mb-1">{info.stream_title || "Protected Stream"}</h2>
          <p className="text-gray-400 text-sm mb-5">This stream requires a password.</p>
          <form onSubmit={handlePasswordSubmit} className="space-y-3">
            <input
              type="password"
              placeholder="Enter password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              className="w-full px-3 py-2 rounded-lg bg-[#13141a] border border-[#2e3042] text-white placeholder-gray-600 text-sm focus:outline-none focus:border-blue-500 transition-colors"
            />
            {authError && <p className="text-sm text-red-400">{authError}</p>}
            <button type="submit" className="w-full py-2.5 rounded-lg bg-blue-600 hover:bg-blue-500 text-white font-medium transition-colors">
              Watch
            </button>
          </form>
        </div>
      </div>
    </div>
  );

  // Scheduled
  if (info.stream_status === "scheduled") {
    const scheduled = info.stream_scheduled_at ? new Date(info.stream_scheduled_at) : null;
    return (
      <div className="flex-1 flex items-center justify-center p-4">
        <div className="text-center max-w-sm">
          <div className="inline-flex items-center justify-center w-14 h-14 rounded-full bg-yellow-500/10 border border-yellow-500/20 mb-4">
            <span className="text-2xl">📅</span>
          </div>
          <h2 className="text-white font-semibold text-lg mb-2">{info.stream_title}</h2>
          {scheduled && scheduled > new Date() ? (
            <>
              <p className="text-gray-400 text-sm">Scheduled to start at</p>
              <p className="text-yellow-400 font-medium mt-1">{scheduled.toLocaleString()}</p>
            </>
          ) : (
            <p className="text-gray-400 text-sm">The stream is about to start…</p>
          )}
          <p className="text-gray-600 text-xs mt-4">This page updates automatically.</p>
        </div>
      </div>
    );
  }

  // Ended — show archive player if available, otherwise a placeholder
  if (info.stream_status === "ended") {
    const videoUrl = info.archive_url || (archiveId ? `/api/watch/${token}/video` : null);
    return (
      <div className="flex-1 flex flex-col bg-black">
        {videoUrl ? (
          <>
            <div className="relative">
              <video
                src={videoUrl}
                controls
                playsInline
                className="w-full max-h-[85vh] bg-black"
              />
            </div>
            <div className="p-4 border-t border-[#2e3042] flex items-center justify-between gap-4">
              <h2 className="text-white font-medium truncate">{info.stream_title}</h2>
              <a
                href={`${videoUrl}?download=1`}
                download
                className="shrink-0 flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[#2e3042] hover:bg-[#3a3d54] text-gray-300 hover:text-white text-sm font-medium transition-colors"
              >
                <svg xmlns="http://www.w3.org/2000/svg" className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
                </svg>
                Download
              </a>
            </div>
          </>
        ) : (
          <div className="flex-1 flex items-center justify-center p-4">
            <div className="text-center">
              <p className="text-4xl mb-3">✅</p>
              <h2 className="text-white font-semibold mb-2">Stream Ended</h2>
              <p className="text-gray-400 text-sm">The recording will be available shortly.</p>
            </div>
          </div>
        )}
      </div>
    );
  }

  // Live player
  return (
    <div className="flex-1 flex flex-col bg-black">
      <div className="relative">
        {/* Live / DVR badge + jump-to-live button (DASH.js only) */}
        <div className="absolute top-3 left-3 z-10 flex items-center gap-2">
          <div className="flex items-center gap-2 bg-black/60 backdrop-blur px-2.5 py-1 rounded-full">
            <span className="relative flex h-2 w-2">
              {isAtLive ? (
                <>
                  <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-red-400 opacity-75" />
                  <span className="relative inline-flex rounded-full h-2 w-2 bg-red-500" />
                </>
              ) : (
                <span className="relative inline-flex rounded-full h-2 w-2 bg-gray-500" />
              )}
            </span>
            <span className="text-white text-xs font-medium">{isAtLive ? "LIVE" : "DVR"}</span>
          </div>
          {!isAtLive && !supportsNativeHLS && (
            <button
              onClick={jumpToLive}
              className="flex items-center gap-1.5 bg-red-600 hover:bg-red-500 text-white text-xs font-medium px-3 py-1 rounded-full transition-colors"
            >
              <span className="relative inline-flex rounded-full h-2 w-2 bg-white" />
              Jump to Live
            </button>
          )}
        </div>
        {/* Native HLS: set src directly so Safari/iOS handles the playlist.
            DASH.js: leave src empty and let the useEffect attach the player. */}
        <video
          ref={videoRef}
          src={supportsNativeHLS ? info.hls_url : undefined}
          autoPlay
          controls
          playsInline
          className="w-full max-h-[85vh] bg-black"
        />
      </div>
      <div className="p-4 border-t border-[#2e3042]">
        <h2 className="text-white font-medium">{info.stream_title}</h2>
      </div>
    </div>
  );
}
