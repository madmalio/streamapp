import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/app_settings.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late final TextEditingController _controller;
  bool _isSaving = false;
  String _selectedEngine = 'ffmpeg';
  String _selectedQuality = 'Auto';
  String _selectedPlayer = 'media_kit';

  @override
  void initState() {
    super.initState();
    final settings = context.read<AppSettings>();
    _controller = TextEditingController(text: settings.baseUrl);
    _selectedEngine = settings.streamingEngine;
    _selectedQuality = settings.defaultQuality;
    _selectedPlayer = settings.videoPlayer;
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final input = _controller.text.trim();
    final uri = Uri.tryParse(input);
    if (uri == null || !uri.hasScheme || uri.host.isEmpty || !input.endsWith('/api')) {
      if (!mounted) {
        return;
      }
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a valid URL like http://192.168.4.143:8080/api')),
      );
      return;
    }

    setState(() => _isSaving = true);
    final settings = context.read<AppSettings>();
    await settings.setBaseUrl(input);
    await settings.setStreamingEngine(_selectedEngine);
    await settings.setDefaultQuality(_selectedQuality);
    await settings.setVideoPlayer(_selectedPlayer);
    if (!mounted) {
      return;
    }
    setState(() => _isSaving = false);
    Navigator.pop(context, true);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: const Color(0xFF0D0D0D),
      appBar: AppBar(
        title: const Text('Settings'),
        backgroundColor: const Color(0xFF1A1A1A),
      ),
      body: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              'Backend API URL',
              style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _controller,
              style: const TextStyle(color: Colors.white),
              decoration: const InputDecoration(
                hintText: 'http://192.168.4.143:8080/api',
                hintStyle: TextStyle(color: Colors.white54),
                filled: true,
                fillColor: Color(0xFF1A1A1A),
                border: OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 12),
            const Text(
              'Tip: include /api at the end.',
              style: TextStyle(color: Colors.white60),
            ),
            const SizedBox(height: 24),
            const Text(
              'Default Transcoding Engine',
              style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 12),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              decoration: BoxDecoration(
                color: const Color(0xFF1A1A1A),
                borderRadius: BorderRadius.circular(4),
                border: Border.all(color: Colors.white24),
              ),
              child: DropdownButtonHideUnderline(
                child: DropdownButton<String>(
                  value: _selectedEngine,
                  dropdownColor: const Color(0xFF1A1A1A),
                  style: const TextStyle(color: Colors.white, fontSize: 16),
                  icon: const Icon(Icons.arrow_drop_down, color: Colors.white),
                  isExpanded: true,
                  items: const [
                    DropdownMenuItem(
                      value: 'ffmpeg',
                      child: Text('FFmpeg (Resilient VAAPI)'),
                    ),
                    DropdownMenuItem(
                      value: 'gstreamer',
                      child: Text('GStreamer (Low Latency)'),
                    ),
                  ],
                  onChanged: (val) {
                    if (val != null) {
                      setState(() => _selectedEngine = val);
                    }
                  },
                ),
              ),
            ),
            const SizedBox(height: 24),
            const Text(
              'Default Playback Quality',
              style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 12),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              decoration: BoxDecoration(
                color: const Color(0xFF1A1A1A),
                borderRadius: BorderRadius.circular(4),
                border: Border.all(color: Colors.white24),
              ),
              child: DropdownButtonHideUnderline(
                child: DropdownButton<String>(
                  value: _selectedQuality,
                  dropdownColor: const Color(0xFF1A1A1A),
                  style: const TextStyle(color: Colors.white, fontSize: 16),
                  icon: const Icon(Icons.arrow_drop_down, color: Colors.white),
                  isExpanded: true,
                  items: const [
                    DropdownMenuItem(value: 'Auto', child: Text('Auto (Network Recommended)')),
                    DropdownMenuItem(value: 'Original', child: Text('Original (Direct Playback)')),
                    DropdownMenuItem(value: 'Original HLS', child: Text('Original (HLS Transmux)')),
                    DropdownMenuItem(value: '8M', child: Text('8 Mbps HLS Transcode')),
                    DropdownMenuItem(value: '4M', child: Text('4 Mbps HLS Transcode')),
                    DropdownMenuItem(value: '3M', child: Text('3 Mbps HLS Transcode')),
                    DropdownMenuItem(value: '1.5M', child: Text('1.5 Mbps HLS Transcode')),
                  ],
                  onChanged: (val) {
                    if (val != null) {
                      setState(() => _selectedQuality = val);
                    }
                  },
                ),
              ),
            ),
            const SizedBox(height: 24),
            const Text(
              'Video Player Backend',
              style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 12),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              decoration: BoxDecoration(
                color: const Color(0xFF1A1A1A),
                borderRadius: BorderRadius.circular(4),
                border: Border.all(color: Colors.white24),
              ),
              child: DropdownButtonHideUnderline(
                child: DropdownButton<String>(
                  value: _selectedPlayer,
                  dropdownColor: const Color(0xFF1A1A1A),
                  style: const TextStyle(color: Colors.white, fontSize: 16),
                  icon: const Icon(Icons.arrow_drop_down, color: Colors.white),
                  isExpanded: true,
                  items: const [
                    DropdownMenuItem(value: 'media_kit', child: Text('MediaKit (Hardware Accelerated)')),
                    DropdownMenuItem(value: 'vlc', child: Text('VLC (Robust Networking)')),
                  ],
                  onChanged: (val) {
                    if (val != null) {
                      setState(() => _selectedPlayer = val);
                    }
                  },
                ),
              ),
            ),
            const SizedBox(height: 30),
            ElevatedButton(
              onPressed: _isSaving ? null : _save,
              child: _isSaving
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Text('Save'),
            ),
          ],
        ),
      ),
    );
  }
}
