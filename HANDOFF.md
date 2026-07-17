# StreamApp Handoff

## Current State
- The user is building `streamapp`, a Flutter TV app (`tv_app`) and a Go backend (`backend`) for HDHomeRun live TV.
- Backend and app were heavily updated during this session. Current code now supports:
  - FFmpeg VAAPI transcode path (HLS fMP4 by default)
  - Fast-start query mode (`fast=1`)
  - Prewarm session classification (`prewarm=1`) with shorter timeout
  - Transmux test mode (`transmux=1`)
  - App-side `Original HLS` quality option (transmux test lane)
- API base URL is configurable in app settings and persisted in SharedPreferences.

## Important Environment / Ops Notes
- Home server target IP in session: `192.168.4.143`.
- HDHomeRun source used most in testing: `http://192.168.4.22:5004/auto/v7.1` (also `v13.1` and others).
- Backend runtime envs commonly used:
  - `FFMPEG_VAAPI_DEVICE=/dev/dri/renderD128`
  - `FFMPEG_HLS_START_TIMEOUT_SECONDS=90`
  - `FFMPEG_HLS_SESSION_TIMEOUT_SECONDS=300`
  - `FFMPEG_HLS_PREWARM_TIMEOUT_SECONDS=30`
- Immediate tuner release command when stuck:
  - `curl -s "http://127.0.0.1:8080/api/streams/stop_all"`

## What Was Proven
- VAAPI FFmpeg transcode is functional and can run >1.0x on tested channels.
- fMP4 HLS looked good in quality tests.
- GStreamer is installed on server and can produce HLS output.
- A working GStreamer baseline pipeline was found with relatively good startup:
  - `souphttpsrc` + `decodebin`
  - `videoconvert ! video/x-raw,format=NV12 ! vaapih264enc bitrate=4500 keyframe-period=60`
  - `hlssink2 target-duration=4 playlist-length=6 max-files=20`

## Current Problem (End of Session)
- User still sees startup/sustained buffering in app under some paths.
- Prewarm experiments caused tuner contention and stuck tuners; client prewarm was disabled in guide screen for stability.
- GStreamer experiments showed promising startup but inconsistent long-run behavior and occasional artifacts depending on pipeline variant.
- User explicitly wants to continue evaluating GStreamer and compare it against FFmpeg in app context.

## User Preference / Direction for Next Chat
- The user wants to continue with **GStreamer exploration**, not default back to FFmpeg-only discussions.
- Keep the process simple and avoid overcomplicated steps.
- Prioritize practical in-app comparison and reproducible test flows.

## Next Steps (Recommended)
1. Validate one stable GStreamer pipeline and freeze it as baseline (no further speculative variants until measured).
2. Add a clean app toggle/path for FFmpeg vs GStreamer stream source so A/B testing is one click.
3. Measure and compare:
   - time-to-first-frame
   - 5-minute buffering behavior
   - tuner lock/release behavior
4. Only then decide whether to keep FFmpeg, move to GStreamer, or run hybrid.
