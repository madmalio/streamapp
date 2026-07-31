import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/channel.dart';
import '../models/playlist.dart';
import '../services/api_service.dart';

class TunerEditor extends StatefulWidget {
  final Playlist playlist;

  const TunerEditor({Key? key, required this.playlist}) : super(key: key);

  @override
  State<TunerEditor> createState() => _TunerEditorState();
}

class _TunerEditorState extends State<TunerEditor> {
  List<Channel> _channels = [];
  bool _isLoading = true;

  bool get _isVirtual => widget.playlist.type == 'VIRTUAL';

  @override
  void initState() {
    super.initState();
    _fetchChannels();
  }

  Future<void> _fetchChannels() async {
    try {
      final api = context.read<ApiService>();
      final allChannels = await api.getChannels();
      
      final tunerChannels = allChannels.where((c) => c.playlistId == widget.playlist.id).toList();
      tunerChannels.sort((a, b) => a.channelNumber.compareTo(b.channelNumber));

      setState(() {
        _channels = tunerChannels;
        _isLoading = false;
      });
    } catch (e) {
      if (mounted) {
        setState(() => _isLoading = false);
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error loading channels: $e')));
      }
    }
  }

  Future<void> _onReorder(int oldIndex, int newIndex) async {
    if (newIndex > oldIndex) {
      newIndex -= 1;
    }
    
    setState(() {
      final item = _channels.removeAt(oldIndex);
      _channels.insert(newIndex, item);
      
      for (int i = 0; i < _channels.length; i++) {
        _channels[i] = Channel(
          id: _channels[i].id,
          playlistId: _channels[i].playlistId,
          groupId: _channels[i].groupId,
          name: _channels[i].name,
          streamUrl: _channels[i].streamUrl,
          logoUrl: _channels[i].logoUrl,
          channelNumber: i + 1,
          guideNumber: _channels[i].guideNumber,
          isHidden: _channels[i].isHidden,
          sourceChannelId: _channels[i].sourceChannelId,
          isFavorite: _channels[i].isFavorite,
        );
      }
    });

    try {
      final api = context.read<ApiService>();
      final ids = _channels.map((c) => c.id).toList();
      await api.reorderChannels(ids);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Failed to save order: $e')));
        _fetchChannels();
      }
    }
  }

  Future<void> _editTunerSettings() async {
    final nameController = TextEditingController(text: widget.playlist.name);
    final urlController = TextEditingController(text: widget.playlist.urlPath);

    final result = await showDialog<Map<String, String>>(
      context: context,
      builder: (context) => AlertDialog(
        backgroundColor: const Color(0xFF1A1A1A),
        title: const Text('Edit Tuner Settings', style: TextStyle(color: Colors.white)),
        content: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: nameController,
                style: const TextStyle(color: Colors.white),
                decoration: const InputDecoration(
                  labelText: 'Tuner Name',
                  labelStyle: TextStyle(color: Colors.white54),
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: urlController,
                style: const TextStyle(color: Colors.white),
                decoration: const InputDecoration(
                  labelText: 'URL / Path',
                  labelStyle: TextStyle(color: Colors.white54),
                  border: OutlineInputBorder(),
                ),
                enabled: !_isVirtual,
                readOnly: _isVirtual,
              ),
              if (_isVirtual)
                const Padding(
                  padding: EdgeInsets.only(top: 8),
                  child: Text(
                    'Virtual tuner URL is auto-generated',
                    style: TextStyle(color: Colors.white38, fontSize: 12),
                  ),
                ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel', style: TextStyle(color: Colors.white54)),
          ),
          ElevatedButton(
            onPressed: () {
              Navigator.pop(context, {
                'name': nameController.text.trim(),
                'url': urlController.text.trim(),
              });
            },
            child: const Text('Save'),
          ),
        ],
      ),
    );

    if (result != null && mounted) {
      final api = context.read<ApiService>();
      
      try {
        if (result['name'] != widget.playlist.name) {
          await api.updatePlaylist(
            widget.playlist.id,
            widget.playlist.urlPath,
            widget.playlist.type,
            result['name']!,
          );
        }
        
        if (!_isVirtual && result['url'] != widget.playlist.urlPath) {
          await api.updatePlaylist(
            widget.playlist.id,
            result['url']!,
            widget.playlist.type,
            result['name']!,
          );
        }
        
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Tuner settings updated!'), backgroundColor: Colors.green),
          );
        }
      } catch (e) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('Failed to update: $e'), backgroundColor: Colors.red),
          );
        }
      }
    }
  }

  Future<void> _editMetadata(Channel channel) async {
    final nameController = TextEditingController(text: channel.name);
    final guideNumController = TextEditingController(text: channel.guideNumber);
    final groupController = TextEditingController(text: channel.groupId);

    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Edit ${channel.name}'),
        content: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: nameController,
                decoration: const InputDecoration(labelText: 'Name', border: OutlineInputBorder()),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: guideNumController,
                decoration: const InputDecoration(labelText: 'Guide Number (e.g. 1.1)', border: OutlineInputBorder()),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: groupController,
                decoration: const InputDecoration(labelText: 'Category / Group', border: OutlineInputBorder()),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
          ElevatedButton(
            onPressed: () {
              Navigator.pop(context, {
                'name': nameController.text.trim(),
                'guide_number': guideNumController.text.trim(),
                'group_id': groupController.text.trim(),
              });
            },
            child: const Text('Save'),
          ),
        ],
      ),
    );

    if (result != null && mounted) {
      final api = context.read<ApiService>();
      
      try {
        await api.updateChannelMetadata(
          channel.id, 
          result['name'], 
          channel.channelNumber,
          result['guide_number'], 
          result['group_id']
        );
        await _fetchChannels();
      } catch (e) {
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Failed to update: $e')));
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
      
      try {
        await api.updateChannelLogo(channel.id, url);
        await _fetchChannels();
      } catch (e) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Failed to update logo: $e')));
        }
      }
    }
  }

  Future<void> _toggleVisibility(Channel channel, bool isVisible) async {
    final api = context.read<ApiService>();
    final newHiddenState = !isVisible;
    
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
          sourceChannelId: channel.sourceChannelId,
          isFavorite: channel.isFavorite,
        );
      }
    });

    try {
      await api.updateChannelVisibility(channel.id, newHiddenState);
    } catch (e) {
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

  Widget _buildChannelTile(Channel channel, int index) {
    return ListTile(
      key: ValueKey(channel.id),
      contentPadding: const EdgeInsets.symmetric(horizontal: 24, vertical: 8),
      leading: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (_isVirtual)
            ReorderableDragStartListener(
              index: index,
              child: const Icon(Icons.drag_handle, color: Colors.white38),
            ),
          if (_isVirtual)
            const SizedBox(width: 16),
          Text(
            '${channel.channelNumber}',
            style: const TextStyle(color: Colors.blueAccent, fontSize: 18, fontWeight: FontWeight.bold),
          ),
          const SizedBox(width: 16),
          channel.logoUrl.isNotEmpty
              ? Image.network(
                  channel.logoUrl,
                  width: 50,
                  height: 50,
                  fit: BoxFit.contain,
                  errorBuilder: (_, __, ___) => const Icon(Icons.tv, color: Colors.white54, size: 40),
                )
              : const Icon(Icons.tv, color: Colors.white54, size: 40),
        ],
      ),
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
        'Category: ${channel.groupId.isNotEmpty ? channel.groupId : 'Uncategorized'}',
        style: const TextStyle(color: Colors.white54),
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            icon: const Icon(Icons.edit, color: Colors.blueAccent),
            tooltip: 'Edit Info',
            onPressed: () => _editMetadata(channel),
          ),
          IconButton(
            icon: const Icon(Icons.image, color: Colors.blueAccent),
            tooltip: 'Edit Logo',
            onPressed: () => _editLogo(channel),
          ),
          const SizedBox(width: 8),
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

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        title: Text('Edit: ${widget.playlist.name}'),
        backgroundColor: Colors.black,
        actions: [
          IconButton(
            icon: const Icon(Icons.settings),
            tooltip: 'Tuner Settings',
            onPressed: _editTunerSettings,
          ),
        ],
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : _channels.isEmpty
              ? const Center(child: Text('No channels.', style: TextStyle(color: Colors.white54)))
              : Column(
                  children: [
                    Padding(
                      padding: const EdgeInsets.all(16.0),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            widget.playlist.name,
                            style: const TextStyle(color: Colors.white, fontSize: 20, fontWeight: FontWeight.bold),
                          ),
                          const SizedBox(height: 4),
                          Text(
                            widget.playlist.urlPath,
                            style: const TextStyle(color: Colors.white54, fontSize: 14),
                          ),
                          const SizedBox(height: 8),
                          Text(
                            _isVirtual ? 'Virtual Tuner • Drag to reorder channels' : 'External Tuner • ${_channels.length} channels',
                            style: const TextStyle(color: Colors.blueAccent, fontSize: 14),
                          ),
                        ],
                      ),
                    ),
                    const Divider(color: Colors.white24),
                    Expanded(
                      child: _isVirtual
                          ? ReorderableListView.builder(
                              buildDefaultDragHandles: false,
                              itemCount: _channels.length,
                              onReorder: _onReorder,
                              itemBuilder: (context, index) {
                                return _buildChannelTile(_channels[index], index);
                              },
                            )
                          : ListView.separated(
                              padding: const EdgeInsets.only(bottom: 24),
                              itemCount: _channels.length,
                              separatorBuilder: (_, __) => const Divider(color: Colors.white24, height: 1),
                              itemBuilder: (context, index) {
                                return _buildChannelTile(_channels[index], index);
                              },
                            ),
                    ),
                  ],
                ),
    );
  }
}
