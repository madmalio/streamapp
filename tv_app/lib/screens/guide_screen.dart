import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
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
  List<Channel> _channels = [];
  bool _isLoading = true;

  @override
  void initState() {
    super.initState();
    _loadChannels();
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
                              return ChannelCard(channel: _channels[index]);
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

  const ChannelCard({super.key, required this.channel});

  @override
  State<ChannelCard> createState() => _ChannelCardState();
}

class _ChannelCardState extends State<ChannelCard> {
  bool _isFocused = false;

  @override
  Widget build(BuildContext context) {
    return Focus(
      onFocusChange: (hasFocus) {
        setState(() => _isFocused = hasFocus);
      },
      onKeyEvent: (node, event) {
        if (event is KeyDownEvent && 
            (event.logicalKey == LogicalKeyboardKey.enter || event.logicalKey == LogicalKeyboardKey.select)) {
          _playChannel();
          return KeyEventResult.handled;
        }
        return KeyEventResult.ignored;
      },
      child: GestureDetector(
        onTap: _playChannel,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 200),
          decoration: BoxDecoration(
            color: _isFocused ? Colors.blueAccent : const Color(0xFF222222),
            borderRadius: BorderRadius.circular(16),
            border: Border.all(
              color: _isFocused ? Colors.white : Colors.transparent,
              width: 2,
            ),
            boxShadow: _isFocused
                ? [BoxShadow(color: Colors.blueAccent.withOpacity(0.5), blurRadius: 20, spreadRadius: 5)]
                : [],
          ),
          child: Center(
            child: Text(
              widget.channel.name,
              style: TextStyle(
                fontSize: 24,
                fontWeight: FontWeight.bold,
                color: _isFocused ? Colors.white : Colors.white70,
              ),
            ),
          ),
        ),
      ),
    );
  }

  void _playChannel() async {
    final navigator = Navigator.of(context);
    final messenger = ScaffoldMessenger.of(context);

    try {
      final api = Provider.of<ApiService>(context, listen: false);
      final rawUrl = await api.getStreamUrl(widget.channel.streamUrl);

      if (!mounted) {
        return;
      }

      navigator.push(
        MaterialPageRoute(
          builder: (_) => PlayerScreen(channel: widget.channel, streamUrl: rawUrl),
        ),
      );
    } catch (_) {
      if (!mounted) {
        return;
      }

      messenger.showSnackBar(
        const SnackBar(content: Text('Unable to start stream. Check Settings > Backend API URL.')),
      );
    }
  }
}
