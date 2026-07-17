import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

class AppSettings extends ChangeNotifier {
  AppSettings({
    required String initialBaseUrl,
    required String initialStreamingEngine,
    required String initialDefaultQuality,
  })  : _baseUrl = initialBaseUrl,
        _streamingEngine = initialStreamingEngine,
        _defaultQuality = initialDefaultQuality;

  static const String apiBaseUrlKey = 'api_base_url';
  static const String streamingEngineKey = 'streaming_engine';
  static const String defaultQualityKey = 'default_quality';

  String _baseUrl;
  String _streamingEngine;
  String _defaultQuality;

  String get baseUrl => _baseUrl;
  String get streamingEngine => _streamingEngine;
  String get defaultQuality => _defaultQuality;

  Future<void> setBaseUrl(String value) async {
    final normalized = value.trim();
    if (normalized.isEmpty || normalized == _baseUrl) {
      return;
    }

    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(apiBaseUrlKey, normalized);
    _baseUrl = normalized;
    notifyListeners();
  }

  Future<void> setStreamingEngine(String value) async {
    final normalized = value.trim().toLowerCase();
    if (normalized != 'ffmpeg' && normalized != 'gstreamer') {
      return;
    }
    if (normalized == _streamingEngine) {
      return;
    }

    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(streamingEngineKey, normalized);
    _streamingEngine = normalized;
    notifyListeners();
  }

  Future<void> setDefaultQuality(String value) async {
    final normalized = value.trim();
    if (normalized == _defaultQuality) {
      return;
    }

    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(defaultQualityKey, normalized);
    _defaultQuality = normalized;
    notifyListeners();
  }


}
