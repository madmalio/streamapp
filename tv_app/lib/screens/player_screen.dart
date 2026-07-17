import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';
import 'package:http/http.dart' as http;
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
  
  RTCVideoRenderer? _webrtcRenderer;
  RTCPeerConnection? _peerConnection;
  
  bool _isChangingQuality = false;

  String _currentBitrate = 'Original';
  String? _activeHlsSessionId;
  int _switchToken = 0;
  StreamSubscription<double>? _volumeSubscription;
  String _currentEngine = 'ffmpeg';

  final List<String> _qualityOptions = [
    'Auto',
    'Original',
    'Original HLS',
    'WebRTC',
    '8M',
    '4M',
    '3M',
    '1.5M',
  ];

  late ApiService _api;

  @override
  void initState() {
    super.initState();
    _api = context.read<ApiService>();
    final settings = context.read<AppSettings>();
    _currentEngine = settings.streamingEngine;
    _bootstrapPlayback();
  }

  Future<void> _bootstrapPlayback() async {
    await _initPlayer();
    if (!mounted) return;

    if (widget.streamUrl.contains('.m3u8') || widget.streamUrl.startsWith('srt://')) {
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

    // Media Kit Engine Setup
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
    if (_isChangingQuality) return;

    _isChangingQuality = true;
    final requestToken = ++_switchToken;
    setState(() => _currentBitrate = bitrate);

    try {
      final oldSessionId = _activeHlsSessionId;

      // Aggressively stop the player to instantly sever any direct TCP connections to the HDHomeRun 
      // (crucial if we are currently playing the 'Original' direct stream).
      await player?.stop();

      // Aggressively stop the old HLS stream before starting the new one.
      // This is absolutely critical for HDHomeRun tuners which cannot pool connections.
      if (oldSessionId != null) {
        _activeHlsSessionId = null;
        await _api.stopStream(oldSessionId);
      }
      
      // Wait a grace period to ensure the HDHomeRun has fully cleared the tuner state internally.
      // Embedded devices often take 1-2 seconds to register a closed TCP socket and free the physical tuner.
      await Future<void>.delayed(const Duration(milliseconds: 2000));

      String targetBitrate = bitrate;
      if (bitrate == 'Auto') {
        targetBitrate = await _api.getRecommendedBitrate(
          forceRefresh: refreshAutoRecommendation,
          fallbackOnUnknown: fallbackOnUnknownAuto,
        );
        if (targetBitrate == _currentBitrate) return;
        if (!mounted || requestToken != _switchToken) return;
        setState(() => _currentBitrate = targetBitrate);
      }
      
      // Tear down previous WebRTC connection if any
      await _stopWebRTC();

      if (targetBitrate == 'Original') {
        await _openAndForcePlay(widget.streamUrl);
        return;
      }

      if (targetBitrate == 'WebRTC') {
        try {
          final session = await _api.startWebRTCStream(widget.channel.streamUrl);
          if (!mounted || requestToken != _switchToken) return;
          await _startWebRTC(session.url);
          // Set the active session ID so that dispose() stops the WebRTC transcoder!
          _activeHlsSessionId = session.sessionId;
        } catch (_) {
          // Fallback if WebRTC fails
          await _openAndForcePlay(widget.streamUrl);
        }
        return;
      }

      HlsStreamSession session;
      if (targetBitrate == 'Original HLS') {
        session = await _api.startHlsStream(
          widget.channel.streamUrl,
          bitrate: 'Original',
          fast: preferFastSwitch,
          transmux: true,
          engine: _currentEngine,
        );
      } else {
        try {
          session = await _api.startHlsStream(
            widget.channel.streamUrl,
            bitrate: targetBitrate,
            fast: preferFastSwitch,
            engine: _currentEngine,
          );
        } catch (_) {
          // If the first attempt failed, the tuner might be stuck. Nuke everything and retry.
          await _api.stopAllStreams();
          await Future<void>.delayed(const Duration(milliseconds: 2000));
          session = await _api.startHlsStream(
            widget.channel.streamUrl,
            bitrate: targetBitrate,
            fast: false,
            engine: _currentEngine,
          );
        }
      }

      if (!mounted || requestToken != _switchToken) {
        unawaited(_api.stopStream(session.sessionId));
        return;
      }

      await _openAndForcePlay(session.url);
      _activeHlsSessionId = session.sessionId;
    } catch (e) {
      if (!mounted) return;
      // Fallback if everything failed (tuner exhausted or backend offline)
      if (!isPlaying) {
        await _openAndForcePlay(widget.streamUrl);
      }
    } finally {
      if (mounted) {
        _isChangingQuality = false;
      }
    }
  }

  Future<void> _startWebRTC(String whepUrl) async {
    // 1. Initialize renderer
    _webrtcRenderer = RTCVideoRenderer();
    await _webrtcRenderer!.initialize();

    // 2. Create PeerConnection
    _peerConnection = await createPeerConnection({
      'sdpSemantics': 'unified-plan',
    });

    // 3. Bind stream to renderer
    _peerConnection!.onTrack = (RTCTrackEvent event) {
      if (event.track.kind == 'video' && event.streams.isNotEmpty) {
        _webrtcRenderer!.srcObject = event.streams[0];
        setState(() {}); // trigger rebuild to show WebRTC view
      }
    };

    // 4. Add Transceivers for Receiving Audio & Video
    await _peerConnection!.addTransceiver(
      kind: RTCRtpMediaType.RTCRtpMediaTypeVideo,
      init: RTCRtpTransceiverInit(direction: TransceiverDirection.RecvOnly),
    );
    await _peerConnection!.addTransceiver(
      kind: RTCRtpMediaType.RTCRtpMediaTypeAudio,
      init: RTCRtpTransceiverInit(direction: TransceiverDirection.RecvOnly),
    );

    // 5. Create Offer
    final offer = await _peerConnection!.createOffer();
    await _peerConnection!.setLocalDescription(offer);

    // 6. Send WHEP POST request to MediaMTX with retry loop
    // It takes a second or two for GStreamer to initialize the VAAPI encoder
    // and push the SRT stream to MediaMTX. We must retry the WebRTC negotiation
    // if MediaMTX returns a 404/400 (no stream available).
    int retries = 10;
    http.Response? response;
    while (retries > 0) {
      response = await http.post(
        Uri.parse(whepUrl),
        headers: {'Content-Type': 'application/sdp'},
        body: offer.sdp,
      );

      if (response.statusCode == 201 || response.statusCode == 200) {
        break;
      }
      
      // If the stream isn't ready, wait 500ms and try again
      await Future<void>.delayed(const Duration(milliseconds: 500));
      retries--;
    }

    if (response == null || (response.statusCode != 201 && response.statusCode != 200)) {
      throw Exception('WHEP server rejected offer: ${response?.statusCode}');
    }

    // 7. Set Remote Description (Answer)
    await _peerConnection!.setRemoteDescription(
      RTCSessionDescription(response.body, 'answer'),
    );
  }

  Future<void> _stopWebRTC() async {
    if (_peerConnection != null) {
      await _peerConnection!.close();
      _peerConnection = null;
    }
    if (_webrtcRenderer != null) {
      _webrtcRenderer!.srcObject = null;
      await _webrtcRenderer!.dispose();
      _webrtcRenderer = null;
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
    _stopWebRTC();
    try {
      final sessionId = _activeHlsSessionId;
      if (sessionId != null) {
        unawaited(_api.stopStream(sessionId));
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
              child: _currentBitrate == 'WebRTC' && _webrtcRenderer != null
                  ? RTCVideoView(_webrtcRenderer!)
                  : MaterialDesktopVideoControlsTheme(
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
