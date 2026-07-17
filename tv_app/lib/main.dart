import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:media_kit/media_kit.dart';

import 'package:shared_preferences/shared_preferences.dart';
import 'screens/guide_screen.dart';
import 'services/api_service.dart';
import 'services/app_settings.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  const defaultApiBaseUrl = String.fromEnvironment(
    'STREAMAPP_API_BASE_URL',
    defaultValue: 'http://192.168.4.143:8080/api',
  );

  final prefs = await SharedPreferences.getInstance();
  final initialBaseUrl =
      prefs.getString(AppSettings.apiBaseUrlKey) ?? defaultApiBaseUrl;
  final initialStreamingEngine =
      prefs.getString(AppSettings.streamingEngineKey) ?? 'ffmpeg';
  final initialDefaultQuality =
      prefs.getString(AppSettings.defaultQualityKey) ?? 'Auto';


  MediaKit.ensureInitialized();

  runApp(
    StreamApp(
      initialBaseUrl: initialBaseUrl,
      initialStreamingEngine: initialStreamingEngine,
      initialDefaultQuality: initialDefaultQuality,
    ),
  );
}

class StreamApp extends StatelessWidget {
  const StreamApp({
    super.key,
    required this.initialBaseUrl,
    required this.initialStreamingEngine,
    required this.initialDefaultQuality,
  });

  final String initialBaseUrl;
  final String initialStreamingEngine;
  final String initialDefaultQuality;

  @override
  Widget build(BuildContext context) {
    return MultiProvider(
      providers: [
        ChangeNotifierProvider(
          create: (_) => AppSettings(
            initialBaseUrl: initialBaseUrl,
            initialStreamingEngine: initialStreamingEngine,
            initialDefaultQuality: initialDefaultQuality,
          ),
        ),
        ProxyProvider<AppSettings, ApiService>(
          update: (_, settings, previous) {
            if (previous != null && previous.baseUrl == settings.baseUrl) {
              return previous;
            }
            return ApiService(baseUrl: settings.baseUrl);
          },
        ),
      ],
      child: MaterialApp(
        title: 'StreamApp TV',
        theme: ThemeData(
          useMaterial3: true,
          brightness: Brightness.dark,
          primaryColor: Colors.blueAccent,
          scaffoldBackgroundColor: const Color(0xFF0D0D0D),
          focusColor: Colors.blue,
          fontFamily: 'Inter',
        ),
        home: const GuideScreen(),
      ),
    );
  }
}
