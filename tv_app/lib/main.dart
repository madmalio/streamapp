import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:media_kit/media_kit.dart';
import 'screens/guide_screen.dart';
import 'screens/player_screen.dart';
import 'services/api_service.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  MediaKit.ensureInitialized();
  runApp(const StreamApp());
}

class StreamApp extends StatelessWidget {
  const StreamApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MultiProvider(
      providers: [
        Provider(create: (_) => ApiService(baseUrl: 'http://localhost:8080/api')),
      ],
      child: MaterialApp(
        title: 'StreamApp TV',
        theme: ThemeData(
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
