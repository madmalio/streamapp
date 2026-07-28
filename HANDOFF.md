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
  - Exhaustively tested `media_kit`'s ability to play Pluto TV HLS streams natively (Direct Route, bypassing backend).
  - Attempted to bypass 403 Forbidden errors by hardcoding Firefox `User-Agent` into the internal `libmpv` demuxer via the `http-header-fields` property.
  - Attempted to bypass Windows D3D11VA hardware decoder crashes on SSAI ad-resolution changes by forcing software decoding (`hwdec=no`).
  - Attempted to bypass `libmpv` HLS timeline deadlocks by writing a Go backend proxy (`/api/proxy/m3u8`) that fetched and dynamically scrubbed all `subtitle.vtt` references from the M3U8 master playlist before passing it to Flutter.
  - **Result**: Even with all workarounds applied natively, `media_kit` still spins endlessly on the Pluto TV streams.
- **UI Updates**: Added a Closed Caption (CC) toggle button directly into the `media_kit_video` `MaterialDesktopVideoControlsThemeData` bottom control bar.

## Current Problem (End of Session)
- **Pluto TV Direct Playback Fails**: `media_kit` has been proven fundamentally incapable of natively playing the chaotic, ad-stitched Pluto TV M3U8 links directly.
- **The Fork in the Road**: The user must make a final architectural decision on how to handle external IPTV streams:
  1. **Backend Transcoding**: Use the backend to transcode the Pluto TV stream (forcing a locked resolution and single video track). This prevents `media_kit` from crashing, but burns CPU and compresses quality.
  2. **VLC Engine Swap**: Completely replace `media_kit` with `flutter_vlc_player` for IPTV streams (since VLC natively handles Pluto's ads and subtitles perfectly). This requires rebuilding all player UI controls from scratch.

## Next Steps (Recommended)
1. Ask the user which path they want to take (Transcode vs VLC).
2. If they choose Backend Transcoding, ensure the backend extracts the 1080p variant using the newly written `getBestHlsVariant` parser, and optimize the Intel VAAPI transcoder for maximum bitrate.
3. If they choose VLC, import `flutter_vlc_player` and begin drafting the custom overlay UI.
