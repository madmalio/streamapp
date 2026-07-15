import 'dart:convert';
import 'package:http/http.dart' as http;
import '../models/channel.dart';

class ApiService {
  final String baseUrl;

  ApiService({required this.baseUrl});

  Future<List<Channel>> getChannels() async {
    final response = await http.get(Uri.parse('$baseUrl/channels'));
    if (response.statusCode == 200) {
      final List<dynamic> json = jsonDecode(response.body);
      return json.map((ch) => Channel.fromJson(ch)).toList();
    } else {
      throw Exception('Failed to load channels');
    }
  }

  Future<String> getStreamUrl(String rawUrl, {String bitrate = 'Original'}) async {
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
      // Start HLS Transcode with specific bitrate
      final response = await http.get(Uri.parse('$baseUrl/streams/start?url=${Uri.encodeComponent(rawUrl)}&bitrate=${Uri.encodeComponent(bitrate)}'));
      if (response.statusCode == 200) {
        final json = jsonDecode(response.body);
        // Ensure we connect to the absolute path for HLS chunks without duplicating /api
        final host = baseUrl.replaceAll('/api', '');
        return '$host${json['hls_url']}';
      } else {
        throw Exception('Failed to start HLS transcode');
      }
    }
  }

  Future<void> stopStream(String id) async {
    await http.get(Uri.parse('$baseUrl/streams/stop?id=$id'));
  }

  Future<void> stopAllStreams() async {
    await http.get(Uri.parse('$baseUrl/streams/stop_all'));
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

