import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/app_settings.dart';
import '../services/api_service.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late final TextEditingController _controller;
  late final TextEditingController _epgController;
  late final TextEditingController _hdhrController;
  bool _isSaving = false;
  bool _isSyncingEpg = false;
  String _selectedEngine = 'ffmpeg';
  String _selectedQuality = 'Auto';

  @override
  void initState() {
    super.initState();
    final settings = context.read<AppSettings>();
    _controller = TextEditingController(text: settings.baseUrl);
    _epgController = TextEditingController(text: settings.epgUrl);
    _hdhrController = TextEditingController();
    _selectedEngine = settings.streamingEngine;
    _selectedQuality = settings.defaultQuality;
  }

  @override
  void dispose() {
    _controller.dispose();
    _epgController.dispose();
    _hdhrController.dispose();
    super.dispose();
  }

  Future<void> _syncEpg() async {
    final input = _epgController.text.trim();
    if (input.isEmpty) return;

    setState(() => _isSyncingEpg = true);
    try {
      final api = context.read<ApiService>();
      await api.syncEpg(input);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('EPG Synced Successfully!'), backgroundColor: Colors.green),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Error syncing EPG: $e'), backgroundColor: Colors.red),
        );
      }
    } finally {
      if (mounted) {
        setState(() => _isSyncingEpg = false);
      }
    }
  }

  Future<void> _addTuner() async {
    final ip = _hdhrController.text.trim();
    if (ip.isEmpty) return;

    setState(() => _isSaving = true);
    try {
      final api = context.read<ApiService>();
      await api.addPlaylist(name: 'HDHomeRun', urlPath: ip, type: 'HDHOMERUN');
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Tuner Channels Added Successfully!'), backgroundColor: Colors.green),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Error adding tuner: $e'), backgroundColor: Colors.red),
        );
      }
    } finally {
      if (mounted) {
        setState(() => _isSaving = false);
      }
    }
  }

  Future<void> _save() async {
    final input = _controller.text.trim();
    final epgInput = _epgController.text.trim();
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
    await settings.setEpgUrl(epgInput);
    await settings.setStreamingEngine(_selectedEngine);
    await settings.setDefaultQuality(_selectedQuality);
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
        child: SingleChildScrollView(
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
                'HDHomeRun Tuner IP Address',
                style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _hdhrController,
                      style: const TextStyle(color: Colors.white),
                      decoration: const InputDecoration(
                        hintText: 'e.g. 192.168.1.100',
                        hintStyle: TextStyle(color: Colors.white54),
                        filled: true,
                        fillColor: Color(0xFF1A1A1A),
                        border: OutlineInputBorder(),
                      ),
                    ),
                  ),
                  const SizedBox(width: 12),
                  ElevatedButton.icon(
                    onPressed: _isSaving ? null : _addTuner,
                    icon: _isSaving
                        ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                        : const Icon(Icons.add_to_queue),
                    label: const Text('Add Tuner'),
                    style: ElevatedButton.styleFrom(
                      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
                      backgroundColor: Colors.blueAccent,
                      foregroundColor: Colors.white,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 24),
              const Text(
                'XMLTV EPG Source URL',
                style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _epgController,
                      style: const TextStyle(color: Colors.white),
                      decoration: const InputDecoration(
                        hintText: 'http://192.168.4.143:8080/epg.xml',
                        hintStyle: TextStyle(color: Colors.white54),
                        filled: true,
                        fillColor: Color(0xFF1A1A1A),
                        border: OutlineInputBorder(),
                      ),
                    ),
                  ),
                  const SizedBox(width: 12),
                  ElevatedButton.icon(
                    onPressed: _isSyncingEpg ? null : _syncEpg,
                    icon: _isSyncingEpg
                        ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                        : const Icon(Icons.sync),
                    label: const Text('Sync EPG Now'),
                    style: ElevatedButton.styleFrom(
                      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
                      backgroundColor: Colors.blueAccent,
                      foregroundColor: Colors.white,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              ElevatedButton.icon(
                onPressed: _isSyncingEpg ? null : () async {
                  _epgController.text = 'HDHOMERUN_AUTO';
                  await _syncEpg();
                },
                icon: _isSyncingEpg
                    ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.router),
                label: const Text('Auto-Sync HDHomeRun Guide'),
                style: ElevatedButton.styleFrom(
                  padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
                  backgroundColor: Colors.deepOrangeAccent,
                  foregroundColor: Colors.white,
                ),
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
                      DropdownMenuItem(value: 'WebRTC', child: Text('WebRTC (Ultra Low Latency)')),
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
              const SizedBox(height: 40),
              ElevatedButton(
                onPressed: _isSaving ? null : _save,
                style: ElevatedButton.styleFrom(
                  minimumSize: const Size(double.infinity, 50),
                ),
                child: _isSaving
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Save Settings', style: TextStyle(fontSize: 16)),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
