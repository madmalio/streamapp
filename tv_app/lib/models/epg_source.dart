class EpgSource {
  final String id;
  final String name;
  final String url;
  final DateTime? createdAt;

  EpgSource({
    required this.id,
    required this.name,
    required this.url,
    this.createdAt,
  });

  factory EpgSource.fromJson(Map<String, dynamic> json) {
    return EpgSource(
      id: json['id'] ?? '',
      name: json['name'] ?? '',
      url: json['url'] ?? '',
      createdAt: json['created_at'] != null ? DateTime.tryParse(json['created_at']) : null,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'name': name,
      'url': url,
    };
  }
}
