import 'dart:async';
import 'package:flutter/material.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/channel.dart';
import '../services/api_service.dart';
import '../services/app_settings.dart';

class PlayerScreen extends StatefulWidget {
  final Channel channel;
  final String streamUrl;

  const PlayerScreen({
    super.key,
    required this.channel,
    required this.streamUrl,
  });

  @override
  State<PlayerScreen> createState() => _PlayerScreenState();
}

class _PlayerScreenState extends State<PlayerScreen> {
  Player? player;
  VideoController? controller;

  String _currentBitrate = 'Original';
  String? _activeHlsSessionId;
  int _switchToken = 0;
  StreamSubscription<double>? _volumeSubscription;
  String _currentEngine = 'ffmpeg';

  final List<String> _qualityOptions = [
    'Auto',
    'Original',
    'Original HLS',
    '8M',
    '4M',
    '3M',
    '1.5M',
  ];

  @override
  void initState() {
    super.initState();
    final settings = context.read<AppSettings>();
    _currentEngine = settings.streamingEngine;
    _bootstrapPlayback();
  }

  Future<void> _bootstrapPlayback() async {
    await _initPlayer();
    if (!mounted) return;

    if (widget.streamUrl.contains('.m3u8')) {
      setState(() => _currentBitrate = 'Original');
      await _openAndForcePlay(widget.streamUrl);
      return;
    }

    final settings = context.read<AppSettings>();
    final defaultQuality = settings.defaultQuality;

    await _changeQuality(
      defaultQuality,
      fallbackOnUnknownAuto: true,
      refreshAutoRecommendation: false,
      preferFastSwitch: true,
    );
  }

  Future<void> _openAndForcePlay(String url) async {
    if (player == null) return;

    await player!.open(Media(url), play: true);
    await player!.play();

    // Catch potential platform pauses directly after stream initialization shifts
    unawaited(
      Future<void>.delayed(const Duration(milliseconds: 350), () async {
        if (mounted && player != null && !player!.state.playing) {
          await player!.play();
        }
      }),
    );
    unawaited(
      Future<void>.delayed(const Duration(milliseconds: 900), () async {
        if (mounted && player != null && !player!.state.playing) {
          await player!.play();
        }
      }),
    );
  }

  Future<void> _initPlayer() async {
    final prefs = await SharedPreferences.getInstance();
    final savedVolume = prefs.getDouble('player_volume') ?? 100.0;

    player = Player();
    controller = VideoController(player!);

    await _configureNativeLowLatencyProfile();
    await player!.setVolume(savedVolume);

    _volumeSubscription = player!.stream.volume.listen((volume) {
      prefs.setDouble('player_volume', volume);
    });
  }

  Future<void> _configureNativeLowLatencyProfile() async {
    try {
      if (player?.platform is! NativePlayer) return;
      final nativePlayer = player!.platform as NativePlayer;

      // Force libmpv down into a live real-time state mirroring standalone VLC configurations
      await nativePlayer.setProperty('profile', 'low-latency');
      await nativePlayer.setProperty('cache', 'no');
      await nativePlayer.setProperty('video-sync', 'audio');
      await nativePlayer.setProperty('untimed', 'yes');
      await nativePlayer.setProperty('hwdec', 'auto');
      await nativePlayer.setProperty('network-timeout', '5');
    } catch (e) {
      debugPrint('Low-latency profile parameters not applied: $e');
    }
  }

  Future<void> _changeQuality(
    String bitrate, {
    bool fallbackOnUnknownAuto = true,
    bool refreshAutoRecommendation = false,
    bool preferFastSwitch = false,
  }) async {
    final isPlaying = player?.state.playing ?? false;
    if (bitrate == _currentBitrate && isPlaying) return;

    final requestToken = ++_switchToken;
    setState(() => _currentBitrate = bitrate);

    String? pendingSessionId;

    try {
      final api = Provider.of<ApiService>(context, listen: false);
      final oldSessionId = _activeHlsSessionId;

      if (oldSessionId != null) {
        _activeHlsSessionId = null;
        await api.stopStream(oldSessionId);
        await Future<void>.delayed(const Duration(milliseconds: 350));
      }

      String targetBitrate = bitrate;
      if (bitrate == 'Auto') {
        targetBitrate = await api.getRecommendedBitrate(
          forceRefresh: refreshAutoRecommendation,
          fallbackOnUnknown: fallbackOnUnknownAuto,
        );
        if (targetBitrate == _currentBitrate) return;
        if (!mounted || requestToken != _switchToken) return;
        setState(() => _currentBitrate = targetBitrate);
      }

      if (targetBitrate == 'Original') {
        await _openAndForcePlay(widget.streamUrl);
        final oldSessionId = _activeHlsSessionId;
        _activeHlsSessionId = null;
        if (oldSessionId != null) {
          unawaited(api.stopStream(oldSessionId));
        }
        return;
      }

      if (targetBitrate == 'Original HLS') {
        final session = await api.startHlsStream(
          widget.channel.streamUrl,
          bitrate: 'Original',
          fast: preferFastSwitch,
          transmux: true,
          engine: _currentEngine,
        );
        pendingSessionId = session.sessionId;
        if (!mounted || requestToken != _switchToken) {
          unawaited(api.stopStream(session.sessionId));
          return;
        }

        final previousSessionId = _activeHlsSessionId;
        await _openAndForcePlay(session.url);
        _activeHlsSessionId = session.sessionId;
        pendingSessionId = null;

        if (previousSessionId != null &&
            previousSessionId != session.sessionId) {
          unawaited(api.stopStream(previousSessionId));
        }
        return;
      }

      HlsStreamSession session;
      try {
        session = await api.startHlsStream(
          widget.channel.streamUrl,
          bitrate: targetBitrate,
          fast: preferFastSwitch,
          engine: _currentEngine,
        );
      } catch (_) {
        try {
          session = await api.startHlsStream(
            widget.channel.streamUrl,
            bitrate: targetBitrate,
            fast: false,
            engine: _currentEngine,
          );
        } catch (_) {
          await api.stopAllStreams();
          await Future<void>.delayed(const Duration(milliseconds: 350));
          session = await api.startHlsStream(
            widget.channel.streamUrl,
            bitrate: targetBitrate,
            fast: false,
            engine: _currentEngine,
          );
        }
      }

      pendingSessionId = session.sessionId;
      if (!mounted || requestToken != _switchToken) {
        unawaited(api.stopStream(session.sessionId));
        return;
      }

      final previousSessionId = _activeHlsSessionId;
      await _openAndForcePlay(session.url);
      _activeHlsSessionId = session.sessionId;
      pendingSessionId = null;

      if (previousSessionId != null && previousSessionId != session.sessionId) {
        unawaited(api.stopStream(previousSessionId));
      }
    } catch (e) {
      if (!mounted) return;
      try {
        final api = Provider.of<ApiService>(context, listen: false);
        if (pendingSessionId != null) {
          unawaited(api.stopStream(pendingSessionId));
        }
      } catch (_) {}

      final isPlaying = player?.state.playing ?? false;
      if (mounted && !isPlaying) {
        await _openAndForcePlay(widget.streamUrl);
      }
    }
  }

  Future<void> _toggleEngine() async {
    final newEngine = _currentEngine == 'ffmpeg' ? 'gstreamer' : 'ffmpeg';
    setState(() => _currentEngine = newEngine);

    if (_currentBitrate == 'Original' && widget.streamUrl.contains('.m3u8')) {
      return;
    }

    await _changeQuality(
      _currentBitrate,
      fallbackOnUnknownAuto: true,
      refreshAutoRecommendation: false,
      preferFastSwitch: true,
    );
  }

  Future<void> _onQualitySelected(String bitrate) async {
    await _changeQuality(bitrate, preferFastSwitch: true);
  }

  @override
  void dispose() {
    _volumeSubscription?.cancel();
    try {
      final api = Provider.of<ApiService>(context, listen: false);
      final sessionId = _activeHlsSessionId;
      if (sessionId != null) {
        unawaited(api.stopStream(sessionId));
      }
    } catch (_) {}

    player?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      body: Focus(
        autofocus: true,
        onKeyEvent: (node, event) {
          if (event is KeyDownEvent &&
              (event.logicalKey == LogicalKeyboardKey.escape ||
                  event.logicalKey == LogicalKeyboardKey.browserBack)) {
            Navigator.pop(context);
            return KeyEventResult.handled;
          }
          return KeyEventResult.ignored;
        },
        child: Stack(
          children: [
            Center(
              child: MaterialDesktopVideoControlsTheme(
                normal: MaterialDesktopVideoControlsThemeData(
                  bottomButtonBar: [
                    const MaterialPlayOrPauseButton(),
                    const MaterialPositionIndicator(),
                    const Spacer(),
                    const MaterialDesktopVolumeButton(),
                    const MaterialDesktopFullscreenButton(),
                  ],
                ),
                fullscreen: MaterialDesktopVideoControlsThemeData(
                  bottomButtonBar: [
                    const MaterialPlayOrPauseButton(),
                    const MaterialPositionIndicator(),
                    const Spacer(),
                    const MaterialDesktopVolumeButton(),
                    const MaterialDesktopFullscreenButton(),
                  ],
                ),
                child: controller != null
                    ? Video(controller: controller!)
                    : const SizedBox(),
              ),
            ),
            // Channel Info Overlay
            Positioned(
              top: 40,
              left: 40,
              child: Container(
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 8,
                ),
                decoration: BoxDecoration(
                  color: Colors.black.withOpacity(0.7),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      widget.channel.name,
                      style: const TextStyle(
                        color: Colors.white,
                        fontSize: 24,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                    Text(
                      'Quality: $_currentBitrate',
                      style: const TextStyle(color: Colors.grey, fontSize: 16),
                    ),
                  ],
                ),
              ),
            ),
            // Quality & Engine Selector Overlay (Top Right)
            Positioned(
              top: 40,
              right: 40,
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextButton.icon(
                    onPressed: _toggleEngine,
                    icon: Icon(
                      _currentEngine == 'gstreamer'
                          ? Icons.science
                          : Icons.videocam,
                      color: Colors.white,
                      size: 20,
                    ),
                    label: Text(
                      _currentEngine == 'gstreamer' ? 'GStreamer' : 'FFmpeg',
                      style: const TextStyle(
                        color: Colors.white,
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    style: TextButton.styleFrom(
                      backgroundColor: Colors.black54,
                      padding: const EdgeInsets.symmetric(
                        horizontal: 16,
                        vertical: 12,
                      ),
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(20),
                        side: const BorderSide(color: Colors.white30),
                      ),
                    ),
                  ),
                  const SizedBox(width: 16),
                  PopupMenuButton<String>(
                    icon: const Icon(
                      Icons.settings,
                      color: Colors.white,
                      size: 32,
                    ),
                    color: Colors.black87,
                    tooltip: 'Quality',
                    onSelected: _onQualitySelected,
                    itemBuilder: (context) => _qualityOptions.map((value) {
                      String text = value == 'Original'
                          ? 'Original (Direct)'
                          : '${value.replaceAll('M', '')} Mbps';
                      if (value == 'Auto') {
                        text = 'Auto';
                      } else if (value == 'Original HLS') {
                        text = 'Original (HLS)';
                      }
                      return PopupMenuItem(
                        value: value,
                        child: Text(
                          text,
                          style: TextStyle(
                            color: _currentBitrate == value
                                ? Colors.blue
                                : Colors.white,
                          ),
                        ),
                      );
                    }).toList(),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
