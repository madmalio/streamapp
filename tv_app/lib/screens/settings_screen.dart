import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/app_settings.dart';
import '../services/api_service.dart';
import '../models/playlist.dart';
import '../models/epg_source.dart';
import 'channel_management_screen.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late final TextEditingController _controller;
  late final TextEditingController _epgController;
  late final TextEditingController _epgNameController;
  late final TextEditingController _hdhrController;
  late final TextEditingController _m3uController;
  late final TextEditingController _m3uNameController;
  bool _isSaving = false;
  bool _isSyncingEpg = false;
  String _selectedQuality = 'Auto';
  List<Playlist> _tuners = [];
  bool _isLoadingTuners = true;
  List<EpgSource> _epgSources = [];
  bool _isLoadingEpgSources = true;

  @override
  void initState() {
    super.initState();
    final settings = context.read<AppSettings>();
    _controller = TextEditingController(text: settings.baseUrl);
    _epgController = TextEditingController();
    _epgNameController = TextEditingController();
    _hdhrController = TextEditingController();
    _m3uController = TextEditingController();
    _m3uNameController = TextEditingController();
    _selectedQuality = settings.defaultQuality;
    _loadTuners();
    _loadEpgSources();
  }

  Future<void> _loadEpgSources() async {
    try {
      final api = context.read<ApiService>();
      final sources = await api.getEpgSources();
      if (mounted) {
        setState(() {
          _epgSources = sources;
          _isLoadingEpgSources = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() => _isLoadingEpgSources = false);
      }
    }
  }

  Future<void> _loadTuners() async {
    try {
      final api = context.read<ApiService>();
      final playlists = await api.getPlaylists();
      if (mounted) {
        setState(() {
          _tuners = playlists;
          _isLoadingTuners = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() => _isLoadingTuners = false);
      }
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    _epgController.dispose();
    _epgNameController.dispose();
    _hdhrController.dispose();
    _m3uController.dispose();
    _m3uNameController.dispose();
    super.dispose();
  }

  Future<void> _syncEpg(String input) async {
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

  Future<void> _editTuner(Playlist tuner) async {
    final controller = TextEditingController(text: tuner.urlPath);
    final newUrl = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: const Color(0xFF1A1A1A),
        title: const Text('Edit Tuner', style: TextStyle(color: Colors.white)),
        content: TextField(
          controller: controller,
          style: const TextStyle(color: Colors.white),
          decoration: const InputDecoration(
            labelText: 'Tuner IP Address',
            labelStyle: TextStyle(color: Colors.white54),
            filled: true,
            fillColor: Color(0xFF0D0D0D),
            border: OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel', style: TextStyle(color: Colors.white54)),
          ),
          ElevatedButton(
            onPressed: () => Navigator.pop(ctx, controller.text),
            child: const Text('Save'),
          ),
        ],
      ),
    );

    if (newUrl != null && newUrl.trim().isNotEmpty && newUrl.trim() != tuner.urlPath) {
      setState(() => _isSaving = true);
      try {
        await context.read<ApiService>().updatePlaylist(tuner.id, newUrl.trim(), tuner.type, tuner.name);
        await _loadTuners();
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Tuner updated!'), backgroundColor: Colors.green));
        }
      } catch (e) {
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error: $e'), backgroundColor: Colors.red));
      } finally {
        if (mounted) setState(() => _isSaving = false);
      }
    }
  }

  Future<void> _syncTuner(Playlist tuner) async {
    setState(() => _isSaving = true);
    try {
      await context.read<ApiService>().syncPlaylist(tuner.id);
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('${tuner.name} synced!'), backgroundColor: Colors.green));
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error syncing ${tuner.name}: $e'), backgroundColor: Colors.red));
    } finally {
      if (mounted) setState(() => _isSaving = false);
    }
  }

  Future<void> _deleteTuner(Playlist tuner) async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: const Color(0xFF1A1A1A),
        title: const Text('Delete Tuner?', style: TextStyle(color: Colors.white)),
        content: const Text('Are you sure you want to delete this tuner? All associated channels will be removed.', style: TextStyle(color: Colors.white70)),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel', style: TextStyle(color: Colors.white54)),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Delete', style: TextStyle(color: Colors.white)),
          ),
        ],
      ),
    );

    if (confirm == true) {
      setState(() => _isSaving = true);
      try {
        await context.read<ApiService>().deletePlaylist(tuner.id);
        await _loadTuners();
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Tuner deleted!'), backgroundColor: Colors.green));
        }
      } catch (e) {
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error: $e'), backgroundColor: Colors.red));
      } finally {
        if (mounted) setState(() => _isSaving = false);
      }
    }
  }

  Future<void> _addEpgSource(String name, String url) async {
    if (name.isEmpty || url.isEmpty) return;
    setState(() => _isSaving = true);
    try {
      await context.read<ApiService>().addEpgSource(name: name, url: url);
      await _loadEpgSources();
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('EPG Source Added!'), backgroundColor: Colors.green));
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error: $e'), backgroundColor: Colors.red));
    } finally {
      if (mounted) setState(() => _isSaving = false);
    }
  }

  Future<void> _editEpgSource(EpgSource source) async {
    final nameController = TextEditingController(text: source.name);
    final urlController = TextEditingController(text: source.url);
    final result = await showDialog<Map<String, String>>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: const Color(0xFF1A1A1A),
        title: const Text('Edit EPG Source', style: TextStyle(color: Colors.white)),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              controller: nameController,
              style: const TextStyle(color: Colors.white),
              decoration: const InputDecoration(labelText: 'Name', labelStyle: TextStyle(color: Colors.white54), filled: true, fillColor: Color(0xFF0D0D0D), border: OutlineInputBorder()),
            ),
            const SizedBox(height: 16),
            TextField(
              controller: urlController,
              style: const TextStyle(color: Colors.white),
              decoration: const InputDecoration(labelText: 'URL', labelStyle: TextStyle(color: Colors.white54), filled: true, fillColor: Color(0xFF0D0D0D), border: OutlineInputBorder()),
            ),
          ],
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel', style: TextStyle(color: Colors.white54))),
          ElevatedButton(onPressed: () => Navigator.pop(ctx, {'name': nameController.text, 'url': urlController.text}), child: const Text('Save')),
        ],
      ),
    );

    if (result != null && result['name']!.trim().isNotEmpty && result['url']!.trim().isNotEmpty) {
      setState(() => _isSaving = true);
      try {
        await context.read<ApiService>().updateEpgSource(source.id, result['name']!.trim(), result['url']!.trim());
        await _loadEpgSources();
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('EPG Source updated!'), backgroundColor: Colors.green));
      } catch (e) {
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error: $e'), backgroundColor: Colors.red));
      } finally {
        if (mounted) setState(() => _isSaving = false);
      }
    }
  }

  Future<void> _deleteEpgSource(EpgSource source) async {
    final confirm = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: const Color(0xFF1A1A1A),
        title: const Text('Delete EPG Source?', style: TextStyle(color: Colors.white)),
        content: const Text('Are you sure you want to delete this EPG source?', style: TextStyle(color: Colors.white70)),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Cancel', style: TextStyle(color: Colors.white54))),
          ElevatedButton(style: ElevatedButton.styleFrom(backgroundColor: Colors.red), onPressed: () => Navigator.pop(ctx, true), child: const Text('Delete', style: TextStyle(color: Colors.white))),
        ],
      ),
    );

    if (confirm == true) {
      setState(() => _isSaving = true);
      try {
        await context.read<ApiService>().deleteEpgSource(source.id);
        await _loadEpgSources();
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('EPG Source deleted!'), backgroundColor: Colors.green));
      } catch (e) {
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error: $e'), backgroundColor: Colors.red));
      } finally {
        if (mounted) setState(() => _isSaving = false);
      }
    }
  }

  Future<void> _syncEpgSource(EpgSource source) async {
    setState(() => _isSyncingEpg = true);
    try {
      await context.read<ApiService>().syncEpgSource(source.id);
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('${source.name} synced!'), backgroundColor: Colors.green));
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error: $e'), backgroundColor: Colors.red));
    } finally {
      if (mounted) setState(() => _isSyncingEpg = false);
    }
  }

  Future<void> _addTuner() async {
    final ip = _hdhrController.text.trim();
    if (ip.isEmpty) return;

    setState(() => _isSaving = true);
    try {
      final api = context.read<ApiService>();
      await api.addPlaylist(name: 'HDHomeRun', urlPath: ip, type: 'HDHOMERUN');
      await _loadTuners(); // reload list
      _hdhrController.clear();
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

  Future<void> _addM3uTuner() async {
    final name = _m3uNameController.text.trim();
    final url = _m3uController.text.trim();
    if (name.isEmpty || url.isEmpty) return;

    setState(() => _isSaving = true);
    try {
      final api = context.read<ApiService>();
      await api.addPlaylist(name: name, urlPath: url, type: 'M3U');
      await _loadTuners(); // reload list
      _m3uNameController.clear();
      _m3uController.clear();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('M3U Playlist Added Successfully!'), backgroundColor: Colors.green),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Error adding M3U playlist: $e'), backgroundColor: Colors.red),
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
    await settings.setDefaultQuality(_selectedQuality);
    if (!mounted) {
      return;
    }
    setState(() => _isSaving = false);
    Navigator.pop(context, true);
  }

  @override
  Widget build(BuildContext context) {
    return DefaultTabController(
      length: 3,
      child: Scaffold(
        backgroundColor: const Color(0xFF0D0D0D),
        appBar: AppBar(
          title: const Text('Settings'),
          backgroundColor: const Color(0xFF1A1A1A),
          bottom: const TabBar(
            indicatorColor: Colors.blueAccent,
            labelColor: Colors.blueAccent,
            unselectedLabelColor: Colors.white54,
            tabs: [
              Tab(icon: Icon(Icons.dns), text: 'Server & Tuners'),
              Tab(icon: Icon(Icons.tv), text: 'Guide & Channels'),
              Tab(icon: Icon(Icons.play_circle_filled), text: 'Playback'),
            ],
          ),
        ),
        bottomNavigationBar: SafeArea(
          child: Padding(
            padding: const EdgeInsets.all(24.0),
            child: ElevatedButton(
              onPressed: _isSaving ? null : _save,
              style: ElevatedButton.styleFrom(
                minimumSize: const Size(double.infinity, 50),
                backgroundColor: Colors.blueAccent,
                foregroundColor: Colors.white,
              ),
              child: _isSaving
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                    )
                  : const Text('Save Settings', style: TextStyle(fontSize: 16, fontWeight: FontWeight.bold)),
            ),
          ),
        ),
        body: TabBarView(
          children: [
            // Tab 1: Server & Tuners
            SingleChildScrollView(
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
                  const SizedBox(height: 32),
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
                  const SizedBox(height: 32),
                  const Text(
                    'Add M3U Tuner Playlist',
                    style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      Expanded(
                        flex: 1,
                        child: TextField(
                          controller: _m3uNameController,
                          style: const TextStyle(color: Colors.white),
                          decoration: const InputDecoration(
                            labelText: 'Tuner Name',
                            labelStyle: TextStyle(color: Colors.white54),
                            filled: true,
                            fillColor: Color(0xFF1A1A1A),
                            border: OutlineInputBorder(),
                          ),
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        flex: 2,
                        child: TextField(
                          controller: _m3uController,
                          style: const TextStyle(color: Colors.white),
                          decoration: const InputDecoration(
                            labelText: 'M3U Playlist URL',
                            labelStyle: TextStyle(color: Colors.white54),
                            filled: true,
                            fillColor: Color(0xFF1A1A1A),
                            border: OutlineInputBorder(),
                          ),
                        ),
                      ),
                      const SizedBox(width: 12),
                      ElevatedButton.icon(
                        onPressed: _isSaving ? null : _addM3uTuner,
                        icon: _isSaving
                            ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                            : const Icon(Icons.playlist_add),
                        label: const Text('Add M3U'),
                        style: ElevatedButton.styleFrom(
                          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
                          backgroundColor: Colors.blueAccent,
                          foregroundColor: Colors.white,
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 32),
                  const Divider(color: Colors.white24),
                  const SizedBox(height: 16),
                  if (_isLoadingTuners)
                    const Padding(
                      padding: EdgeInsets.all(24.0),
                      child: Center(child: CircularProgressIndicator()),
                    )
                  else if (_tuners.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 24.0),
                      child: ListView.builder(
                        shrinkWrap: true,
                        physics: const NeverScrollableScrollPhysics(),
                        itemCount: _tuners.length,
                        itemBuilder: (ctx, i) {
                          final t = _tuners[i];
                          return ListTile(
                            contentPadding: EdgeInsets.zero,
                            leading: const Icon(Icons.router, color: Colors.blueAccent),
                            title: Text(t.name, style: const TextStyle(color: Colors.white)),
                            subtitle: Text(t.urlPath, style: const TextStyle(color: Colors.white54)),
                            trailing: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                IconButton(
                                  icon: const Icon(Icons.sync, color: Colors.blueAccent),
                                  onPressed: () => _syncTuner(t),
                                ),
                                IconButton(
                                  icon: const Icon(Icons.edit, color: Colors.white70),
                                  onPressed: () => _editTuner(t),
                                ),
                                IconButton(
                                  icon: const Icon(Icons.delete, color: Colors.redAccent),
                                  onPressed: () => _deleteTuner(t),
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
            
            // Tab 2: Guide & Channels
            SingleChildScrollView(
              padding: const EdgeInsets.all(24),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text(
                    'Add EPG Source',
                    style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      Expanded(
                        flex: 1,
                        child: TextField(
                          controller: _epgNameController,
                          style: const TextStyle(color: Colors.white),
                          decoration: const InputDecoration(
                            labelText: 'Source Name',
                            labelStyle: TextStyle(color: Colors.white54),
                            filled: true,
                            fillColor: Color(0xFF1A1A1A),
                            border: OutlineInputBorder(),
                          ),
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        flex: 2,
                        child: TextField(
                          controller: _epgController,
                          style: const TextStyle(color: Colors.white),
                          decoration: const InputDecoration(
                            labelText: 'XMLTV URL',
                            labelStyle: TextStyle(color: Colors.white54),
                            filled: true,
                            fillColor: Color(0xFF1A1A1A),
                            border: OutlineInputBorder(),
                          ),
                        ),
                      ),
                      const SizedBox(width: 12),
                      ElevatedButton.icon(
                        onPressed: _isSaving ? null : () {
                          _addEpgSource(_epgNameController.text.trim(), _epgController.text.trim());
                          _epgNameController.clear();
                          _epgController.clear();
                        },
                        icon: const Icon(Icons.add),
                        label: const Text('Add Source'),
                        style: ElevatedButton.styleFrom(
                          padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
                          backgroundColor: Colors.blueAccent,
                          foregroundColor: Colors.white,
                        ),
                      ),
                    ],
                  ),
                  if (_isLoadingEpgSources)
                    const Padding(
                      padding: EdgeInsets.all(24.0),
                      child: Center(child: CircularProgressIndicator()),
                    )
                  else if (_epgSources.isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(top: 24.0),
                      child: ListView.builder(
                        shrinkWrap: true,
                        physics: const NeverScrollableScrollPhysics(),
                        itemCount: _epgSources.length,
                        itemBuilder: (ctx, i) {
                          final s = _epgSources[i];
                          return ListTile(
                            contentPadding: EdgeInsets.zero,
                            leading: const Icon(Icons.tv, color: Colors.blueAccent),
                            title: Text(s.name, style: const TextStyle(color: Colors.white)),
                            subtitle: Text(s.url, style: const TextStyle(color: Colors.white54)),
                            trailing: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                IconButton(
                                  icon: const Icon(Icons.sync, color: Colors.blueAccent),
                                  onPressed: _isSyncingEpg ? null : () => _syncEpgSource(s),
                                ),
                                IconButton(
                                  icon: const Icon(Icons.edit, color: Colors.white70),
                                  onPressed: () => _editEpgSource(s),
                                ),
                                IconButton(
                                  icon: const Icon(Icons.delete, color: Colors.redAccent),
                                  onPressed: () => _deleteEpgSource(s),
                                ),
                              ],
                            ),
                          );
                        },
                      ),
                    ),
                  if (!_epgSources.any((s) => s.url == 'HDHOMERUN_AUTO')) ...[
                    const SizedBox(height: 12),
                    ElevatedButton.icon(
                      onPressed: _isSaving ? null : () async {
                        await _addEpgSource('HDHomeRun Built-in Guide', 'HDHOMERUN_AUTO');
                        final source = _epgSources.firstWhere((s) => s.url == 'HDHOMERUN_AUTO');
                        await _syncEpgSource(source);
                      },
                      icon: _isSaving
                          ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                          : const Icon(Icons.router),
                      label: const Text('Add & Sync HDHomeRun Guide'),
                      style: ElevatedButton.styleFrom(
                        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
                        backgroundColor: Colors.deepOrangeAccent,
                        foregroundColor: Colors.white,
                      ),
                    ),
                  ],
                  const SizedBox(height: 32),
                  const Text(
                    'Channel Management',
                    style: TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w600),
                  ),
                  const SizedBox(height: 12),
                  ElevatedButton.icon(
                    onPressed: () {
                      Navigator.push(
                        context,
                        MaterialPageRoute(builder: (context) => const ChannelManagementScreen()),
                      );
                    },
                    icon: const Icon(Icons.list_alt),
                    label: const Text('Manage Channels & Logos'),
                    style: ElevatedButton.styleFrom(
                      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 18),
                      backgroundColor: Colors.purpleAccent,
                      foregroundColor: Colors.white,
                    ),
                  ),
                ],
              ),
            ),
            
            // Tab 3: Playback
            SingleChildScrollView(
              padding: const EdgeInsets.all(24),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
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
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
