import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/channel.dart';
import '../models/playlist.dart';
import '../services/api_service.dart';

class VirtualTunerEditor extends StatefulWidget {
  final Playlist playlist;

  const VirtualTunerEditor({Key? key, required this.playlist}) : super(key: key);

  @override
  State<VirtualTunerEditor> createState() => _VirtualTunerEditorState();
}

class _VirtualTunerEditorState extends State<VirtualTunerEditor> {
  List<Channel> _channels = [];
  bool _isLoading = true;

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
      // Sort by channel number
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
      
      // Update local channel numbers
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
        _fetchChannels(); // Revert
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
          channel.channelNumber, // Keep existing channel number
          result['guide_number'], 
          result['group_id']
        );
        await _fetchChannels();
      } catch (e) {
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Failed to update: $e')));
      }
    }
  }

  Future<void> _deleteChannel(Channel channel) async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Delete Channel?'),
        content: Text('Are you sure you want to remove ${channel.name}?'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          ElevatedButton(
            onPressed: () => Navigator.pop(context, true),
            style: ElevatedButton.styleFrom(backgroundColor: Colors.redAccent),
            child: const Text('Delete'),
          ),
        ],
      ),
    );

    if (confirm == true && mounted) {
      setState(() {
        _channels.removeWhere((c) => c.id == channel.id);
      });
      try {
        final api = context.read<ApiService>();
        // Using visibility to hide it for MVP
        await api.updateChannelVisibility(channel.id, true); 
        await _onReorder(0, 0); // hack to re-save sequential order
      } catch (e) {
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Failed to delete: $e')));
        _fetchChannels();
      }
    }
  }

  Widget _buildChannelTile(Channel channel) {
    return ListTile(
      key: ValueKey(channel.id),
      contentPadding: const EdgeInsets.symmetric(horizontal: 24, vertical: 8),
      leading: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.drag_handle, color: Colors.white38),
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
        style: const TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
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
            icon: const Icon(Icons.delete, color: Colors.redAccent),
            tooltip: 'Remove',
            onPressed: () => _deleteChannel(channel),
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
        title: Text('Edit Tuner: ${widget.playlist.name}'),
        backgroundColor: Colors.black,
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : _channels.isEmpty
              ? const Center(child: Text('No channels.', style: TextStyle(color: Colors.white54)))
              : ReorderableListView.builder(
                  itemCount: _channels.length,
                  onReorder: _onReorder,
                  itemBuilder: (context, index) {
                    return _buildChannelTile(_channels[index]);
                  },
                ),
    );
  }
}
