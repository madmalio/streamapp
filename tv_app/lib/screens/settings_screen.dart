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

  @override
  void initState() {
    super.initState();
    final settings = context.read<AppSettings>();
    _controller = TextEditingController(text: settings.baseUrl);
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
    await context.read<AppSettings>().setBaseUrl(input);
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
            const SizedBox(height: 20),
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
