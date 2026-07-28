import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/channel.dart';
import '../models/playlist.dart';
import '../services/api_service.dart';

class ChannelManagementScreen extends StatefulWidget {
  const ChannelManagementScreen({super.key});

  @override
  State<ChannelManagementScreen> createState() => _ChannelManagementScreenState();
}

class _ChannelManagementScreenState extends State<ChannelManagementScreen> {
  List<Channel> _channels = [];
  List<Playlist> _playlists = [];
  Map<String, Playlist> _playlistsById = {};
  bool _isLoading = true;

  @override
  void initState() {
    super.initState();
    _fetchChannels();
  }

  Future<void> _fetchChannels() async {
    try {
      final api = context.read<ApiService>();
      final channels = await api.getChannels();
      final playlists = await api.getPlaylists();
      setState(() {
        _channels = channels;
        _playlists = playlists;
        _playlistsById = {for (final p in playlists) p.id: p};
        _isLoading = false;
      });
    } catch (e) {
      if (mounted) {
        setState(() => _isLoading = false);
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error loading channels: $e')));
      }
    }
  }

  List<Channel> _sortedChannels(Iterable<Channel> channels) {
    final list = channels.toList();
    list.sort((a, b) {
      if (a.channelNumber != b.channelNumber) {
        return a.channelNumber.compareTo(b.channelNumber);
      }
      return a.name.toLowerCase().compareTo(b.name.toLowerCase());
    });
    return list;
  }

  List<_ChannelTab> _buildTabs() {
    final tabs = <_ChannelTab>[];

    for (final playlist in _playlists) {
      final label = playlist.name.trim().isNotEmpty ? playlist.name.trim() : playlist.type.toUpperCase();
      tabs.add(
        _ChannelTab(
          label: label,
          channels: _sortedChannels(_channels.where((c) => c.playlistId == playlist.id)),
        ),
      );
    }

    final knownPlaylistIds = _playlistsById.keys.toSet();
    final orphanChannels = _sortedChannels(_channels.where((c) => !knownPlaylistIds.contains(c.playlistId)));
    if (orphanChannels.isNotEmpty) {
      tabs.add(_ChannelTab(label: 'Other', channels: orphanChannels));
    }

    if (tabs.isEmpty) {
      tabs.add(_ChannelTab(label: 'All Channels', channels: _sortedChannels(_channels)));
    }

    return tabs;
  }

  Widget _buildChannelTile(Channel channel) {
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 24, vertical: 8),
      leading: channel.logoUrl.isNotEmpty
          ? Image.network(
              channel.logoUrl,
              width: 50,
              height: 50,
              fit: BoxFit.contain,
              errorBuilder: (_, __, ___) => const Icon(Icons.tv, color: Colors.white54, size: 40),
            )
          : const Icon(Icons.tv, color: Colors.white54, size: 40),
      title: Text(
        channel.name,
        style: TextStyle(
          color: channel.isHidden ? Colors.white54 : Colors.white,
          fontSize: 18,
          fontWeight: FontWeight.w600,
          decoration: channel.isHidden ? TextDecoration.lineThrough : null,
        ),
      ),
      subtitle: Text(
        'Guide Number: ${channel.guideNumber.isNotEmpty ? channel.guideNumber : 'None'}',
        style: const TextStyle(color: Colors.white38),
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            icon: const Icon(Icons.edit, color: Colors.blueAccent),
            tooltip: 'Edit Logo',
            onPressed: () => _editLogo(channel),
          ),
          const SizedBox(width: 16),
          Switch(
            value: !channel.isHidden,
            onChanged: (val) => _toggleVisibility(channel, val),
            activeColor: Colors.greenAccent,
            inactiveThumbColor: Colors.redAccent,
            inactiveTrackColor: Colors.redAccent.withOpacity(0.3),
          ),
        ],
      ),
    );
  }

  Widget _buildChannelList(List<Channel> channels) {
    if (channels.isEmpty) {
      return const Center(
        child: Text(
          'No channels in this tuner.',
          style: TextStyle(color: Colors.white54, fontSize: 16),
        ),
      );
    }

    return ListView.separated(
      padding: const EdgeInsets.only(bottom: 24),
      itemCount: channels.length,
      separatorBuilder: (_, __) => const Divider(color: Colors.white24, height: 1),
      itemBuilder: (context, index) => _buildChannelTile(channels[index]),
    );
  }

  Future<void> _toggleVisibility(Channel channel, bool isVisible) async {
    final api = context.read<ApiService>();
    final newHiddenState = !isVisible;
    
    // Optimistic UI update
    setState(() {
      final index = _channels.indexWhere((c) => c.id == channel.id);
      if (index != -1) {
        _channels[index] = Channel(
          id: channel.id,
          playlistId: channel.playlistId,
          groupId: channel.groupId,
          name: channel.name,
          streamUrl: channel.streamUrl,
          logoUrl: channel.logoUrl,
          channelNumber: channel.channelNumber,
          guideNumber: channel.guideNumber,
          isHidden: newHiddenState,
          isFavorite: channel.isFavorite,
        );
      }
    });

    try {
      await api.updateChannelVisibility(channel.id, newHiddenState);
    } catch (e) {
      // Revert on failure
      setState(() {
        final index = _channels.indexWhere((c) => c.id == channel.id);
        if (index != -1) {
          _channels[index] = channel;
        }
      });
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Failed to update visibility: $e')));
      }
    }
  }

  Future<void> _editLogo(Channel channel) async {
    final controller = TextEditingController(text: channel.logoUrl);
    final url = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Edit Logo for ${channel.name}'),
        content: TextField(
          controller: controller,
          decoration: const InputDecoration(
            hintText: 'Enter Logo URL (https://...)',
            border: OutlineInputBorder(),
          ),
          autofocus: true,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () => Navigator.pop(context, controller.text.trim()),
            child: const Text('Save'),
          ),
        ],
      ),
    );

    if (url != null && mounted) {
      final api = context.read<ApiService>();
      
      // Optimistic update
      final oldLogo = channel.logoUrl;
      setState(() {
        final index = _channels.indexWhere((c) => c.id == channel.id);
        if (index != -1) {
          _channels[index] = Channel(
            id: channel.id,
            playlistId: channel.playlistId,
            groupId: channel.groupId,
            name: channel.name,
            streamUrl: channel.streamUrl,
            logoUrl: url,
            channelNumber: channel.channelNumber,
            guideNumber: channel.guideNumber,
            isHidden: channel.isHidden,
            isFavorite: channel.isFavorite,
          );
        }
      });

      try {
        await api.updateChannelLogo(channel.id, url);
      } catch (e) {
        // Revert on failure
        setState(() {
          final index = _channels.indexWhere((c) => c.id == channel.id);
          if (index != -1) {
            _channels[index] = Channel(
              id: channel.id,
              playlistId: channel.playlistId,
              groupId: channel.groupId,
              name: channel.name,
              streamUrl: channel.streamUrl,
              logoUrl: oldLogo,
              channelNumber: channel.channelNumber,
              guideNumber: channel.guideNumber,
              isHidden: channel.isHidden,
              isFavorite: channel.isFavorite,
            );
          }
        });
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Failed to update logo: $e')));
        }
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_isLoading) {
      return Scaffold(
        backgroundColor: Colors.black,
        appBar: AppBar(
          title: const Text('Manage Channels'),
          backgroundColor: Colors.black,
          elevation: 0,
        ),
        body: const Center(child: CircularProgressIndicator()),
      );
    }

    final tabs = _buildTabs();
    return DefaultTabController(
      length: tabs.length,
      child: Scaffold(
        backgroundColor: Colors.black,
        appBar: AppBar(
          title: const Text('Manage Channels'),
          backgroundColor: Colors.black,
          elevation: 0,
          bottom: TabBar(
            isScrollable: true,
            indicatorColor: Colors.blueAccent,
            labelColor: Colors.white,
            unselectedLabelColor: Colors.white54,
            tabs: tabs
                .map(
                  (tab) => Tab(text: '${tab.label} (${tab.channels.length})'),
                )
                .toList(),
          ),
        ),
        body: TabBarView(
          children: tabs.map((tab) => _buildChannelList(tab.channels)).toList(),
        ),
      ),
    );
  }
}

class _ChannelTab {
  final String label;
  final List<Channel> channels;

  const _ChannelTab({
    required this.label,
    required this.channels,
  });
}
