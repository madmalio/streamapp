import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../models/channel.dart';
import '../services/api_service.dart';

class ChannelManagementScreen extends StatefulWidget {
  const ChannelManagementScreen({super.key});

  @override
  State<ChannelManagementScreen> createState() => _ChannelManagementScreenState();
}

class _ChannelManagementScreenState extends State<ChannelManagementScreen> {
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
      final channels = await api.getChannels();
      setState(() {
        _channels = channels;
        _isLoading = false;
      });
    } catch (e) {
      if (mounted) {
        setState(() => _isLoading = false);
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error loading channels: $e')));
      }
    }
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
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        title: const Text('Manage Channels'),
        backgroundColor: Colors.black,
        elevation: 0,
      ),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : ListView.separated(
              itemCount: _channels.length,
              separatorBuilder: (context, index) => const Divider(color: Colors.white24, height: 1),
              itemBuilder: (context, index) {
                final channel = _channels[index];
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
              },
            ),
    );
  }
}
