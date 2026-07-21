# Project Rules

- Ensure that any future FFmpeg transcoding pipelines are optimized for dirty, packet-dropping live antenna feeds.
- Avoid using CPU-bound software deinterlacing (`yadif`) when decoding HDHomeRun MPEG2 streams, as this bottlenecks transcoding speed and causes severe buffering in the Flutter UI.
- Use explicit file paths, and when making changes to `player_screen.dart`, be mindful that the `media_kit_video` `MaterialDesktopVideoControlsTheme` absorbs touch events; UI overlays like dropdown menus should be placed outside the bottom control bar to remain functional.
- Current user priority: continue testing and evaluating **GStreamer** in practical app flows before deciding between FFmpeg and GStreamer.
- Keep suggested workflows simple and actionable. Avoid over-complicating test instructions.
- If tuner lock issues appear, first run backend cleanup (`/api/streams/stop_all`) and verify no orphan FFmpeg/GStreamer processes before deeper changes.
- Preserve the existing FFmpeg path as fallback, but do not force conversation direction away from GStreamer testing unless the user asks.
- **Deployment & Testing Workflow**: When providing code updates for the backend, ALWAYS provide the exact push and run commands using the user's specific credentials:
  - `scp backend/internal/handlers/handlers.go mark@192.168.4.143:/home/mark/streamapp/backend/internal/handlers/`
  - `go build -o streamapp-backend ./cmd/server`
  - `./streamapp-backend`
- **Go/SQLite Boolean Mapping**: When querying SQLite `INTEGER` columns that represent booleans (e.g. `is_hidden BOOLEAN DEFAULT 0`), do not use `sql.NullBool` or `sql.NullInt64` in `rows.Scan`. Use a generic `interface{}` and cast appropriately, as the `go-sqlite3` driver intercepts booleans and maps them to Go `bool`s unpredictably.
- **SQLite Transaction Locking**: When holding a write transaction (`tx.Begin()`), do not execute queries using the global `database.DB` connection, as it will silently fail with a `SQLITE_BUSY` lock error. Always execute queries on the transaction (`tx.Exec`) until it is committed.
- **Free IPTV Playback (Pluto TV)**: When passing external IPTV `.m3u8` links directly to `media_kit`, ensure you configure `http-header-fields` with a standard browser `User-Agent`. Empty or `libmpv` user agents are frequently blocked by these providers.
