class Channel {
  final String id;
  final String name;
  final String streamUrl;
  final bool isFavorite;

  Channel({
    required this.id,
    required this.name,
    required this.streamUrl,
    required this.isFavorite,
  });

  factory Channel.fromJson(Map<String, dynamic> json) {
    return Channel(
      id: json['id'],
      name: json['name'],
      streamUrl: json['stream_url'],
      isFavorite: json['is_favorite'] ?? false,
    );
  }
}
