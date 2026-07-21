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
- **Fixed multiple SQLite & driver bugs:** We migrated all boolean fetching to a generic `interface{}` scanner (`parseSQLiteBool`) to bypass unpredictable casting in `go-sqlite3`.
- **Fixed database lock on logos:** Modified `syncEPGSource` to use `tx.Exec` when saving logos to avoid `SQLITE_BUSY` conflicts with the active EPG insertion transaction.
- **Improved XMLTV Matching:** Modified the EPG channel matching algorithm to use fuzzy substring matches (`strings.Contains`) instead of strict prefixes so SiliconDust's EPG guides fully map to the tuner's channel names.

## Current Problem (End of Session)
- **Deployment Handoff:** The backend code has been perfected and is ready for a final push and rebuild on the user's dev-server (`192.168.4.143`).

## Next Steps (Recommended)
1. Get the user's feedback on whether the new optimized GStreamer pipeline starts up fast enough.
2. Address any remaining custom UI overlay tasks in `player_screen.dart`.
