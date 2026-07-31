class Playlist {
  final String id;
  final String name;
  final String urlPath;
  final String type;
  final DateTime? createdAt;
  final bool createdFromFavorites;

  Playlist({
    required this.id,
    required this.name,
    required this.urlPath,
    required this.type,
    this.createdAt,
    this.createdFromFavorites = false,
  });

  factory Playlist.fromJson(Map<String, dynamic> json) {
    return Playlist(
      id: json['id'] ?? '',
      name: json['name'] ?? '',
      urlPath: json['url_path'] ?? '',
      type: json['type'] ?? '',
      createdAt: json['created_at'] != null ? DateTime.tryParse(json['created_at']) : null,
      createdFromFavorites: json['created_from_favorites'] ?? false,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'name': name,
      'url_path': urlPath,
      'type': type,
      'created_from_favorites': createdFromFavorites,
    };
  }
}
