# StreamApp Handoff

## Current State
- The user is building `streamapp`, a Flutter TV app (`tv_app`) and a Go backend (`backend`) to stream Live TV from an HDHomeRun tuner on their local network.
- The user intends to use this app remotely via Tailscale, which requires the Go backend to transcode the HDHomeRun's raw 1080i MPEG-2 TS stream into lower bitrates (e.g., 1.5 Mbps, 3 Mbps, etc.) using HLS.
- The UI includes a Quality selector (Gear icon) that successfully changes bitrates by making API calls to the Go backend.

## The Problem
- The user is experiencing **severe, persistent buffering** when watching transcoded streams (e.g., at 1.5 Mbps). The player consumes the video faster than the Go backend can encode it.
- FFmpeg transcoder speed constantly drops below 1.0x (usually ~0.85x to ~0.95x).

## What Has Been Tested
- **Hardware Acceleration:** Attempted to use NVIDIA hardware decoding (`-hwaccel cuda`, `-c:v mpeg2_cuvid`, `-hwaccel nvdec`) and NVENC encoding (`-c:v h264_nvenc`) on the user's RTX 3060.
- **Probe Size:** The dirty nature of live ATSC 1.0 antenna streams causes FFmpeg's stream probing to fail if `probesize` is too small (e.g., 5MB). When probing fails, FFmpeg aborts the hardware decoder initialization and falls back to `native` CPU decoding, which bottlenecks the pipeline and causes buffering. 
- **Frame Duplication:** When the antenna drops packets, FFmpeg's default Constant Frame Rate (CFR) logic (`-fps_mode cfr`) duplicates thousands of frames to fill timestamp gaps, which cripples transcode speed. Removing `-fps_mode cfr` slightly improved things but didn't completely solve it because the HLS muxer implicitly prefers a constant frame rate.

## Next Steps
1. **Analyze Jellyfin:** The user noted that Jellyfin effortlessly streams this same hardware without buffering. The next agent should analyze exactly what FFmpeg arguments Jellyfin uses for Live TV HLS transcoding.
2. **Rebuild FFmpeg Pipeline:** Rip out the current FFmpeg command in `backend/internal/handlers/handlers.go` and replace it with a highly resilient pipeline tailored specifically for lossy Live TV TS streams.
3. **Avoid Deinterlacing Bottlenecks:** Avoid CPU-bound deinterlacing (`yadif`) which severely slows down the pipeline when combined with CPU decoding.
