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
    this.isFavorite = false,
  });

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
    );
  }
}
