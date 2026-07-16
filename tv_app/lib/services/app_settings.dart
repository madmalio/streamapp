import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

class AppSettings extends ChangeNotifier {
  AppSettings({required String initialBaseUrl}) : _baseUrl = initialBaseUrl;

  static const String apiBaseUrlKey = 'api_base_url';

  String _baseUrl;

  String get baseUrl => _baseUrl;

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
}
