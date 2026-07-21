class Playlist {
  final String id;
  final String name;
  final String urlPath;
  final String type;
  final DateTime? createdAt;

  Playlist({
    required this.id,
    required this.name,
    required this.urlPath,
    required this.type,
    this.createdAt,
  });

  factory Playlist.fromJson(Map<String, dynamic> json) {
    return Playlist(
      id: json['id'] ?? '',
      name: json['name'] ?? '',
      urlPath: json['url_path'] ?? '',
      type: json['type'] ?? '',
      createdAt: json['created_at'] != null ? DateTime.tryParse(json['created_at']) : null,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'name': name,
      'url_path': urlPath,
      'type': type,
    };
  }
}
