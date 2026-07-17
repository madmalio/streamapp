# StreamApp Handoff

## Current Architecture (Hybrid HLS)
- The user is building `streamapp`, a Flutter TV app (`tv_app`) and a Go backend (`backend`) for HDHomeRun live TV.
- We successfully refactored the streaming engine to a **Hybrid Architecture** leveraging MediaMTX to achieve zero-buffering / zero-disk-I/O:
  - **Go Backend:** Responsible for translating API calls from Flutter, managing the FFmpeg/GStreamer lifecycles, and instantly killing transcoding processes to instantly free tuners.
  - **FFmpeg/GStreamer:** Now pushes the stream *directly* into MediaMTX via RTSP (FFmpeg) or SRT (GStreamer) instead of writing `.ts` chunks to SSD.
  - **MediaMTX:** Demuxes the RTSP/SRT streams and hosts the LL-HLS segments in RAM (`http://<ip>:8888/hls_<id>/index.m3u8`).
- **MediaMTX Config**: The user's `mediamtx.yml` is heavily customized using regex paths. To allow the Go backend to publish streams, an `all_others:` catch-all path was added to `mediamtx.yml`.

## Important Environment / Ops Notes
- Home server target IP in session: `192.168.4.143`.
- SSH User: `mark`
- Deployment commands (IMPORTANT: AI must provide these exactly when modifying backend code):
  - Push: `scp backend/internal/handlers/handlers.go mark@192.168.4.143:/home/mark/streamapp/backend/internal/handlers/`
  - Build: `go build -o streamapp-backend ./cmd/server`
  - Run: `./streamapp-backend`

## What Was Proven & Modified Today
- Old WebRTC routing in the Go backend was deleted because the Flutter app now directly contacts MediaMTX's `runOnDemand` WHEP path for WebRTC.
- The GStreamer pipeline was aggressively optimized for low latency (`latency=0`, `max-bframes=0`, `config-interval=-1`, `alignment=7`) and pushes to MediaMTX via `srtclientsink`.
- The FFmpeg pipeline was modified to push to MediaMTX via `rtsp`.
- The Go backend now polls localhost (`127.0.0.1:8888/hls_<id>/index.m3u8`) to verify when MediaMTX is ready, preventing firewall loopback issues.

## Current Problem (End of Session)
- We finalized the Hybrid HLS Architecture. FFmpeg was proven to be extremely fast. GStreamer was slightly slower to start up due to pipeline initialization, but the user is about to test the aggressively optimized GStreamer pipeline provided right at the end of the session.

## Next Steps (Recommended)
1. Get the user's feedback on whether the new optimized GStreamer pipeline starts up fast enough.
2. Address any remaining custom UI overlay tasks in `player_screen.dart` (the user previously mentioned that the `media_kit_video` `MaterialDesktopVideoControlsTheme` absorbs touch events, requiring overlays to be placed outside the bottom control bar).
3. If HLS latency is still an issue, continue optimizing MediaMTX's `hlsSegmentDuration` and `hlsPartDuration`.
