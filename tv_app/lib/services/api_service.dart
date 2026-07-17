import 'dart:convert';
import 'package:http/http.dart' as http;
import '../models/channel.dart';

class HlsStreamSession {
  final String url;
  final String sessionId;

  HlsStreamSession({required this.url, required this.sessionId});
}

class ApiService {
  final String baseUrl;
  DateTime? _lastRecommendationAt;
  String? _lastRecommendedBitrate;

  static const Duration _recommendationTtl = Duration(minutes: 10);

  ApiService({required this.baseUrl});

  Future<void> primeAutoRecommendation() async {
    await getRecommendedBitrate(forceRefresh: true, fallbackOnUnknown: true);
  }

  Future<String> getRecommendedBitrate({
    bool forceRefresh = false,
    bool fallbackOnUnknown = true,
  }) async {
    final now = DateTime.now();
    final cacheFresh = _lastRecommendationAt != null && now.difference(_lastRecommendationAt!) < _recommendationTtl;
    if (!forceRefresh && cacheFresh && _lastRecommendedBitrate != null) {
      return _lastRecommendedBitrate!;
    }

    final speedMbps = await runSpeedTest();
    String? recommended;

    if (speedMbps > 12.0) {
      recommended = '8M';
    } else if (speedMbps > 6.0) {
      recommended = '4M';
    } else if (speedMbps > 4.0) {
      recommended = '3M';
    } else if (speedMbps > 0.0) {
      recommended = '1.5M';
    }

    if (recommended == null) {
      if (!fallbackOnUnknown) {
        return _lastRecommendedBitrate ?? '3M';
      }
      recommended = _lastRecommendedBitrate ?? '3M';
    }

    _lastRecommendedBitrate = recommended;
    _lastRecommendationAt = now;
    return recommended;
  }

  Future<List<Channel>> getChannels() async {
    final response = await http.get(Uri.parse('$baseUrl/channels'));
    if (response.statusCode == 200) {
      final List<dynamic> json = jsonDecode(response.body);
      return json.map((ch) => Channel.fromJson(ch)).toList();
    } else {
      throw Exception('Failed to load channels');
    }
  }

  Future<String> getStreamUrl(String rawUrl, {String bitrate = 'Original', String? engine}) async {
    if (bitrate == 'Original') {
      // Fetch the raw stream URL
      final response = await http.get(Uri.parse('$baseUrl/streams/play?url=${Uri.encodeComponent(rawUrl)}'));
      if (response.statusCode == 200) {
        final json = jsonDecode(response.body);
        return json['stream_url'];
      } else {
        throw Exception('Failed to get raw stream url');
      }
    } else {
      final session = await startHlsStream(rawUrl, bitrate: bitrate, engine: engine);
      return session.url;
    }
  }

  Future<HlsStreamSession> startHlsStream(
    String rawUrl, {
    required String bitrate,
    bool fast = false,
    bool prewarm = false,
    bool transmux = false,
    String? engine,
  }) async {
    final fastParam = fast ? '&fast=1' : '';
    final prewarmParam = prewarm ? '&prewarm=1' : '';
    final transmuxParam = transmux ? '&transmux=1' : '';
    final engineParam = engine != null && engine.isNotEmpty ? '&engine=${Uri.encodeComponent(engine)}' : '';
    final response = await http.get(
      Uri.parse(
        '$baseUrl/streams/start?url=${Uri.encodeComponent(rawUrl)}&bitrate=${Uri.encodeComponent(bitrate)}$fastParam$prewarmParam$transmuxParam$engineParam',
      ),
    ).timeout(const Duration(seconds: 35));

    if (response.statusCode != 200) {
      throw Exception('Failed to start HLS transcode');
    }

    final json = jsonDecode(response.body);
    final absoluteUrl = json['hls_url'] as String?;
    if (absoluteUrl == null || absoluteUrl.isEmpty) {
      throw Exception('Missing hls_url in response');
    }

    // URL is like: http://<ip>:8888/hls_<id>/index.m3u8
    final match = RegExp(r'/hls_([^/]+)/index\.m3u8').firstMatch(absoluteUrl);
    final sessionId = match?.group(1);

    if (sessionId == null || sessionId.isEmpty) {
      throw Exception('Unable to parse HLS session ID');
    }

    return HlsStreamSession(url: absoluteUrl, sessionId: sessionId);
  }

  Future<String> startSrtStream(String rawUrl) async {
    final response = await http.get(Uri.parse('$baseUrl/streams/start_srt?url=${Uri.encodeComponent(rawUrl)}'));
    if (response.statusCode == 200) {
      final json = jsonDecode(response.body);
      return json['srt_url'];
    } else {
      throw Exception('Failed to start SRT stream');
    }
  }



  Future<HlsStreamSession?> prewarmHlsStream(String rawUrl, {String? bitrate, String? engine}) async {
    try {
      final targetBitrate = bitrate ?? await getRecommendedBitrate(forceRefresh: false, fallbackOnUnknown: true);
      return await startHlsStream(rawUrl, bitrate: targetBitrate, fast: true, prewarm: true, engine: engine);
    } catch (_) {
      // Best-effort prewarm: do not surface failures to UI.
      return null;
    }
  }

  Future<void> stopStream(String id) async {
    try {
      await http.get(Uri.parse('$baseUrl/streams/stop?id=$id')).timeout(const Duration(seconds: 2));
    } catch (_) {}
  }

  Future<void> stopAllStreams() async {
    try {
      await http.get(Uri.parse('$baseUrl/streams/stop_all')).timeout(const Duration(seconds: 2));
    } catch (_) {}
  }

  Future<double> runSpeedTest() async {
    final startTime = DateTime.now();
    try {
      final response = await http.get(Uri.parse('$baseUrl/speedtest')).timeout(const Duration(seconds: 10));
      if (response.statusCode == 200) {
        final endTime = DateTime.now();
        final duration = endTime.difference(startTime).inMilliseconds;
        final bytes = response.bodyBytes.length;
        
        if (duration > 0 && bytes > 0) {
          // Calculate Mbps: (bytes * 8 bits) / (duration in seconds * 1,000,000)
          final bits = bytes * 8;
          final seconds = duration / 1000.0;
          final mbps = bits / seconds / 1000000.0;
          return mbps;
        }
      }
    } catch (e) {
      // Speed test failed, return 0 to fallback to lowest quality
      return 0.0;
    }
    return 0.0;
  }
}
