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
- Added a `channel_management_screen.dart` allowing users to hide channels and edit logos from the Flutter Settings page.
- Updated `guide_screen.dart` to filter out hidden channels on the frontend (`filteredChannels = channels.where((c) => !c.isHidden)`).
- Removed inline channel editing from the Guide UI to clean up the interface.
- Added `is_hidden` column to the `channels` SQLite table.
- Added `PUT /api/channels/{id}/visibility` API endpoint.
- **Fixed a critical bug:** The go-sqlite3 driver silently fails to map `INTEGER` columns (used for booleans like `is_hidden`) into `sql.NullBool`. We updated `handlers.go` `GetChannels` to use `sql.NullInt64` instead and mapped it manually.

## Current Problem (End of Session)
- **Deployment Handoff:** The user wants to start a new chat because the backend was updated locally, but the final `go build` and restart has not been executed on the dev-server (`192.168.4.143`). The user's Flutter app is correctly configured to hide channels, but it's hitting the old un-updated backend, which constantly tells Flutter that `is_hidden = false`. 

## Next Steps (Recommended)
1. **Verify Deployment:** The next agent needs to assist the user in deploying the recent `handlers.go` changes to the dev-server (`192.168.4.143`) and restarting the backend.
2. **Review EPG Guide Data Bug:** Address the ongoing issue where EPG guide data is not showing up despite being successfully parsed. Verify `guide_screen.dart` rendering logic and `epg_programs` mapping in the DB.
3. Get the user's feedback on whether the new optimized GStreamer pipeline starts up fast enough.
4. Address any remaining custom UI overlay tasks in `player_screen.dart`.
