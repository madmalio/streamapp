import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'dart:async';
import '../models/channel.dart';
import '../services/api_service.dart';
import 'player_screen.dart';
import 'settings_screen.dart';

class GuideScreen extends StatefulWidget {
  const GuideScreen({super.key});

  @override
  State<GuideScreen> createState() => _GuideScreenState();
}

class _GuideScreenState extends State<GuideScreen> {
  static const bool _prewarmEnabled = false;

  List<Channel> _channels = [];
  bool _isLoading = true;
  Timer? _prewarmTimer;
  String? _lastPrewarmedKey;
  String? _activePrewarmSessionId;
  int _prewarmToken = 0;
  DateTime? _lastPrewarmAt;

  @override
  void initState() {
    super.initState();
    _primeAutoRecommendation();
    if (!_prewarmEnabled) {
      _prewarmToken = 0;
    }
    _loadChannels();
  }

  Future<void> _primeAutoRecommendation() async {
    try {
      final api = Provider.of<ApiService>(context, listen: false);
      await api.primeAutoRecommendation();
    } catch (_) {
      // Keep startup resilient even if speed test fails.
    }
  }

  Future<void> _loadChannels() async {
    setState(() => _isLoading = true);
    try {
      final api = Provider.of<ApiService>(context, listen: false);
      final channels = await api.getChannels();
      setState(() {
        _channels = channels;
        _isLoading = false;
      });
    } catch (e) {
      setState(() => _isLoading = false);
      // Handled simply for now
    }
  }

  void _schedulePrewarm(Channel channel) {
    if (!_prewarmEnabled) {
      return;
    }

    _prewarmTimer?.cancel();
    final token = ++_prewarmToken;
    _prewarmTimer = Timer(const Duration(milliseconds: 2200), () async {
      if (!mounted) {
        return;
      }

      final now = DateTime.now();
      if (_lastPrewarmAt != null && now.difference(_lastPrewarmAt!) < const Duration(seconds: 4)) {
        return;
      }

      final api = Provider.of<ApiService>(context, listen: false);
      final recommended = await api.getRecommendedBitrate(forceRefresh: false, fallbackOnUnknown: true);
      final key = '${channel.streamUrl}|$recommended';
      if (_lastPrewarmedKey == key) {
        return;
      }

      // Free tuner from prior prewarm before requesting another channel.
      final previousSessionId = _activePrewarmSessionId;
      _activePrewarmSessionId = null;
      if (previousSessionId != null) {
        await api.stopStream(previousSessionId);
      }

      _lastPrewarmedKey = key;

      final session = await api.prewarmHlsStream(channel.streamUrl, bitrate: recommended);
      if (session == null) {
        return;
      }

      if (!mounted || token != _prewarmToken) {
        unawaited(api.stopStream(session.sessionId));
        return;
      }

      _activePrewarmSessionId = session.sessionId;
      _lastPrewarmAt = now;
    });
  }

  Future<void> _releaseActivePrewarm() async {
    final sessionId = _activePrewarmSessionId;
    _activePrewarmSessionId = null;
    if (sessionId == null) {
      return;
    }

    try {
      final api = Provider.of<ApiService>(context, listen: false);
      await api.stopStream(sessionId);
    } catch (_) {}
  }

  Future<void> _openChannel(Channel channel) async {
    _prewarmTimer?.cancel();
    _prewarmToken += 1;
    await _releaseActivePrewarm();
    if (!mounted) {
      return;
    }

    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => PlayerScreen(channel: channel, streamUrl: channel.streamUrl),
      ),
    );
  }

  @override
  void dispose() {
    _prewarmTimer?.cancel();
    unawaited(_releaseActivePrewarm());
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: Row(
        children: [
          // Sidebar
          Container(
            width: 80,
            color: const Color(0xFF1A1A1A),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                const Icon(Icons.tv, size: 32, color: Colors.white),
                const SizedBox(height: 40),
                const Icon(Icons.list, size: 32, color: Colors.blueAccent),
                const SizedBox(height: 40),
                IconButton(
                  icon: const Icon(Icons.settings, size: 32, color: Colors.white54),
                  tooltip: 'Settings',
                  onPressed: () async {
                    final changed = await Navigator.push<bool>(
                      context,
                      MaterialPageRoute(builder: (_) => const SettingsScreen()),
                    );
                    if (changed == true && mounted) {
                      await _loadChannels();
                    }
                  },
                ),
              ],
            ),
          ),
          // Main Content
          Expanded(
            child: _isLoading
                ? const Center(child: CircularProgressIndicator())
                : Padding(
                    padding: const EdgeInsets.all(40.0),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        const Text(
                          'Live Guide',
                          style: TextStyle(
                            fontSize: 36,
                            fontWeight: FontWeight.bold,
                            color: Colors.white,
                          ),
                        ),
                        const SizedBox(height: 30),
                        Expanded(
                          child: GridView.builder(
                            gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
                              crossAxisCount: 3,
                              childAspectRatio: 2.5,
                              crossAxisSpacing: 20,
                              mainAxisSpacing: 20,
                            ),
                            itemCount: _channels.length,
                            itemBuilder: (context, index) {
                              return ChannelCard(
                                channel: _channels[index],
                                onFocus: _prewarmEnabled ? _schedulePrewarm : null,
                                onPlay: _openChannel,
                              );
                            },
                          ),
                        ),
                      ],
                    ),
                  ),
          ),
        ],
      ),
    );
  }
}

class ChannelCard extends StatefulWidget {
  final Channel channel;
  final ValueChanged<Channel>? onFocus;
  final ValueChanged<Channel>? onPlay;

  const ChannelCard({
    super.key,
    required this.channel,
    this.onFocus,
    this.onPlay,
  });

  @override
  State<ChannelCard> createState() => _ChannelCardState();
}

class _ChannelCardState extends State<ChannelCard> {
  bool _isFocused = false;
  bool _isHovered = false;

  @override
  Widget build(BuildContext context) {
    return Focus(
      onFocusChange: (hasFocus) {
        setState(() => _isFocused = hasFocus);
        if (hasFocus) {
          widget.onFocus?.call(widget.channel);
        }
      },
      onKeyEvent: (node, event) {
        if (event is KeyDownEvent && 
            (event.logicalKey == LogicalKeyboardKey.enter || event.logicalKey == LogicalKeyboardKey.select)) {
          _playChannel();
          return KeyEventResult.handled;
        }
        return KeyEventResult.ignored;
      },
      child: MouseRegion(
        onEnter: (_) {
          setState(() => _isHovered = true);
        },
        onExit: (_) => setState(() => _isHovered = false),
        child: GestureDetector(
          onTap: _playChannel,
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 200),
            decoration: BoxDecoration(
              color: (_isFocused || _isHovered) ? Colors.blueAccent : const Color(0xFF222222),
              borderRadius: BorderRadius.circular(16),
              border: Border.all(
                color: (_isFocused || _isHovered) ? Colors.white : Colors.transparent,
                width: 2,
              ),
              boxShadow: (_isFocused || _isHovered)
                  ? [BoxShadow(color: Colors.blueAccent.withOpacity(0.5), blurRadius: 20, spreadRadius: 5)]
                  : [],
            ),
            child: Center(
              child: Text(
                widget.channel.name,
                style: TextStyle(
                  fontSize: 24,
                  fontWeight: FontWeight.bold,
                  color: (_isFocused || _isHovered) ? Colors.white : Colors.white70,
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  void _playChannel() async {
    if (widget.onPlay != null) {
      widget.onPlay!(widget.channel);
      return;
    }

    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => PlayerScreen(channel: widget.channel, streamUrl: widget.channel.streamUrl),
      ),
    );
  }
}
