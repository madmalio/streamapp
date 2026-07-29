import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

class AppSettings extends ChangeNotifier {
  AppSettings({
    required String initialBaseUrl,
    required String initialDefaultQuality,
    required String initialEpgUrl,
    required String initialLastChannelId,
    required String initialPreviousChannelId,
  })  : _baseUrl = initialBaseUrl,
        _defaultQuality = initialDefaultQuality,
        _epgUrl = initialEpgUrl,
        _lastChannelId = initialLastChannelId,
        _previousChannelId = initialPreviousChannelId;

  static const String apiBaseUrlKey = 'api_base_url';
  static const String defaultQualityKey = 'default_quality';
  static const String epgUrlKey = 'epg_url';
  static const String lastChannelIdKey = 'last_channel_id';
  static const String previousChannelIdKey = 'previous_channel_id';

  String _baseUrl;
  String _defaultQuality;
  String _epgUrl;
  String _lastChannelId;
  String _previousChannelId = '';

  String get baseUrl => _baseUrl;
  String get defaultQuality => _defaultQuality;
  String get epgUrl => _epgUrl;
  String get lastChannelId => _lastChannelId;
  String get previousChannelId => _previousChannelId;

  Future<void> setLastChannelId(String value) async {
    final normalized = value.trim();
    if (normalized == _lastChannelId) {
      return;
    }

    final prefs = await SharedPreferences.getInstance();
    
    // Shift the current last channel to previous
    if (_lastChannelId.isNotEmpty) {
      _previousChannelId = _lastChannelId;
      await prefs.setString(previousChannelIdKey, _previousChannelId);
    }

    await prefs.setString(lastChannelIdKey, normalized);
    _lastChannelId = normalized;
    notifyListeners();
  }

  Future<void> loadPreviousChannelId() async {
    final prefs = await SharedPreferences.getInstance();
    _previousChannelId = prefs.getString(previousChannelIdKey) ?? '';
    notifyListeners();
  }

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

  Future<void> setEpgUrl(String value) async {
    final normalized = value.trim();
    if (normalized == _epgUrl) {
      return;
    }

    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(epgUrlKey, normalized);
    _epgUrl = normalized;
    notifyListeners();
  }


}
