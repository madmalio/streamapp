import 'dart:convert';
import 'package:http/http.dart' as http;
import '../models/channel.dart';
import '../models/epg_program.dart';
import '../models/playlist.dart';
import '../models/epg_source.dart';

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
      
      // Debug: Check first few channels for is_favorite field
      if (json.isNotEmpty) {
        print('📡 DEBUG: Backend returned ${json.length} channels');
        for (int i = 0; i < json.length && i < 5; i++) {
          final ch = json[i];
          print('  Channel ${i+1}: ${ch['name']} - is_favorite: ${ch['is_favorite']} (${ch['is_favorite'].runtimeType})');
        }
      }
      
      return json.map((ch) => Channel.fromJson(ch)).toList();
    } else {
      throw Exception('Failed to load channels');
    }
  }

  Future<void> reorderChannels(List<String> channelIds) async {
    final response = await http.put(
      Uri.parse('$baseUrl/channels/reorder'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({
        'channel_ids': channelIds,
      }),
    ).timeout(const Duration(seconds: 15));

    if (response.statusCode != 200) {
      throw Exception('Failed to reorder channels: ${response.body}');
    }
  }

  Future<void> updateChannelMetadata(String channelId, String name, int channelNumber, String guideNumber, String groupId) async {
    final response = await http.put(
      Uri.parse('$baseUrl/channels/$channelId/metadata'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({
        'name': name,
        'channel_number': channelNumber,
        'guide_number': guideNumber,
        'group_id': groupId,
      }),
    ).timeout(const Duration(seconds: 15));

    if (response.statusCode != 200) {
      throw Exception('Failed to update metadata: ${response.body}');
    }
  }

  Future<void> updateChannelLogo(String channelId, String logoUrl) async {
    final response = await http.put(
      Uri.parse('$baseUrl/channels/$channelId/logo'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'logo_url': logoUrl}),
    ).timeout(const Duration(seconds: 10));

    if (response.statusCode != 200) {
      throw Exception('Failed to update channel logo: ${response.body}');
    }
  }

  Future<void> updateChannelVisibility(String channelId, bool isHidden) async {
    final response = await http.put(
      Uri.parse('$baseUrl/channels/$channelId/visibility'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'is_hidden': isHidden}),
    ).timeout(const Duration(seconds: 10));

    if (response.statusCode != 200) {
      throw Exception('Failed to update channel visibility: ${response.body}');
    }
  }

  Future<void> updateChannelFavorite(String channelId, bool isFavorite) async {
    final response = await http.put(
      Uri.parse('$baseUrl/channels/$channelId/favorite'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'is_favorite': isFavorite}),
    ).timeout(const Duration(seconds: 10));

    if (response.statusCode != 200) {
      throw Exception('Failed to update channel favorite: ${response.body}');
    }
  }

  Future<void> addPlaylist({required String name, required String urlPath, required String type}) async {
    final response = await http.post(
      Uri.parse('$baseUrl/playlists'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({
        'name': name,
        'url_path': urlPath,
        'type': type,
      }),
    ).timeout(const Duration(seconds: 30));

    if (response.statusCode != 201 && response.statusCode != 200) {
      throw Exception('Failed to add playlist: ${response.body}');
    }
  }

  Future<void> generateVirtualTuner(String name, List<String> selectedChannels) async {
    final response = await http.post(
      Uri.parse('$baseUrl/virtual-tuners/generate'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({
        'name': name,
        'selected_channels': selectedChannels,
      }),
    ).timeout(const Duration(seconds: 30));

    if (response.statusCode != 201 && response.statusCode != 200) {
      throw Exception('Failed to create virtual tuner: ${response.body}');
    }
  }

  Future<Map<String, dynamic>> createVirtualTunerFromFavorites({String? name}) async {
    final body = <String, dynamic>{};
    if (name != null && name.isNotEmpty) {
      body['name'] = name;
    }

    final response = await http.post(
      Uri.parse('$baseUrl/virtual-tuners/from-favorites'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode(body),
    ).timeout(const Duration(seconds: 30));

    if (response.statusCode != 201 && response.statusCode != 200) {
      throw Exception('Failed to create virtual tuner from favorites: ${response.body}');
    }

    return jsonDecode(response.body);
  }

  Future<void> syncEpg(String epgUrl) async {
    final response = await http.post(
      Uri.parse('$baseUrl/epg/sync'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'url': epgUrl}),
    ).timeout(const Duration(seconds: 30));

    if (response.statusCode != 200) {
      throw Exception('Failed to sync EPG: ${response.body}');
    }
  }

  Future<Map<String, ChannelEPG>> getLiveEpg() async {
    final response = await http.get(Uri.parse('$baseUrl/epg/live')).timeout(const Duration(seconds: 10));
    if (response.statusCode == 200) {
      final Map<String, dynamic> json = jsonDecode(response.body);
      return json.map((key, value) => MapEntry(key, ChannelEPG.fromJson(value)));
    } else {
      throw Exception('Failed to load live EPG: ${response.body}');
    }
  }

  Future<EPGProgram?> getCurrentProgram(String channelId) async {
    try {
      final response = await http.get(Uri.parse('$baseUrl/epg/current/$channelId')).timeout(const Duration(seconds: 5));
      if (response.statusCode == 200) {
        final Map<String, dynamic> json = jsonDecode(response.body);
        return EPGProgram.fromJson(json);
      }
    } catch (_) {}
    return null;
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
      final session = await startHlsStream(rawUrl, bitrate: bitrate);
      return session.url;
    }
  }

  Future<HlsStreamSession> startHlsStream(
    String rawUrl, {
    required String bitrate,
    bool fast = false,
    bool prewarm = false,
    bool transmux = false,
  }) async {
    final fastParam = fast ? '&fast=1' : '';
    final prewarmParam = prewarm ? '&prewarm=1' : '';
    final transmuxParam = transmux ? '&transmux=1' : '';
    final response = await http.get(
      Uri.parse(
        '$baseUrl/streams/start?url=${Uri.encodeComponent(rawUrl)}&bitrate=${Uri.encodeComponent(bitrate)}$fastParam$prewarmParam$transmuxParam',
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



  Future<HlsStreamSession?> prewarmHlsStream(String rawUrl, {String? bitrate}) async {
    try {
      final targetBitrate = bitrate ?? await getRecommendedBitrate(forceRefresh: false, fallbackOnUnknown: true);
      return await startHlsStream(rawUrl, bitrate: targetBitrate, fast: true, prewarm: true);
    } catch (_) {
      // Best-effort prewarm: do not surface failures to UI.
      return null;
    }
  }

  Future<void> stopStream(String id) async {
    try {
      await http.get(Uri.parse('$baseUrl/streams/stop?id=$id')).timeout(const Duration(seconds: 2));
    } catch (e) {
      print('Failed to stop stream $id: $e');
    }
  }

  Future<void> sendHeartbeat(String id) async {
    try {
      await http.post(Uri.parse('$baseUrl/streams/heartbeat/$id')).timeout(const Duration(seconds: 2));
    } catch (e) {
      print('Failed to send heartbeat for stream $id: $e');
    }
  }

  Future<void> stopAllStreams() async {
    try {
      await http.get(Uri.parse('$baseUrl/streams/stop_all')).timeout(const Duration(seconds: 2));
    } catch (_) {}
  }

  Future<List<Playlist>> getPlaylists() async {
    final response = await http.get(Uri.parse('$baseUrl/playlists'));
    if (response.statusCode == 200) {
      final List<dynamic> json = jsonDecode(response.body);
      return json.map((p) => Playlist.fromJson(p)).toList();
    }
    throw Exception('Failed to load playlists');
  }

  Future<void> syncPlaylist(String id) async {
    final response = await http.post(
      Uri.parse('$baseUrl/playlists/$id/sync'),
    ).timeout(const Duration(minutes: 2));

    if (response.statusCode != 200) {
      throw Exception('Failed to sync playlist: ${response.body}');
    }
  }

  Future<void> updatePlaylist(String id, String urlPath, String type, String name) async {
    final response = await http.put(
      Uri.parse('$baseUrl/playlists/$id'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({
        'url_path': urlPath,
        'type': type,
        'name': name,
      }),
    ).timeout(const Duration(seconds: 30));

    if (response.statusCode != 200) {
      throw Exception('Failed to update playlist: ${response.body}');
    }
  }

  Future<void> deletePlaylist(String id) async {
    final response = await http.delete(Uri.parse('$baseUrl/playlists/$id'));
    if (response.statusCode != 200) {
      throw Exception('Failed to delete playlist: ${response.body}');
    }
  }

  Future<List<EpgSource>> getEpgSources() async {
    final response = await http.get(Uri.parse('$baseUrl/epg/sources'));
    if (response.statusCode == 200) {
      final List<dynamic> json = jsonDecode(response.body);
      return json.map((s) => EpgSource.fromJson(s)).toList();
    }
    throw Exception('Failed to load EPG sources');
  }

  Future<void> addEpgSource({required String name, required String url}) async {
    final response = await http.post(
      Uri.parse('$baseUrl/epg/sources'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'name': name, 'url': url}),
    ).timeout(const Duration(seconds: 10));
    if (response.statusCode != 201) {
      throw Exception('Failed to add EPG source: ${response.body}');
    }
  }

  Future<void> updateEpgSource(String id, String name, String url) async {
    final response = await http.put(
      Uri.parse('$baseUrl/epg/sources/$id'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'name': name, 'url': url}),
    ).timeout(const Duration(seconds: 10));
    if (response.statusCode != 200) {
      throw Exception('Failed to update EPG source: ${response.body}');
    }
  }

  Future<void> deleteEpgSource(String id) async {
    final response = await http.delete(Uri.parse('$baseUrl/epg/sources/$id'));
    if (response.statusCode != 200) {
      throw Exception('Failed to delete EPG source: ${response.body}');
    }
  }

  Future<void> syncEpgSource(String id) async {
    final response = await http.post(Uri.parse('$baseUrl/epg/sources/$id/sync'))
        .timeout(const Duration(seconds: 180)); // Sync can take a while
    if (response.statusCode != 200) {
      throw Exception('Failed to sync EPG source: ${response.body}');
    }
  }

  Future<double> runSpeedTest() async {
    final startTime = DateTime.now();
    try {
      final response = await http.get(Uri.parse('$baseUrl/speedtest')).timeout(const Duration(seconds: 3));
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
