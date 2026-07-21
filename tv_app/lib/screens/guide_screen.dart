import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'dart:async';
import 'dart:ui';
import '../models/channel.dart';
import '../models/epg_program.dart';
import '../services/api_service.dart';
import '../services/app_settings.dart';
import 'player_screen.dart';
import 'settings_screen.dart';

class GuideScreen extends StatefulWidget {
  const GuideScreen({super.key});

  @override
  State<GuideScreen> createState() => _GuideScreenState();
}

class _GuideScreenState extends State<GuideScreen> {
  static const bool _prewarmEnabled = false;
  static const String _gstTestUrl = 'http://192.168.4.143:8090/stream.m3u8';

  List<Channel> _channels = [];
  Map<String, ChannelEPG> _epgData = {};
  Channel? _focusedChannel;
  EPGProgram? _focusedProgram;
  bool _isLoading = true;
  int _currentTabIndex = 0; // 0 = Channels, 1 = Guide
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
      Map<String, ChannelEPG> epg = {};
      try {
        epg = await api.getLiveEpg();
      } catch (_) {
        // EPG might not be synced yet
      }

      // Filter out hidden channels and channels without a name
      var filteredChannels = channels.where((c) => c.name.trim().isNotEmpty && !c.isHidden).toList();

      setState(() {
        _channels = filteredChannels;
        _epgData = epg;
        if (_channels.isNotEmpty && _focusedChannel == null) {
          _focusedChannel = _channels.first;
          _focusedProgram = _epgData[_channels.first.id.toLowerCase()]?.currentProgram;
        }
        _isLoading = false;
      });
    } catch (e) {
      setState(() => _isLoading = false);
      // Handled simply for now
    }
  }

  void _onChannelFocus(Channel channel, {EPGProgram? program}) {
    if (_focusedChannel?.id != channel.id || _focusedProgram?.id != program?.id) {
      setState(() {
        _focusedChannel = channel;
        _focusedProgram = program;
      });
    }
    if (_prewarmEnabled) {
      _schedulePrewarm(channel);
    }
  }

  void _schedulePrewarm(Channel channel) {
    if (!_prewarmEnabled) return;

    _prewarmTimer?.cancel();
    final token = ++_prewarmToken;
    _prewarmTimer = Timer(const Duration(milliseconds: 2200), () async {
      if (!mounted) return;

      final now = DateTime.now();
      if (_lastPrewarmAt != null && now.difference(_lastPrewarmAt!) < const Duration(seconds: 4)) {
        return;
      }

      final api = Provider.of<ApiService>(context, listen: false);
      final recommended = await api.getRecommendedBitrate(forceRefresh: false, fallbackOnUnknown: true);
      final key = '${channel.streamUrl}|$recommended';
      if (_lastPrewarmedKey == key) return;

      // Free tuner from prior prewarm before requesting another channel.
      final previousSessionId = _activePrewarmSessionId;
      _activePrewarmSessionId = null;
      if (previousSessionId != null) {
        await api.stopStream(previousSessionId);
      }

      _lastPrewarmedKey = key;

      final session = await api.prewarmHlsStream(channel.streamUrl, bitrate: recommended);
      if (session == null) return;

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
    if (sessionId == null) return;

    try {
      final api = Provider.of<ApiService>(context, listen: false);
      await api.stopStream(sessionId);
    } catch (_) {}
  }

  void _showRecordModal(EPGProgram program) {
    showDialog(
      context: context,
      builder: (context) {
        return AlertDialog(
          backgroundColor: const Color(0xFF1E1E1E),
          title: const Text('Setup Recording', style: TextStyle(color: Colors.white)),
          content: Text(
            'Would you like to record ${program.title}?\n\nThis feature is coming soon!',
            style: const TextStyle(color: Colors.white70),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('Cancel', style: TextStyle(color: Colors.white54)),
            ),
            ElevatedButton(
              onPressed: () => Navigator.pop(context),
              style: ElevatedButton.styleFrom(backgroundColor: Colors.blueAccent),
              child: const Text('Record', style: TextStyle(color: Colors.white)),
            ),
          ],
        );
      },
    );
  }

  Future<void> _openChannel(Channel channel) async {
    // If we are opening the currently prewarmed channel, do NOT kill it!
    // PlayerScreen will adopt the exact same HLS Session ID from the backend.
    _prewarmTimer?.cancel();
    
    if (!mounted) return;
    
    context.read<AppSettings>().setLastChannelId(channel.id);

    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => PlayerScreen(
          initialChannel: channel, 
          initialStreamUrl: channel.streamUrl,
          channels: _channels,
        ),
      ),
    ).then((_) {
      // Clean up when returning from the player
      _prewarmToken += 1;
      _activePrewarmSessionId = null;
    });
  }

  void _openGstTestStream() {
    final testChannel = Channel(
      id: 'gst-test',
      playlistId: '',
      groupId: '',
      name: 'GStreamer Test',
      streamUrl: _gstTestUrl,
      logoUrl: '',
      channelNumber: 0,
      guideNumber: '',
      isFavorite: false,
    );

    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => PlayerScreen(initialChannel: testChannel, initialStreamUrl: _gstTestUrl, channels: _channels),
      ),
    );
  }

  Future<void> _openSrtTestStream() async {
    _prewarmTimer?.cancel();
    await _releaseActivePrewarm();
    if (!mounted) return;

    if (_channels.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('No channels loaded yet!')));
      return;
    }
    
    Channel targetChannel = _channels.first;
    for (var ch in _channels) {
      if (ch.streamUrl.contains('v7.1')) {
        targetChannel = ch;
        break;
      }
    }

    try {
      final api = Provider.of<ApiService>(context, listen: false);
      final srtUrl = await api.startSrtStream(targetChannel.streamUrl);
      
      final testChannel = Channel(
        id: 'test-srt',
        playlistId: '',
        groupId: '',
        name: 'SRT Test: ${targetChannel.name}',
        streamUrl: srtUrl,
        logoUrl: '',
        channelNumber: 0,
        guideNumber: '',
        isFavorite: false,
      );

      if (!mounted) return;
      Navigator.of(context).push(
        MaterialPageRoute(
            builder: (_) => PlayerScreen(initialChannel: testChannel, initialStreamUrl: srtUrl, channels: _channels),
        ),
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('SRT failed: $e')));
      }
    }
  }

  @override
  void dispose() {
    _prewarmTimer?.cancel();
    unawaited(_releaseActivePrewarm());
    super.dispose();
  }

  Widget _buildFeaturedHero(Channel channel) {
    return AnimatedSwitcher(
      duration: const Duration(milliseconds: 600),
      switchInCurve: Curves.easeOutCubic,
      switchOutCurve: Curves.easeInCubic,
      child: Container(
        key: ValueKey('${channel.id}_${_focusedProgram?.id}'),
        height: 360, // Increased height to prevent overflow
        width: double.infinity,
        decoration: BoxDecoration(
          color: const Color(0xFF151515),
          image: DecorationImage(
            image: NetworkImage((_focusedProgram?.posterUrl != null && _focusedProgram!.posterUrl.isNotEmpty)
                ? _focusedProgram!.posterUrl
                : 'https://images.unsplash.com/photo-1616469829581-73993eb86b02?q=80&w=2070'),
            fit: BoxFit.cover,
            colorFilter: ColorFilter.mode(Colors.black.withOpacity(0.4), BlendMode.darken),
          ),
          boxShadow: [
            BoxShadow(
              color: Colors.black.withOpacity(0.6),
              blurRadius: 40,
              offset: const Offset(0, 15),
            )
          ],
        ),
        child: ClipRRect(
          borderRadius: BorderRadius.zero,
          child: BackdropFilter(
            filter: ImageFilter.blur(sigmaX: 5, sigmaY: 5),
            child: Container(
              padding: const EdgeInsets.all(40),
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  colors: [
                    const Color(0xFF0F0F0F).withOpacity(0.9), 
                    Colors.transparent,
                  ],
                  begin: Alignment.bottomLeft,
                  end: Alignment.topRight,
                ),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.center,
                    children: [
                      if (channel.logoUrl.isNotEmpty) ...[
                        ClipRRect(
                          borderRadius: BorderRadius.circular(4),
                          child: Image.network(
                            channel.logoUrl,
                            height: 32,
                            fit: BoxFit.contain,
                            errorBuilder: (c, e, s) => const SizedBox(),
                          ),
                        ),
                        const SizedBox(width: 16),
                      ],
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
                        decoration: BoxDecoration(
                          color: Colors.redAccent.withOpacity(0.8),
                          borderRadius: BorderRadius.circular(8),
                        ),
                        child: const Text(
                          'LIVE NOW',
                          style: TextStyle(color: Colors.white, fontWeight: FontWeight.bold, fontSize: 14),
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 10),
                  Text(
                    _focusedProgram?.title ?? channel.name,
                    style: const TextStyle(
                      fontSize: 48,
                      fontWeight: FontWeight.w900,
                      color: Colors.white,
                      letterSpacing: -0.5,
                      shadows: [Shadow(color: Colors.black54, blurRadius: 10, offset: Offset(0, 4))],
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 8),
                  Text(
                    _focusedProgram?.description ?? 'HDHomeRun Network Broadcast',
                    style: const TextStyle(
                      fontSize: 18,
                      color: Colors.white70,
                      fontWeight: FontWeight.w500,
                    ),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 15),
                  Row(
                    children: [
                      ElevatedButton.icon(
                        onPressed: () => _openChannel(channel),
                        icon: const Icon(Icons.play_arrow, color: Colors.black, size: 24),
                        label: const Text('Play Stream', style: TextStyle(color: Colors.black, fontSize: 16, fontWeight: FontWeight.bold)),
                        style: ElevatedButton.styleFrom(
                          backgroundColor: Colors.white,
                          padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 14),
                          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(30)),
                          elevation: 10,
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildChannelRow(String title, List<Channel> rowChannels) {
    if (rowChannels.isEmpty) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 10, bottom: 10),
          child: Text(
            title,
            style: const TextStyle(
              fontSize: 22,
              fontWeight: FontWeight.w800,
              color: Colors.white,
              letterSpacing: 0.5,
            ),
          ),
        ),
        SizedBox(
          height: 130, // Compact height like Google TV
          child: ListView.builder(
            clipBehavior: Clip.none,
            scrollDirection: Axis.horizontal,
            itemCount: rowChannels.length,
            padding: const EdgeInsets.symmetric(horizontal: 10),
            itemBuilder: (context, index) {
              final chan = rowChannels[index];
              return Padding(
                padding: const EdgeInsets.only(right: 20, top: 5, bottom: 10),
                child: SizedBox(
                  width: 200, // Compact width to fit many channels on screen
                  child: ChannelCard(
                    channel: chan,
                    onFocus: (c) => _onChannelFocus(c, program: _epgData[c.id.toLowerCase()]?.currentProgram),
                    onPlay: _openChannel,
                  ),
                ),
              );
            },
          ),
        ),
        const SizedBox(height: 10),
      ],
    );
  }

  String _formatTimeGrid(DateTime time) {
    final hour = time.hour > 12 ? time.hour - 12 : (time.hour == 0 ? 12 : time.hour);
    final min = time.minute.toString().padLeft(2, '0');
    final ampm = time.hour >= 12 ? 'PM' : 'AM';
    return '$hour:$min $ampm';
  }

  Widget _buildEpgGrid(String title, List<Channel> gridChannels) {
    if (gridChannels.isEmpty) return const SizedBox.shrink();

    // Determine the timeline bounds based on now.
    final now = DateTime.now();
    // Align to the previous 30-minute mark
    final startOfTimeline = DateTime(now.year, now.month, now.day, now.hour, now.minute < 30 ? 0 : 30);
    // Show 4 hours
    final endOfTimeline = startOfTimeline.add(const Duration(hours: 4));
    
    // We need a pixel per minute scale. E.g., 10 pixels per minute. 30 mins = 300px.
    const double pixelsPerMinute = 10.0;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 10, bottom: 10),
          child: Text(
            title,
            style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w800, color: Colors.white, letterSpacing: 0.5),
          ),
        ),
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Pinned Left Column (Channel Logos)
            SizedBox(
              width: 140,
              child: Column(
                children: [
                  const SizedBox(height: 30), // Match the timeline header height
                  ...gridChannels.map((chan) {
                    return Container(
                      height: 90,
                      margin: const EdgeInsets.only(bottom: 12, left: 10, right: 10),
                      decoration: BoxDecoration(
                        color: const Color(0xFF1A1A1A),
                        borderRadius: BorderRadius.zero,
                      ),
                      child: Column(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          chan.logoUrl.isNotEmpty
                              ? Image.network(
                                  chan.logoUrl,
                                  width: 80,
                                  height: 40,
                                  fit: BoxFit.contain,
                                  errorBuilder: (context, error, stackTrace) => SizedBox(
                                    width: 80,
                                    height: 40,
                                    child: Center(
                                      child: Text(
                                        chan.name,
                                        style: const TextStyle(fontSize: 14, fontWeight: FontWeight.bold, color: Colors.white),
                                        textAlign: TextAlign.center,
                                        overflow: TextOverflow.ellipsis,
                                      ),
                                    ),
                                  ),
                                )
                              : SizedBox(
                                  width: 80,
                                  height: 40,
                                  child: Center(
                                    child: Text(
                                      chan.name,
                                      style: const TextStyle(fontSize: 14, fontWeight: FontWeight.bold, color: Colors.white),
                                      textAlign: TextAlign.center,
                                      overflow: TextOverflow.ellipsis,
                                    ),
                                  ),
                                ),
                          if (chan.guideNumber.isNotEmpty) const SizedBox(height: 4),
                          if (chan.guideNumber.isNotEmpty)
                            Text(
                              chan.guideNumber,
                              style: const TextStyle(fontSize: 13, fontWeight: FontWeight.bold, color: Colors.white54),
                            ),
                        ],
                      ),
                    );
                  }),
                ],
              ),
            ),
            
            // Scrollable Timeline + Programs
            Expanded(
              child: SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                clipBehavior: Clip.none,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // Timeline Header
                    Container(
                      height: 30,
                      padding: const EdgeInsets.only(bottom: 5),
                      child: Row(
                        children: List.generate(8, (index) { // 8 half-hours = 4 hours
                          final time = startOfTimeline.add(Duration(minutes: index * 30));
                          final formatted = _formatTimeGrid(time);
                          return SizedBox(
                            width: 30 * pixelsPerMinute,
                            child: Text(
                              formatted,
                              style: const TextStyle(color: Colors.white54, fontWeight: FontWeight.bold, fontSize: 16),
                            ),
                          );
                        }),
                      ),
                    ),
                    
                    // Program Rows
                    ...gridChannels.map((chan) {
                      final epg = _epgData[chan.id.toLowerCase()];
                      final programs = epg?.programs ?? [];
                      
                      return Container(
                        height: 90,
                        width: 4 * 60 * pixelsPerMinute, // 4 hours total width
                        margin: const EdgeInsets.only(bottom: 12),
                        child: Stack(
                          clipBehavior: Clip.none,
                          children: programs.map((p) {
                            var pStart = p.startTime;
                            var pEnd = p.endTime;
                            
                            if (pStart.isBefore(startOfTimeline)) pStart = startOfTimeline;
                            if (pEnd.isAfter(endOfTimeline)) pEnd = endOfTimeline;
                            
                            if (pEnd.isBefore(startOfTimeline) || pStart.isAfter(endOfTimeline)) {
                              return const SizedBox.shrink(); // Outside view
                            }

                            final offsetMinutes = pStart.difference(startOfTimeline).inMinutes;
                            final durationMinutes = pEnd.difference(pStart).inMinutes;
                            
                            final leftOffset = offsetMinutes * pixelsPerMinute;
                            final width = durationMinutes * pixelsPerMinute;
                            
                            final isNow = !now.isBefore(p.startTime) && now.isBefore(p.endTime);
                            
                            return Positioned(
                              left: leftOffset,
                              width: width - 4, // 4px spacing between blocks
                              height: 90,
                              child: EpgProgramBlock(
                                program: p,
                                channel: chan,
                                isNowPlaying: isNow,
                                onFocus: _onChannelFocus,
                                onPlay: () {
                                  if (isNow) {
                                    _openChannel(chan);
                                  } else {
                                    _showRecordModal(p);
                                  }
                                },
                              ),
                            );
                          }).toList(),
                        ),
                      );
                    }),
                  ],
                ),
              ),
            ),
          ],
        ),
      ],
    );
  }

  @override
  Widget build(BuildContext context) {
    final hasFavorites = _channels.any((c) => c.isFavorite);
    
    return Scaffold(
      backgroundColor: const Color(0xFF0F0F0F),
      body: Stack(
        children: [
          // Main Content Area (Positioned behind the sidebar)
          Positioned(
            left: 80, // Offset by sidebar width
            right: 0,
            top: 0,
            bottom: 0,
            child: _isLoading
                ? const Center(child: CircularProgressIndicator(color: Colors.blueAccent))
                : Stack(
                    children: [
                      // Scrollable Guide beneath the hero
                      Positioned.fill(
                        child: SingleChildScrollView(
                          padding: const EdgeInsets.only(top: 380.0, left: 40.0, right: 0, bottom: 20.0), // padding matches hero height
                          clipBehavior: Clip.none,
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              if (_currentTabIndex == 0) ...[
                                if (hasFavorites)
                                  _buildChannelRow('Favorites', _channels.where((c) => c.isFavorite).toList()),
                                _buildChannelRow('All Channels', _channels),
                              ] else if (_currentTabIndex == 1) ...[
                                if (hasFavorites)
                                  _buildChannelRow('Favorites', _channels.where((c) => c.isFavorite).toList()),
                                _buildEpgGrid('Live TV Guide', _channels),
                              ],
                              const SizedBox(height: 40), // Extra padding at very bottom of scroll
                            ],
                          ),
                        ),
                      ),
                      // Sticky Full-Width Hero
                      if (_focusedChannel != null)
                        Positioned(
                          top: 0,
                          left: 0,
                          right: 0,
                          height: 360,
                          child: _buildFeaturedHero(_focusedChannel!),
                        ),
                    ],
                  ),
          ),
          
          // Sleek Sidebar (Painted ON TOP of everything else)
          Positioned(
            left: 0,
            top: 0,
            bottom: 0,
            width: 80,
            child: Container(
              decoration: BoxDecoration(
                color: const Color(0xFF151515),
                border: Border(right: BorderSide(color: Colors.white.withOpacity(0.05))),
                boxShadow: [
                  BoxShadow(
                    color: Colors.black.withOpacity(0.5),
                    blurRadius: 20,
                    offset: const Offset(5, 0),
                  )
                ],
              ),
              child: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  IconButton(
                    icon: Icon(Icons.grid_view_rounded, size: 32, color: _currentTabIndex == 0 ? Colors.blueAccent : Colors.white54),
                    tooltip: 'Channels',
                    onPressed: () => setState(() => _currentTabIndex = 0),
                  ),
                  const SizedBox(height: 50),
                  IconButton(
                    icon: Icon(Icons.view_list_rounded, size: 32, color: _currentTabIndex == 1 ? Colors.blueAccent : Colors.white54),
                    tooltip: 'Live Guide',
                    onPressed: () => setState(() => _currentTabIndex = 1),
                  ),
                  const SizedBox(height: 50),
                  IconButton(
                    icon: const Icon(Icons.tv, size: 32, color: Colors.white54),
                    tooltip: 'Live TV',
                    onPressed: () {
                      if (_channels.isEmpty) return;
                      final lastChannelId = context.read<AppSettings>().lastChannelId;
                      Channel? target = _channels.where((c) => c.id == lastChannelId).firstOrNull;
                      target ??= _channels.first;
                      _openChannel(target);
                    },
                  ),
                  const SizedBox(height: 50),
                  IconButton(
                    icon: const Icon(Icons.settings, size: 32, color: Colors.white54),
                    tooltip: 'Settings',
                    onPressed: () async {
                      await Navigator.push(
                        context,
                        MaterialPageRoute(builder: (_) => const SettingsScreen()),
                      );
                      if (mounted) {
                        await _loadChannels();
                      }
                    },
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
    final active = _isFocused || _isHovered;

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
        onEnter: (_) => setState(() => _isHovered = true),
        onExit: (_) => setState(() => _isHovered = false),
        child: GestureDetector(
          onTap: _playChannel,
          child: AnimatedScale(
            scale: active ? 1.05 : 1.0,
            duration: const Duration(milliseconds: 250),
            curve: Curves.easeOutCubic,
            child: AnimatedContainer(
              duration: const Duration(milliseconds: 250),
              curve: Curves.easeOutCubic,
              decoration: BoxDecoration(
                color: active ? const Color(0xFF252525) : const Color(0xFF1A1A1A),
                borderRadius: BorderRadius.circular(16),
                border: Border.all(
                  color: active ? Colors.blueAccent.withOpacity(0.8) : Colors.white.withOpacity(0.05),
                  width: active ? 2 : 1,
                ),
                boxShadow: active
                    ? [
                        BoxShadow(
                          color: Colors.blueAccent.withOpacity(0.3),
                          blurRadius: 20,
                          offset: const Offset(0, 8),
                        )
                      ]
                    : [],
              ),
              child: Stack(
                alignment: Alignment.center,
                children: [
                  // Glassmorphism highlight
                  Positioned.fill(
                    child: Container(
                      decoration: BoxDecoration(
                        borderRadius: BorderRadius.circular(14),
                        gradient: LinearGradient(
                          colors: [Colors.white.withOpacity(0.08), Colors.transparent],
                          begin: Alignment.topLeft,
                          end: Alignment.bottomRight,
                        ),
                      ),
                    ),
                  ),
                  Row(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      widget.channel.logoUrl.isNotEmpty
                          ? Image.network(
                              widget.channel.logoUrl,
                              width: 80,
                              height: 60,
                              fit: BoxFit.contain,
                              errorBuilder: (context, error, stackTrace) => SizedBox(
                                width: 80,
                                height: 60,
                                child: Center(
                                  child: Text(
                                    widget.channel.name,
                                    style: TextStyle(
                                      fontSize: 20,
                                      fontWeight: FontWeight.bold,
                                      color: active ? Colors.white : Colors.white70,
                                    ),
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                ),
                              ),
                            )
                          : SizedBox(
                              width: 80,
                              height: 60,
                              child: Center(
                                child: Text(
                                  widget.channel.name,
                                  style: TextStyle(
                                    fontSize: 20,
                                    fontWeight: FontWeight.bold,
                                    color: active ? Colors.white : Colors.white70,
                                  ),
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ),
                            ),
                      if (widget.channel.guideNumber.isNotEmpty) const SizedBox(width: 16),
                      if (widget.channel.guideNumber.isNotEmpty)
                        Text(
                          widget.channel.guideNumber,
                          style: TextStyle(
                            fontSize: 22,
                            fontWeight: FontWeight.bold,
                            color: active ? Colors.white : Colors.white70,
                          ),
                        ),
                    ],
                  ),
                ],
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
        builder: (_) => PlayerScreen(initialChannel: widget.channel, initialStreamUrl: widget.channel.streamUrl, channels: []),
      ),
    );
  }
}

class EpgProgramBlock extends StatefulWidget {
  final EPGProgram program;
  final Channel channel;
  final bool isNowPlaying;
  final void Function(Channel, {EPGProgram? program}) onFocus;
  final VoidCallback onPlay;

  const EpgProgramBlock({
    super.key,
    required this.program,
    required this.channel,
    required this.isNowPlaying,
    required this.onFocus,
    required this.onPlay,
  });

  @override
  State<EpgProgramBlock> createState() => _EpgProgramBlockState();
}

class _EpgProgramBlockState extends State<EpgProgramBlock> {
  bool _isFocused = false;
  bool _isHovered = false;

  String _formatTime(DateTime time) {
    final hour = time.hour > 12 ? time.hour - 12 : (time.hour == 0 ? 12 : time.hour);
    final min = time.minute.toString().padLeft(2, '0');
    final ampm = time.hour >= 12 ? 'PM' : 'AM';
    return '$hour:$min $ampm';
  }

  @override
  Widget build(BuildContext context) {
    final active = _isFocused || _isHovered;

    return Focus(
      onFocusChange: (hasFocus) {
        setState(() => _isFocused = hasFocus);
        if (hasFocus) {
          widget.onFocus(widget.channel, program: widget.program);
        }
      },
      onKeyEvent: (node, event) {
        if (event is KeyDownEvent && 
            (event.logicalKey == LogicalKeyboardKey.enter || event.logicalKey == LogicalKeyboardKey.select)) {
          widget.onPlay();
          return KeyEventResult.handled;
        }
        return KeyEventResult.ignored;
      },
      child: MouseRegion(
        onEnter: (_) => setState(() => _isHovered = true),
        onExit: (_) => setState(() => _isHovered = false),
        child: GestureDetector(
          onTap: widget.onPlay,
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 200),
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            decoration: BoxDecoration(
              color: active 
                ? Colors.blueAccent.withOpacity(0.9)
                : (widget.isNowPlaying ? const Color(0xFF2A2A2A) : const Color(0xFF1E1E1E)),
              borderRadius: BorderRadius.zero,
              border: Border.all(
                color: active ? Colors.white : Colors.white.withOpacity(0.05),
                width: active ? 2 : 1,
              ),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                Text(
                  widget.program.title,
                  style: TextStyle(
                    color: active ? Colors.white : Colors.white.withOpacity(0.9),
                    fontWeight: FontWeight.bold,
                    fontSize: 16,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 4),
                Text(
                  '${_formatTime(widget.program.startTime)} - ${_formatTime(widget.program.endTime)}',
                  style: TextStyle(
                    color: active ? Colors.white70 : Colors.white54,
                    fontSize: 14,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
