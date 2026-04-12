// Package ffmpeg provides shared helpers for building ffmpeg argument lists.
package ffmpeg

import (
	"log"
	"os/exec"
	"sync"

	"github.com/c0d3d3v/streamer-2/internal/config"
)

// HWAccel describes a hardware acceleration backend and the ffmpeg flags it
// requires for both the decode (input) and encode (output) sides.
type HWAccel struct {
	// Name is the config value, e.g. "qsv".
	Name string
	// DecodeArgs are the ffmpeg flags inserted *before* the -i input flag.
	// They tell ffmpeg which hardware device to use for decoding.
	DecodeArgs []string
	// VideoEncoder is the ffmpeg codec name for the hardware encoder,
	// e.g. "h264_qsv". Empty means "keep stream-copy behaviour".
	VideoEncoder string
	// liveDecodeArgs are the flags inserted before -i specifically for the live
	// encode pipeline (device init without hwaccel decode, since we upload
	// frames from CPU via the filter graph instead).
	liveDecodeArgs []string
	// liveVideoArgs are the -vf / -c:v / encoder-option flags used in the live
	// encode pipeline. They replace the software defaults when hw is active.
	liveVideoArgs []string
	// probeArgs are the full ffmpeg argument list (after -hide_banner
	// -loglevel error) used to verify this backend is actually available.
	probeArgs []string
}

// known is the table of all supported hardware acceleration backends.
// Keeping this as a plain slice (not a map) makes the order deterministic.
var known = []HWAccel{
	{
		Name: "qsv",
		// Archive decode flags (used when re-reading encoded material).
		// -init_hw_device qsv=hw  – initialise the QSV device and name it "hw"
		// -hwaccel qsv            – use QSV for hardware-accelerated decode
		// -hwaccel_output_format qsv – keep decoded frames on the GPU surface
		DecodeArgs:   []string{"-init_hw_device", "qsv=hw", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv"},
		VideoEncoder: "h264_qsv",
		// Live pipeline: initialise the device but do not request hw decode
		// output format — browser frames arrive on CPU and are uploaded via
		// the filter graph (format=nv12,hwupload,...).
		// -filter_hw_device tells the hwupload filter which device to target;
		// without it, ffmpeg may not bind hwupload to the QSV surface.
		// -fflags +genpts regenerates missing PTS values from the WebM input,
		// preventing timestamp holes that cause the DASH muxer to stall.
		// -flags +cgop forces closed GOPs so every segment is independently
		// decodable — critical for "Jump to live" seeks in DASH/HLS players.
		// -fflags +genpts regenerates missing PTS values from the WebM input,
		// preventing timestamp holes that cause the DASH muxer to stall.
		// -flags +cgop is intentionally omitted: -forced-idr 1 on the encoder
		// already ensures closed IDR boundaries on the output; applying +cgop to
		// the input side is redundant and may add muxer processing overhead.
		liveDecodeArgs: []string{"-init_hw_device", "qsv=hw", "-filter_hw_device", "hw", "-fflags", "+genpts"},
		liveVideoArgs: []string{
			// Explicitly convert to nv12 on CPU before uploading to the QSV
			// surface. This removes ambiguity about the input pixel format and
			// avoids implicit multi-step conversions inside hwupload.
			// extra_hw_frames=16: enough for async_depth=1 + 2 reference frames
			// + pipeline margin. 64 (previous value) pre-allocates far more than
			// needed and may hint to the driver that a deeper pipeline is expected.
			"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=nv12,hwupload=extra_hw_frames=16,format=qsv",
			"-c:v", "h264_qsv",
			// Tell the muxer the output pixel format is qsv (on-GPU surface).
			"-pix_fmt", "qsv",
			// h264_qsv has no CRF mode; an explicit bitrate is required.
			// -maxrate/-bufsize force CBR mode (VBR by default). Without this the
			// encoder banks bits during low-complexity scenes by delaying frame
			// output, which causes segments to appear later than the wall-clock
			// timing the player expects, producing intermittent 404 errors on
			// live-edge segments.
			"-b:v", "4M",
			"-maxrate", "4M",
			// bufsize=1M (0.25s at 4 Mbps): tight VBV window forces the encoder
			// to flush frequently rather than smoothing over a full second.
			// Previous value (4M = 1s window) was a major contributor to the
			// 20s live latency vs VAAPI's 8s.
			"-bufsize", "1M",
			// low_power=1 selects VDEnc (fixed-function encoder). Required on
			// 12th gen+ Intel (Alder Lake / Raptor Lake) which removed the VME
			// engine — without this flag every parameter is reported unsupported.
			"-low_power", "1",
			// look_ahead is incompatible with low_power mode and adds latency.
			"-look_ahead", "0",
			// async_depth=1 minimises internal frame buffering. The default (4)
			// causes the encoder to hold back several frames before outputting,
			// which makes the stream slow to start and slow to recover after a
			// "back to live" seek.
			"-async_depth", "1",
			// Disable B-frames: they require a reorder buffer that adds latency
			// and can stall the decoder when seeking to the live edge.
			"-bf", "0",
			// forced-idr=1 ensures keyframe requests (from -force_key_frames)
			// produce true IDR frames rather than open-GOP I-frames. Without
			// this, hardware encoders may insert I-frames that still reference
			// the previous segment's context, forcing the player to seek back
			// further and causing the long "Jump to live" loading delay.
			"-forced-idr", "1",
			// veryfast maps to the highest target-usage (TU7) for VDEnc, which
			// disables temporal features that add pipeline depth. Owncast uses
			// veryfast for the same reason. Previous value "fast" (≈TU5) was a
			// significant contributor to the elevated latency vs VAAPI.
			"-preset", "veryfast",
		},
		probeArgs: []string{
			"-init_hw_device", "qsv=hw",
			"-f", "lavfi", "-i", "color=black:s=64x64:r=1",
			"-vframes", "1",
			"-vf", "format=nv12,hwupload=extra_hw_frames=64",
			"-c:v", "h264_qsv",
			"-b:v", "1M",
			"-low_power", "1",
			"-look_ahead", "0",
			"-f", "null", "-",
		},
	},
	{
		Name: "nvenc",
		// NVENC accepts CPU frames directly; no special decode flags needed.
		DecodeArgs:   []string{"-hwaccel", "cuda"},
		VideoEncoder: "h264_nvenc",
		// Live pipeline: NVENC takes CPU frames, so no pre-input flags needed.
		liveDecodeArgs: nil,
		liveVideoArgs: []string{
			"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
			"-c:v", "h264_nvenc",
		},
		probeArgs: []string{
			"-f", "lavfi", "-i", "color=black:s=64x64:r=1",
			"-vframes", "1",
			"-c:v", "h264_nvenc",
			"-f", "null", "-",
		},
	},
	{
		Name: "vaapi",
		// VA-API: generic Linux hardware acceleration (AMD, Intel).
		// -hwaccel_output_format vaapi – keep frames on VA-API surface.
		DecodeArgs:   []string{"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128", "-hwaccel_output_format", "vaapi"},
		VideoEncoder: "h264_vaapi",
		// Live pipeline: open the render node for the encoder, then upload
		// CPU frames via format=nv12,hwupload in the filter graph.
		liveDecodeArgs: []string{"-vaapi_device", "/dev/dri/renderD128"},
		liveVideoArgs: []string{
			"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=nv12,hwupload",
			"-c:v", "h264_vaapi",
			// Force true IDR frames at keyframe boundaries so each DASH segment
			// is independently decodable and "Jump to live" seeks resolve quickly.
			"-forced-idr", "1",
		},
		probeArgs: []string{
			"-vaapi_device", "/dev/dri/renderD128",
			"-f", "lavfi", "-i", "color=black:s=64x64:r=1",
			"-vframes", "1",
			"-vf", "format=nv12,hwupload",
			"-c:v", "h264_vaapi",
			"-f", "null", "-",
		},
	},
}

// autoDetected caches the result of Detect so the probe runs only once.
var (
	autoOnce     sync.Once
	autoDetected HWAccel
)

// Detect probes each known backend in order and returns the first one that
// ffmpeg can actually use on this machine. Returns a zero-value HWAccel if
// none are available. The result is cached after the first call.
func Detect() HWAccel {
	autoOnce.Do(func() {
		for _, h := range known {
			args := append([]string{"-hide_banner", "-loglevel", "error"}, h.probeArgs...)
			if err := exec.Command("ffmpeg", args...).Run(); err == nil {
				log.Printf("hwaccel: auto-detected backend: %s", h.Name)
				autoDetected = h
				return
			}
		}
		log.Printf("hwaccel: auto-detection found no working hardware backend, falling back to software")
	})
	return autoDetected
}

// Active returns the HWAccel configuration for the currently configured
// backend. Returns a zero-value HWAccel (no flags, no encoder) when hardware
// acceleration is disabled.
func Active() HWAccel {
	name := config.Get().App.HWAccel
	if name == "" || name == "none" {
		return HWAccel{}
	}
	if name == "auto" {
		return Detect()
	}
	for _, h := range known {
		if h.Name == name {
			return h
		}
	}
	// Unknown value – fall back to software to avoid a hard crash.
	return HWAccel{}
}

// DecodeArgs returns the ffmpeg input-side flags for the active backend,
// or nil when hardware acceleration is disabled.
func DecodeArgs() []string {
	return Active().DecodeArgs
}

// VideoEncoder returns the codec name for the active hardware encoder
// (e.g. "h264_qsv"), or "copy" when hardware acceleration is disabled.
// Use this value as the argument to -c:v in encode commands.
func VideoEncoder() string {
	if enc := Active().VideoEncoder; enc != "" {
		return enc
	}
	return "copy"
}

// IsEnabled reports whether any hardware acceleration backend is configured.
func IsEnabled() bool {
	return Active().VideoEncoder != ""
}

// LiveDecodeArgs returns the pre-input flags for the live encode pipeline.
// These differ from DecodeArgs: they initialise the hardware device without
// requesting hw-format output, since the live pipeline uploads frames from
// CPU via the filter graph rather than decoding directly into GPU memory.
func LiveDecodeArgs() []string {
	return Active().liveDecodeArgs
}

// LiveVideoArgs returns the video filter and encoder flags for the live encode
// pipeline. When no hardware backend is active it returns software defaults
// (libx264 ultrafast+zerolatency).
func LiveVideoArgs() []string {
	if args := Active().liveVideoArgs; len(args) > 0 {
		return args
	}
	return []string{
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
	}
}
