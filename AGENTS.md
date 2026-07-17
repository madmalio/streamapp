# Project Rules

- Ensure that any future FFmpeg transcoding pipelines are optimized for dirty, packet-dropping live antenna feeds.
- Avoid using CPU-bound software deinterlacing (`yadif`) when decoding HDHomeRun MPEG2 streams, as this bottlenecks transcoding speed and causes severe buffering in the Flutter UI.
- Use explicit file paths, and when making changes to `player_screen.dart`, be mindful that the `media_kit_video` `MaterialDesktopVideoControlsTheme` absorbs touch events; UI overlays like dropdown menus should be placed outside the bottom control bar to remain functional.
- Current user priority: continue testing and evaluating **GStreamer** in practical app flows before deciding between FFmpeg and GStreamer.
- Keep suggested workflows simple and actionable. Avoid over-complicating test instructions.
- If tuner lock issues appear, first run backend cleanup (`/api/streams/stop_all`) and verify no orphan FFmpeg/GStreamer processes before deeper changes.
- Preserve the existing FFmpeg path as fallback, but do not force conversation direction away from GStreamer testing unless the user asks.
