# StreamApp Handoff

## Current Architecture (Hybrid HLS)
- The user is building `streamapp`, a Flutter TV app (`tv_app`) and a Go backend (`backend`) for HDHomeRun live TV.
- We successfully refactored the streaming engine to a **Hybrid Architecture** leveraging MediaMTX to achieve zero-buffering / zero-disk-I/O:
  - **Go Backend:** Responsible for translating API calls from Flutter, managing the FFmpeg lifecycles, and instantly killing transcoding processes to instantly free tuners.
  - **FFmpeg:** Now pushes the stream *directly* into MediaMTX via RTSP instead of writing `.ts` chunks to SSD.
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
    - Rewrites media playlist URIs (`segments`, `#EXT-X-KEY URI=`, `#EXT-X-MAP URI=`) to absolute URLs.
    - Detects HLS by response content (`Content-Type`, `#EXTM3U`) and safely passes through non-HLS responses.
  - Updated Flutter resolver in `player_screen.dart` to proxy all external HLS URLs through `/api/proxy/m3u8?...&best=1&t=<cache-bust>`, including Plex `epg.provider.plex.tv/library/parts/...` endpoints that do not end with `.m3u8`.
  - Applied Pluto-specific native player properties:
    - Browser-like headers (`User-Agent`, `Referer`, `Origin`).
    - `hwdec=no` to avoid D3D11VA crashes during ad-driven resolution changes.
    - `sid=no` and `sub-auto=no` to prevent subtitle deadlocks.
    - `hls-bitrate=max` and tuned cache/readahead profile.
    - Hybrid scaler policy: stable playback window enables `scale=spline36` and `cscale=bilinear`; risky windows fall back to bilinear for stability.
  - Added automatic Pluto stall detection/recovery (buffering + no-progress monitor), cooldowns, and single-flight generation guards to prevent surf/recovery race crashes.
  - Added manual-switch stabilization (debounce, Pluto settle delay, temporary recovery suppression after channel switch) to reduce ad-transition channel-change crashes.
  - Removed custom on-screen "Re-syncing stream..." badge per user request; native media_kit spinner is now the only recovery UI.
  - Desktop fullscreen channel menu now opens via modal in fullscreen and keeps side-panel behavior in windowed mode.
  - Channel management screen now separates channels by tuner/source via tabs instead of a single long list.
  - **Virtual Tuners & Categorization Engine:**
    - Revamped Virtual Tuners (Wizard UI) allowing deduplication and category selection.
    - Added `SmartCategorize` regex engine to identify networks (e.g., NBC, ABC, CBS, MeTV) from channel names.
    - Implemented EPG-based category overrides: `syncEPGSource` now leverages rich display names from XMLTV to accurately categorize local affiliates that the raw HDHomeRun tuner sync initially marked as 'Other'.
  - **Volume Slider Reverted**: Explored persistent custom volume slider, but reverted back to default `media_kit_video` hover controls due to player widget tree rebuild conflicts. Will investigate in a future session.
  - **Tuner Editor Consolidation**: Merged channel management into a unified `tuner_editor.dart` accessible from the pencil icon on each tuner in Settings Tab 1. This editor handles:
    - Tuner-level settings (name + URL editing; URL read-only for virtual tuners)
    - Channel metadata editing (name, guide number, category)
    - Logo URL editing
    - Visibility toggle (green/red switch instead of confusing "delete")
    - Drag-to-reorder (virtual tuners only)
  - Removed redundant "Manage Channels & Logos" button from Tab 2 (Guide & Channels), making Tab 2 EPG-focused only.
  - Deleted obsolete files: `virtual_tuner_editor.dart` and `channel_management_screen.dart`.
  - **Fixed Favorites Preservation on Tuner Resync**: Modified `chanState` struct and `getExistingChannelState()` to include `is_favorite` field. Updated all sync functions (`syncM3U`, `syncXtream`, `syncHDHomeRun`) to preserve favorite status when channels are deleted and re-inserted during tuner sync.
  - **Fixed Guide Data for Virtual Channels**: Modified `syncEPGSource()` to exclude virtual channels (those with `source_channel_id != ''`) from EPG matching. After EPG sync completes, virtual channels now get their EPG data copied from source channels. Frontend EPG lookups now use `channel.id` instead of `sourceChannelId`.
  - **Added Gzip Support for EPG Sources**: Backend now detects and decompresses gzip-compressed XMLTV files (by `.gz` URL extension or `Content-Encoding: gzip` header) before parsing. Fixes sync failures for compressed EPG sources like Pluto TV.
  - **Removed DIY Tuner Wizard**: Removed `virtual_tuner_wizard.dart`, the "Build Custom Tuner (DIY)" button, and the `/virtual-tuners/generate` backend endpoint. Kept the simpler "Build from Favorites" workflow.
  - **Hide Favorites Category on Favorites Tuner**: Added `isFavoritesTuner` parameter to `_buildGroupedChannelRows()` and `_buildEpgGrid()` in `guide_screen.dart`. When viewing a tuner created from favorites (`p.createdFromFavorites == true`), the Favorites category grouping is skipped to eliminate redundancy.
  - **RTSP Camera Support**: Added full camera management system for RTSP security cameras:
    - **Backend**: New `cameras` table with id, name, rtsp_url, location, is_enabled, sort_order, created_at. CRUD handlers (`GetCameras`, `AddCamera`, `UpdateCamera`, `DeleteCamera`, `ReorderCameras`). RTSP URL validation (must start with `rtsp://` or `rtsps://).
    - **Frontend**: New `Camera` model, camera API methods in `api_service.dart`, new "Cameras" tab (Tab 3) in settings screen for camera management.
    - **Camera Viewing**: `CameraScreen` with adaptive grid layout (2x2 for ≤4 cameras, 3x3 for 5-9, 4 columns for more). `CameraTile` widget for individual camera feeds using direct RTSP playback via `media_kit` (no backend transcoding). `FullscreenCameraView` widget with audio controls, gradient overlays, and auto-hiding controls (3 second timeout).
    - **Connectivity Testing**: RTSP URL tests run client-side using Dart `Socket.connect`, allowing testing from the Flutter app's network location rather than the backend server.
    - **Navigation**: Added sidebar to camera screen matching guide screen layout. `GuideScreen` now accepts `initialTab` and `playLastChannel` parameters for cross-screen navigation.

## Current Problem (End of Session)
- **Status**: Full camera management and viewing system is operational. Tuner management is consolidated. Guide data and favorites are preserved correctly during resyncs.
- **Pending/Open**: 
  - Custom persistent volume slider in `player_screen.dart` reverted due to state conflicts; needs structural fix if hover-only volume control is to be removed.
  - Camera auto-reconnect not implemented (streams show error state if connection drops).
  - Camera grid doesn't pause streams when scrolling off-screen (performance consideration for many cameras).

## Next Steps (Recommended)
1. Run short soak tests across Pluto, Plex, and Tubi (channel changes during ad windows) and track crash count + recovery count.
2. Continue testing the Smart Categorization Engine with different HDHomeRun setups to ensure all local network variants (e.g. 'NBC 5') are caught by the regex patterns in `category.go`.
3. If returning to the volume slider, refactor `PlayerScreen` to prevent total widget tree rebuilds on volume state changes.
4. Consider implementing camera auto-reconnect for dropped RTSP streams.
5. Consider implementing visibility-based stream pausing in camera grid for performance with many cameras.
