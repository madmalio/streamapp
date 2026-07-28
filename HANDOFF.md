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
- **Frontend Performance Optimizations:** 
  - Passed EPG map directly from `GuideScreen` to `PlayerScreen` to eliminate massive 2.5MB redundant API calls on channel playback.
  - Implemented `AutomaticKeepAliveClientMixin` for the `TabBar` views to persist guide renderings in memory.
  - Used an `IndexedStack` for the sidebar's Channels/Guide toggle, instantly switching between them without re-rendering.
- **Pluto TV & Native Playback Testing:**
  - Confirmed direct `jmp2.uk/plu-...m3u8` playback can fail in-app due to redirect/relative-URL handling and subtitle-track edge cases.
  - Added backend playlist proxy endpoint `GET /api/proxy/m3u8?url=...&best=1` to normalize master playlists before Flutter opens them:
    - Strips subtitle track declarations (`#EXT-X-MEDIA:TYPE=SUBTITLES`).
    - Rewrites relative variant URLs to absolute URLs using the final redirected base URL.
    - Supports `best=1` to retain only the highest-bandwidth variant.
  - Updated Flutter Pluto route in `player_screen.dart` to always resolve Pluto-like URLs through `/api/proxy/m3u8?...&best=1&t=<cache-bust>`.
  - Applied Pluto-specific native player properties:
    - Browser-like headers (`User-Agent`, `Referer`, `Origin`).
    - `hwdec=no` to avoid D3D11VA crashes during ad-driven resolution changes.
    - `sid=no` and `sub-auto=no` to prevent subtitle deadlocks.
    - `hls-bitrate=max` and tuned cache/readahead profile.
  - Added automatic Pluto stall detection/recovery (buffering + no-progress monitor), cooldowns, and single-flight generation guards to prevent surf/recovery race crashes.
  - Removed custom on-screen "Re-syncing stream..." badge per user request; native media_kit spinner is now the only recovery UI.

## Current Problem (End of Session)
- **Status**: Pluto direct playback in-app is now working decently through the proxy + stabilization path.
- **Known Tradeoffs**: Quality can still appear below VLC in some scenes, occasional behind-live drift can appear, and some ad transitions may still require automatic hard recovery.

## Next Steps (Recommended)
1. Run a short Pluto soak test (channel changes + ad breaks) and track freeze count/hour, recovery time, and crash count.
2. Keep current direct-play baseline; tune only one Pluto variable at a time if needed.
3. Continue the user's higher-priority GStreamer practical evaluation while preserving FFmpeg fallback.
