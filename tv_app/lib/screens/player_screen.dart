import 'dart:async';
import 'package:flutter/material.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/channel.dart';
import '../services/api_service.dart';

class PlayerScreen extends StatefulWidget {
  final Channel channel;
  final String streamUrl;

  const PlayerScreen({super.key, required this.channel, required this.streamUrl});

  @override
  State<PlayerScreen> createState() => _PlayerScreenState();
}

class _PlayerScreenState extends State<PlayerScreen> {
  late Player player;
  late VideoController controller;
  String _currentBitrate = 'Original';
  StreamSubscription<double>? _volumeSubscription;

  final List<String> _qualityOptions = [
    'Auto',
    'Original',
    '8M',
    '4M',
    '3M',
    '1.5M',
  ];

  @override
  void initState() {
    super.initState();
    _bootstrapPlayback();
  }

  Future<void> _bootstrapPlayback() async {
    await _initPlayer();

    // Start immediately with the direct stream.
    await player.open(Media(widget.streamUrl), play: true);
    await player.play();
  }

  Future<void> _initPlayer() async {
    player = Player();
    controller = VideoController(player);

    // Load saved volume
    final prefs = await SharedPreferences.getInstance();
    final savedVolume = prefs.getDouble('player_volume') ?? 100.0;
    await player.setVolume(savedVolume);

    // Listen to volume changes and save them
    _volumeSubscription = player.stream.volume.listen((volume) {
      prefs.setDouble('player_volume', volume);
    });
  }

  Future<void> _changeQuality(
    String bitrate, {
    bool releaseTunersFirst = true,
  }) async {
    if (bitrate == _currentBitrate && player.state.playing) return;

    setState(() {
      _currentBitrate = bitrate;
    });

    try {
      final api = Provider.of<ApiService>(context, listen: false);

      if (releaseTunersFirst && bitrate != 'Original') {
        // Free up any previous HDHomeRun tuners held by old FFmpeg sessions.
        await api.stopAllStreams();
        await Future.delayed(const Duration(milliseconds: 300));
      }
      
      String targetBitrate = bitrate;
      if (bitrate == 'Auto') {
        // Run speed test
        final speedMbps = await api.runSpeedTest();
        debugPrint('Speed Test Result: ${speedMbps.toStringAsFixed(2)} Mbps');

        if (speedMbps > 12.0) {
          targetBitrate = '8M';
        } else if (speedMbps > 6.0) {
          targetBitrate = '4M';
        } else if (speedMbps > 4.0) {
          targetBitrate = '3M';
        } else {
          targetBitrate = '1.5M';
        }
        debugPrint('Auto Selected Bitrate: $targetBitrate');
      }

      String newUrl;
      if (targetBitrate == 'Original') {
        newUrl = widget.channel.streamUrl;
      } else {
        newUrl = await api.getStreamUrl(widget.channel.streamUrl, bitrate: targetBitrate);
      }

      await player.open(Media(newUrl), play: true);
      await player.play();
    } catch (e) {
      if (mounted) {
        await player.open(Media(widget.streamUrl), play: true);
        await player.play();
      }
    }
  }

  @override
  void dispose() {
    _volumeSubscription?.cancel();
    player.dispose();
    try {
      final api = Provider.of<ApiService>(context, listen: false);
      api.stopAllStreams();
    } catch (_) {}
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
              (event.logicalKey == LogicalKeyboardKey.escape || event.logicalKey == LogicalKeyboardKey.browserBack)) {
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
                child: Video(controller: controller),
              ),
            ),
            // Channel Info Overlay
            Positioned(
              top: 40,
              left: 40,
              child: Container(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
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
                      style: const TextStyle(
                        color: Colors.grey,
                        fontSize: 16,
                      ),
                    ),
                  ],
                ),
              ),
            ),
            // Quality Selector Overlay (Top Right)
            Positioned(
              top: 40,
              right: 40,
              child: PopupMenuButton<String>(
                icon: const Icon(Icons.settings, color: Colors.white, size: 32),
                color: Colors.black87,
                tooltip: 'Quality',
                onSelected: _changeQuality,
                itemBuilder: (context) => _qualityOptions.map((value) {
                  String text = value == 'Original' ? 'Original (Direct)' : '${value.replaceAll('M', '')} Mbps';
                  if (value == 'Auto') {
                    text = 'Auto';
                  }
                  return PopupMenuItem(
                    value: value,
                    child: Text(
                      text,
                      style: TextStyle(
                        color: _currentBitrate == value ? Colors.blue : Colors.white,
                      ),
                    ),
                  );
                }).toList(),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
