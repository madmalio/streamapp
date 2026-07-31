class Channel {
  final String id;
  final String playlistId;
  final String groupId;
  final String name;
  final String streamUrl;
  final String logoUrl;
  final int channelNumber;
  final String guideNumber;
  final bool isHidden;
  final String? sourceChannelId;
  bool isFavorite;

  Channel({
    required this.id,
    required this.playlistId,
    required this.groupId,
    required this.name,
    required this.streamUrl,
    required this.logoUrl,
    required this.channelNumber,
    required this.guideNumber,
    this.isHidden = false,
    this.sourceChannelId,
    this.isFavorite = false,
  });

  String get normalizedCategory {
    if (groupId.isEmpty) return '';
    final g = groupId.toLowerCase();
    
    if (g.contains('movie')) return 'Movies';
    if (g.contains('news') || g.contains('weather')) return 'News';
    if (g.contains('sport') || g.contains('espn') || g.contains('nfl') || g.contains('mlb') || g.contains('nhl') || g.contains('nba')) return 'Sports';
    if (g.contains('kid') || g.contains('cartoon') || g.contains('animation') || g.contains('family') || g.contains('children')) return 'Kids';
    if (g.contains('music') || g.contains('mtv') || g.contains('vh1')) return 'Music';
    if (g.contains('comedy') || g.contains('laugh')) return 'Comedy';
    if (g.contains('documentary') || g.contains('nature') || g.contains('science') || g.contains('history') || g.contains('explore')) return 'Documentary';
    if (g.contains('crime') || g.contains('mystery') || g.contains('investigation')) return 'Crime & Mystery';
    if (g.contains('reality') || g.contains('drama') || g.contains('action') || g.contains('entertainment') || g.contains('tv')) return 'Entertainment';
    if (g.contains('local') || g.contains('regional')) return 'Local';
    
    return 'Other'; // Fallback for unmatched categories
  }

  factory Channel.fromJson(Map<String, dynamic> json) {
    return Channel(
      id: json['id'] ?? '',
      playlistId: json['playlist_id'] ?? '',
      groupId: json['group_id'] ?? '',
      name: json['name'] ?? '',
      streamUrl: json['stream_url'] ?? '',
      logoUrl: json['logo_url'] ?? '',
      channelNumber: json['channel_number'] ?? 0,
      guideNumber: json['guide_number'] ?? '',
      isHidden: json['is_hidden'] ?? false,
      isFavorite: json['is_favorite'] ?? false,
      sourceChannelId: json['source_channel_id'],
    );
  }
}
