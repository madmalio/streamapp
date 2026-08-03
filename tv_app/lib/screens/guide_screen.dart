import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'dart:async';
import 'dart:ui';
import '../models/channel.dart';
import '../models/epg_program.dart';
import '../models/playlist.dart';
import '../services/api_service.dart';
import '../services/app_settings.dart';
import 'player_screen.dart';
import 'settings_screen.dart';

class GuideScreen extends StatefulWidget {
  const GuideScreen({super.key});

  @override
  State<GuideScreen> createState() => _GuideScreenState();
}

class _TunerTab extends StatefulWidget {
  final Widget child;
  const _TunerTab({required this.child});
  @override
  State<_TunerTab> createState() => _TunerTabState();
}

class _TunerTabState extends State<_TunerTab> with AutomaticKeepAliveClientMixin {
  @override
  bool get wantKeepAlive => true;
  @override
  Widget build(BuildContext context) {
    super.build(context);
    return widget.child;
  }
}

int _getCategoryOrder(String category) {
  const order = [
    'Entertainment',
    'Movies',
    'News',
    'Sports',
    'Comedy',
    'Kids',
    'Documentary',
    'Crime & Mystery',
    'Music',
    'Local',
  ];
  final index = order.indexOf(category);
  return index == -1 ? 999 : index;
}

class _GuideScreenState extends State<GuideScreen> with TickerProviderStateMixin {
  static const bool _prewarmEnabled = false;


  List<Playlist> _playlists = [];
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
  TabController? _tabController;
  String? _selectedCategory;

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
      final appSettings = Provider.of<AppSettings>(context, listen: false);
      final channels = await api.getChannels();
      final playlists = await api.getPlaylists();
      
      // Debug: Check favorite status
      final favoriteCount = channels.where((c) => c.isFavorite).length;
      print('🔍 DEBUG: Loaded ${channels.length} channels, $favoriteCount are favorites');
      if (favoriteCount > 0) {
        final favorites = channels.where((c) => c.isFavorite).take(3).toList();
        for (var fav in favorites) {
          print('  ⭐ Favorite: ${fav.name} (id: ${fav.id})');
        }
      }
      
      Map<String, ChannelEPG> epg = {};
      try {
        epg = await api.getLiveEpg();
      } catch (_) {
        // EPG might not be synced yet
      }

      // Filter out hidden channels and channels without a name
      var filteredChannels = channels.where((c) => c.name.trim().isNotEmpty && !c.isHidden).toList();

      Channel? nextFocusedChannel;
      if (playlists.isNotEmpty) {
        final firstPlaylistChannels = filteredChannels.where((c) => c.playlistId == playlists.first.id).toList();
        if (firstPlaylistChannels.isNotEmpty) {
          nextFocusedChannel = firstPlaylistChannels.first;
        }
      }
      nextFocusedChannel ??= (filteredChannels.isNotEmpty ? filteredChannels.first : null);

      setState(() {
        _playlists = playlists;
        _channels = filteredChannels;
        _epgData = epg;
        _focusedChannel = nextFocusedChannel;
        _focusedProgram = nextFocusedChannel == null ? null : _epgData[nextFocusedChannel.id.toLowerCase()]?.currentProgram;
        _isLoading = false;
      });

      _initTabController();
    } catch (e) {
      setState(() => _isLoading = false);
      // Handled simply for now
    }
  }

  void _initTabController() {
    _tabController?.dispose();
    final tabCount = _playlists.isEmpty ? 1 : _playlists.length;
    _tabController = TabController(length: tabCount, vsync: this);
    _tabController!.addListener(_onTabChanged);
  }

  void _onTabChanged() {
    if (_tabController!.indexIsChanging) return;
    
    setState(() {
      _selectedCategory = null;
    });

    if (_playlists.isEmpty) return;
    
    final selectedPlaylist = _playlists[_tabController!.index];
    final tunerChannels = _channels.where((c) => c.playlistId == selectedPlaylist.id).toList();
    
    if (tunerChannels.isNotEmpty) {
      _onChannelFocus(tunerChannels.first, program: _epgData[tunerChannels.first.id.toLowerCase()]?.currentProgram);
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
    // Debug: Log channel favorite status when clicked
    print('🎯 DEBUG: Opening channel: ${channel.name}');
    print('  channel.isFavorite: ${channel.isFavorite}');
    print('  channel.id: ${channel.id}');
    
    // If we are opening the currently prewarmed channel, do NOT kill it!
    // PlayerScreen will adopt the exact same HLS Session ID from the backend.
    _prewarmTimer?.cancel();
    
    if (!mounted) return;
    final settings = context.read<AppSettings>();
    await settings.setLastChannelId(channel.id);

    Channel? previousChannel;
    if (settings.previousChannelId.isNotEmpty) {
      previousChannel = _channels.where((c) => c.id == settings.previousChannelId).firstOrNull;
    }

    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => PlayerScreen(
          initialChannel: channel, 
          initialStreamUrl: channel.streamUrl,
          channels: _channels.where((c) => c.playlistId == channel.playlistId).toList(),
          epgData: _epgData,
          initialPreviousChannel: previousChannel,
        ),
      ),
    ).then((_) {
      // Clean up when returning from the player
      _prewarmToken += 1;
      _activePrewarmSessionId = null;
      // Refresh channels to get updated favorite status
      _loadChannels();
    });
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
    _tabController?.dispose();
    _prewarmTimer?.cancel();
    unawaited(_releaseActivePrewarm());
    super.dispose();
  }

  Widget _buildFeaturedHero(Channel channel, {required double heroHeight}) {
    final compactHero = heroHeight < 300;
    return AnimatedSwitcher(
      duration: const Duration(milliseconds: 600),
      switchInCurve: Curves.easeOutCubic,
      switchOutCurve: Curves.easeInCubic,
      child: Container(
        key: ValueKey('${channel.id}_${_focusedProgram?.id}'),
        height: heroHeight,
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
                  padding: EdgeInsets.all(compactHero ? 28 : 40),
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
                            height: compactHero ? 28 : 32,
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
                    style: TextStyle(
                      fontSize: compactHero ? 36 : 48,
                      fontWeight: FontWeight.w900,
                      color: Colors.white,
                      letterSpacing: -0.5,
                      shadows: const [Shadow(color: Colors.black54, blurRadius: 10, offset: Offset(0, 4))],
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 8),
                  Text(
                    _focusedProgram?.description ?? 'HDHomeRun Network Broadcast',
                    style: TextStyle(
                      fontSize: compactHero ? 16 : 18,
                      color: Colors.white70,
                      fontWeight: FontWeight.w500,
                    ),
                    maxLines: compactHero ? 1 : 2,
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

  List<Widget> _buildGroupedChannelRows(List<Channel> channelsList, {required String fallbackGroupName}) {
    if (channelsList.isEmpty) return const [];
    
    final Map<String, List<Channel>> groups = {};
    for (var c in channelsList) {
      final groupName = (c.normalizedCategory.isNotEmpty) ? c.normalizedCategory : fallbackGroupName;
      groups.putIfAbsent(groupName, () => []).add(c);
    }

    // Add Favorites as a special category if there are any favorites
    final favoriteChannels = channelsList.where((c) => c.isFavorite).toList();
    if (favoriteChannels.isNotEmpty) {
      groups['Favorites'] = favoriteChannels;
    }

    if (groups.length <= 1) {
      return const [];
    }

    final sortedKeys = groups.keys.toList()..sort((a, b) {
      // Favorites always comes first
      if (a == 'Favorites') return -1;
      if (b == 'Favorites') return 1;
      if (a == fallbackGroupName || a == 'Other') return 1;
      if (b == fallbackGroupName || b == 'Other') return -1;
      final orderA = _getCategoryOrder(a);
      final orderB = _getCategoryOrder(b);
      if (orderA != orderB) return orderA.compareTo(orderB);
      return a.compareTo(b);
    });

    final List<Widget> rows = [];
    for (var key in sortedKeys) {
      rows.add(_buildChannelRow(key, groups[key]!));
    }
    return rows;
  }

  Widget _buildEpgGrid(String title, List<Channel> gridChannels, {required double availableHeight, required String fallbackGroupName}) {
    final Map<String, List<Channel>> groups = {};
    for (var c in gridChannels) {
      final groupName = (c.normalizedCategory.isNotEmpty) ? c.normalizedCategory : fallbackGroupName;
      groups.putIfAbsent(groupName, () => []).add(c);
    }

    // Add Favorites as a special category if there are any favorites
    final favoriteChannels = gridChannels.where((c) => c.isFavorite).toList();
    if (favoriteChannels.isNotEmpty) {
      groups['Favorites'] = favoriteChannels;
    }

    final sortedCategories = groups.keys.toList()..sort((a, b) {
      // Favorites always comes first
      if (a == 'Favorites') return -1;
      if (b == 'Favorites') return 1;
      if (a == fallbackGroupName || a == 'Other') return 1;
      if (b == fallbackGroupName || b == 'Other') return -1;
      final orderA = _getCategoryOrder(a);
      final orderB = _getCategoryOrder(b);
      if (orderA != orderB) return orderA.compareTo(orderB);
      return a.compareTo(b);
    });

    List<Channel> displayedChannels = gridChannels;
    if (_selectedCategory != null && _selectedCategory != 'All Channels') {
      displayedChannels = groups[_selectedCategory!] ?? [];
    }

    return _StickyEpgGrid(
      title: title,
      gridChannels: displayedChannels,
      epgData: _epgData,
      availableHeight: availableHeight,
      onFocus: _onChannelFocus,
      onOpenChannel: _openChannel,
      onRecordProgram: _showRecordModal,
      categories: sortedCategories,
      selectedCategory: _selectedCategory ?? 'All Channels',
      onCategoryChanged: (String? newCategory) {
        setState(() {
          _selectedCategory = newCategory;
        });
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    final tabCount = _playlists.isEmpty ? 1 : _playlists.length;
    final screenHeight = MediaQuery.of(context).size.height;
    final heroHeight = screenHeight >= 1200
        ? 320.0
        : screenHeight >= 980
            ? 280.0
            : 240.0;
    const tabBarHeight = 50.0;
    final guideViewportHeight = (screenHeight - (heroHeight + tabBarHeight) - 40.0).clamp(260.0, 2000.0).toDouble();
    
    if (_isLoading || _tabController == null) {
      return const Scaffold(
        backgroundColor: Color(0xFF0F0F0F),
        body: Center(child: CircularProgressIndicator(color: Colors.blueAccent)),
      );
    }

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
                        // Sticky Full-Width Hero
                        if (_focusedChannel != null)
                          Positioned(
                            top: 0,
                            left: 0,
                            right: 0,
                            height: heroHeight,
                            child: _buildFeaturedHero(_focusedChannel!, heroHeight: heroHeight),
                          ),
                        
                        // Sticky TabBar below hero
                        Positioned(
                          top: heroHeight,
                          left: 40,
                          right: 40,
                          child: TabBar(
                            controller: _tabController,
                            isScrollable: true,
                            indicatorColor: Colors.blueAccent,
                            labelColor: Colors.white,
                            unselectedLabelColor: Colors.white54,
                            tabs: _playlists.isEmpty
                                ? [const Tab(text: 'All Channels')]
                                : _playlists.map((p) => Tab(text: p.name)).toList(),
                          ),
                        ),
                        
                        // Scrollable Guide beneath the TabBar
                        Positioned.fill(
                          top: heroHeight + tabBarHeight,
                          child: TabBarView(
                            controller: _tabController,
                            children: _playlists.isEmpty
                                ? [
                                    _TunerTab(
                                      child: SingleChildScrollView(
                                        padding: const EdgeInsets.only(top: 20.0, left: 40.0, right: 0, bottom: 20.0),
                                        clipBehavior: Clip.none,
                                        child: IndexedStack(
                                          index: _currentTabIndex,
                                          children: [
                                            Column(
                                              crossAxisAlignment: CrossAxisAlignment.start,
                                              children: [
                                                _buildChannelRow('All Channels', _channels),
                                                ..._buildGroupedChannelRows(_channels, fallbackGroupName: 'Other Channels'),
                                                const SizedBox(height: 40),
                                              ],
                                            ),
                                            Column(
                                              crossAxisAlignment: CrossAxisAlignment.start,
                                              children: [
                                                _buildEpgGrid('Live TV Guide', _channels, availableHeight: guideViewportHeight, fallbackGroupName: 'Other Channels'),
                                              ],
                                            ),
                                          ],
                                        ),
                                      ),
                                    )
                                  ]
                                : _playlists.map((p) {
                                    final tunerChannels = _channels.where((c) => c.playlistId == p.id).toList();
                                    return _TunerTab(
                                      child: SingleChildScrollView(
                                        padding: const EdgeInsets.only(top: 20.0, left: 40.0, right: 0, bottom: 20.0),
                                        clipBehavior: Clip.none,
                                        child: IndexedStack(
                                          index: _currentTabIndex,
                                          children: [
                                            Column(
                                              crossAxisAlignment: CrossAxisAlignment.start,
                                              children: [
                                                if (tunerChannels.isNotEmpty) 
                                                  _buildChannelRow('All ${p.name} Channels', tunerChannels),
                                                ..._buildGroupedChannelRows(tunerChannels, fallbackGroupName: 'Other ${p.name} Channels'),
                                                const SizedBox(height: 40),
                                              ],
                                            ),
                                            Column(
                                              crossAxisAlignment: CrossAxisAlignment.start,
                                              children: [
                                                if (tunerChannels.isNotEmpty)
                                                  _buildEpgGrid('${p.name} Guide', tunerChannels, availableHeight: guideViewportHeight, fallbackGroupName: 'Other ${p.name} Channels'),
                                              ],
                                            ),
                                          ],
                                        ),
                                      ),
                                    );
                                  }).toList(),
                          ),
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

class _StickyEpgGrid extends StatefulWidget {
  final String title;
  final List<Channel> gridChannels;
  final Map<String, ChannelEPG> epgData;
  final double availableHeight;
  final void Function(Channel, {EPGProgram? program}) onFocus;
  final ValueChanged<Channel> onOpenChannel;
  final ValueChanged<EPGProgram> onRecordProgram;
  final List<String> categories;
  final String selectedCategory;
  final ValueChanged<String?> onCategoryChanged;

  const _StickyEpgGrid({
    required this.title,
    required this.gridChannels,
    required this.epgData,
    required this.availableHeight,
    required this.onFocus,
    required this.onOpenChannel,
    required this.onRecordProgram,
    this.categories = const [],
    this.selectedCategory = 'All Channels',
    this.onCategoryChanged = _defaultCategoryChanged,
  });

  static void _defaultCategoryChanged(String? _) {}

  @override
  State<_StickyEpgGrid> createState() => _StickyEpgGridState();
}

class _StickyEpgGridState extends State<_StickyEpgGrid> {
  static const double _pixelsPerMinute = 10.0;
  static const double _rowHeight = 90.0;
  static const double _rowGap = 12.0;
  static const double _timelineHeight = 30.0;

  final ScrollController _horizontalController = ScrollController();
  final ScrollController _leftVerticalController = ScrollController();
  final ScrollController _rightVerticalController = ScrollController();

  Timer? _nowLineTimer;
  bool _syncingLeft = false;
  bool _syncingRight = false;

  @override
  void initState() {
    super.initState();
    _leftVerticalController.addListener(_syncVerticalFromLeft);
    _rightVerticalController.addListener(_syncVerticalFromRight);
    _nowLineTimer = Timer.periodic(const Duration(seconds: 30), (_) {
      if (mounted) {
        setState(() {});
      }
    });
  }

  @override
  void dispose() {
    _nowLineTimer?.cancel();
    _leftVerticalController.removeListener(_syncVerticalFromLeft);
    _rightVerticalController.removeListener(_syncVerticalFromRight);
    _horizontalController.dispose();
    _leftVerticalController.dispose();
    _rightVerticalController.dispose();
    super.dispose();
  }

  void _syncVerticalFromLeft() {
    if (_syncingLeft || !_rightVerticalController.hasClients || !_leftVerticalController.hasClients) {
      return;
    }
    _syncingRight = true;
    final target = _leftVerticalController.offset.clamp(
      0.0,
      _rightVerticalController.position.maxScrollExtent,
    );
    _rightVerticalController.jumpTo(target);
    _syncingRight = false;
  }

  void _syncVerticalFromRight() {
    if (_syncingRight || !_leftVerticalController.hasClients || !_rightVerticalController.hasClients) {
      return;
    }
    _syncingLeft = true;
    final target = _rightVerticalController.offset.clamp(
      0.0,
      _leftVerticalController.position.maxScrollExtent,
    );
    _leftVerticalController.jumpTo(target);
    _syncingLeft = false;
  }

  String _formatTimeGrid(DateTime time) {
    final hour = time.hour > 12 ? time.hour - 12 : (time.hour == 0 ? 12 : time.hour);
    final min = time.minute.toString().padLeft(2, '0');
    final ampm = time.hour >= 12 ? 'PM' : 'AM';
    return '$hour:$min $ampm';
  }

  List<_TimelineSegment> _buildTimelineSegments({
    required List<EPGProgram> programs,
    required DateTime startOfTimeline,
    required DateTime endOfTimeline,
  }) {
    final sorted = [...programs]..sort((a, b) => a.startTime.compareTo(b.startTime));
    final segments = <_TimelineSegment>[];
    var cursor = startOfTimeline;

    for (final p in sorted) {
      var pStart = p.startTime;
      var pEnd = p.endTime;

      if (pStart.isBefore(startOfTimeline)) pStart = startOfTimeline;
      if (pEnd.isAfter(endOfTimeline)) pEnd = endOfTimeline;
      if (!pEnd.isAfter(pStart)) continue;

      if (pStart.isAfter(cursor)) {
        segments.add(_TimelineSegment(start: cursor, end: pStart));
      }

      segments.add(_TimelineSegment(start: pStart, end: pEnd, program: p));
      if (pEnd.isAfter(cursor)) cursor = pEnd;
    }

    if (cursor.isBefore(endOfTimeline)) {
      segments.add(_TimelineSegment(start: cursor, end: endOfTimeline));
    }

    if (segments.isEmpty) {
      segments.add(_TimelineSegment(start: startOfTimeline, end: endOfTimeline));
    }

    return segments;
  }

  Widget _buildCategoryChip(String category) {
    final isSelected = widget.selectedCategory == category;
    return Padding(
      padding: const EdgeInsets.only(right: 8),
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          onTap: () => widget.onCategoryChanged(category),
          borderRadius: BorderRadius.circular(20),
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 200),
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            decoration: BoxDecoration(
              color: isSelected ? Colors.white : Colors.white.withValues(alpha: 0.1),
              borderRadius: BorderRadius.circular(20),
              border: Border.all(
                color: isSelected ? Colors.white : Colors.transparent,
                width: 1,
              ),
            ),
            child: Center(
              child: Text(
                category,
                style: TextStyle(
                  color: isSelected ? Colors.black : Colors.white70,
                  fontWeight: isSelected ? FontWeight.bold : FontWeight.w500,
                  fontSize: 14,
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    if (widget.gridChannels.isEmpty) {
      return const SizedBox.shrink();
    }

    final now = DateTime.now();
    final startOfTimeline = DateTime(now.year, now.month, now.day, now.hour, now.minute < 30 ? 0 : 30);
    final endOfTimeline = startOfTimeline.add(const Duration(hours: 4));
    final totalMinutes = endOfTimeline.difference(startOfTimeline).inMinutes.toDouble();
    final timelineWidth = totalMinutes * _pixelsPerMinute;
    final halfHourSlots = (totalMinutes / 30).round();

    final rowsViewportHeight = (widget.availableHeight - 44.0).clamp(220.0, 2000.0).toDouble();

    final nowOffsetMinutes = now.difference(startOfTimeline).inSeconds / 60.0;
    final showNowLine = nowOffsetMinutes >= 0 && nowOffsetMinutes <= totalMinutes;
    final nowLineLeft = nowOffsetMinutes * _pixelsPerMinute;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 10, bottom: 10),
          child: Text(
            widget.title,
            style: const TextStyle(
              fontSize: 22,
              fontWeight: FontWeight.w800,
              color: Colors.white,
              letterSpacing: 0.5,
            ),
          ),
        ),
        if (widget.categories.isNotEmpty)
          Container(
            height: 40,
            margin: const EdgeInsets.only(bottom: 12),
            child: ListView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 10),
              children: [
                _buildCategoryChip('All Channels'),
                ...widget.categories.map((c) => _buildCategoryChip(c)),
              ],
            ),
          ),
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 140,
              child: Column(
                children: [
                  const SizedBox(height: _timelineHeight),
                  SizedBox(
                    height: rowsViewportHeight,
                    child: ListView.separated(
                      controller: _leftVerticalController,
                      itemCount: widget.gridChannels.length,
                      separatorBuilder: (_, __) => const SizedBox(height: _rowGap),
                      itemBuilder: (context, index) {
                        final chan = widget.gridChannels[index];
                        return Container(
                          height: _rowHeight,
                          margin: const EdgeInsets.symmetric(horizontal: 10),
                          decoration: const BoxDecoration(
                            color: Color(0xFF1A1A1A),
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
                                            style: const TextStyle(
                                              fontSize: 14,
                                              fontWeight: FontWeight.bold,
                                              color: Colors.white,
                                            ),
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
                                          style: const TextStyle(
                                            fontSize: 14,
                                            fontWeight: FontWeight.bold,
                                            color: Colors.white,
                                          ),
                                          textAlign: TextAlign.center,
                                          overflow: TextOverflow.ellipsis,
                                        ),
                                      ),
                                    ),
                              if (chan.guideNumber.isNotEmpty) const SizedBox(height: 4),
                              if (chan.guideNumber.isNotEmpty)
                                Text(
                                  chan.guideNumber,
                                  style: const TextStyle(
                                    fontSize: 13,
                                    fontWeight: FontWeight.bold,
                                    color: Colors.white54,
                                  ),
                                ),
                            ],
                          ),
                        );
                      },
                    ),
                  ),
                ],
              ),
            ),
            Expanded(
              child: SingleChildScrollView(
                controller: _horizontalController,
                scrollDirection: Axis.horizontal,
                clipBehavior: Clip.none,
                child: SizedBox(
                  width: timelineWidth,
                  child: Stack(
                    children: [
                      Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          SizedBox(
                            height: _timelineHeight,
                            child: Row(
                              children: List.generate(halfHourSlots, (index) {
                                final time = startOfTimeline.add(Duration(minutes: index * 30));
                                final formatted = _formatTimeGrid(time);
                                return SizedBox(
                                  width: 30 * _pixelsPerMinute,
                                  child: Text(
                                    formatted,
                                    style: const TextStyle(
                                      color: Colors.white54,
                                      fontWeight: FontWeight.bold,
                                      fontSize: 16,
                                    ),
                                  ),
                                );
                              }),
                            ),
                          ),
                          SizedBox(
                            height: rowsViewportHeight,
                            child: ListView.separated(
                              controller: _rightVerticalController,
                              itemCount: widget.gridChannels.length,
                              separatorBuilder: (_, __) => const SizedBox(height: _rowGap),
                              itemBuilder: (context, index) {
                                final chan = widget.gridChannels[index];
                                final epg = widget.epgData[chan.id.toLowerCase()];
                                final programs = epg?.programs ?? <EPGProgram>[];
                                final segments = _buildTimelineSegments(
                                  programs: programs,
                                  startOfTimeline: startOfTimeline,
                                  endOfTimeline: endOfTimeline,
                                );

                                return SizedBox(
                                  height: _rowHeight,
                                  width: timelineWidth,
                                  child: Stack(
                                    clipBehavior: Clip.none,
                                    children: segments.map((segment) {
                                      final offsetMinutes = segment.start.difference(startOfTimeline).inMinutes;
                                      final durationMinutes = segment.end.difference(segment.start).inMinutes;
                                      final leftOffset = offsetMinutes * _pixelsPerMinute;
                                      final width = durationMinutes * _pixelsPerMinute;

                                      if (segment.program == null) {
                                        return Positioned(
                                          left: leftOffset,
                                          width: width,
                                          height: _rowHeight,
                                          child: Container(
                                            color: const Color(0xFF131313),
                                          ),
                                        );
                                      }

                                      final program = segment.program!;
                                      final isNow = !now.isBefore(program.startTime) && now.isBefore(program.endTime);

                                      return Positioned(
                                        left: leftOffset,
                                        width: width - 4,
                                        height: _rowHeight,
                                        child: EpgProgramBlock(
                                          program: program,
                                          channel: chan,
                                          isNowPlaying: isNow,
                                          onFocus: widget.onFocus,
                                          onPlay: () {
                                            if (isNow) {
                                              widget.onOpenChannel(chan);
                                            } else {
                                              widget.onRecordProgram(program);
                                            }
                                          },
                                        ),
                                      );
                                    }).toList(),
                                  ),
                                );
                              },
                            ),
                          ),
                        ],
                      ),
                      if (showNowLine)
                        Positioned(
                          left: nowLineLeft,
                          top: 0,
                          bottom: 0,
                          child: IgnorePointer(
                            child: Container(
                              width: 2,
                              color: Colors.redAccent.withOpacity(0.9),
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
              ),
            ),
          ],
        ),
      ],
    );
  }
}

class _TimelineSegment {
  final DateTime start;
  final DateTime end;
  final EPGProgram? program;

  const _TimelineSegment({
    required this.start,
    required this.end,
    this.program,
  });
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
                  // Favorite indicator
                  if (widget.channel.isFavorite)
                    Positioned(
                      top: 8,
                      right: 8,
                      child: Icon(
                        Icons.favorite,
                        color: Colors.redAccent,
                        size: 16,
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
          child: LayoutBuilder(
            builder: (context, constraints) {
              final showTime = active;
              return AnimatedContainer(
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
                      maxLines: showTime ? 1 : 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    if (showTime) ...[
                      const SizedBox(height: 4),
                      Text(
                        '${_formatTime(widget.program.startTime)} - ${_formatTime(widget.program.endTime)}',
                        style: TextStyle(
                          color: active ? Colors.white70 : Colors.white54,
                          fontSize: 14,
                        ),
                      ),
                    ],
                  ],
                ),
              );
            },
          ),
        ),
      ),
    );
  }
}
