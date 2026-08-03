class Camera {
  final String id;
  final String name;
  final String rtspUrl;
  final String location;
  final bool isEnabled;
  final int sortOrder;
  final DateTime? createdAt;

  Camera({
    required this.id,
    required this.name,
    required this.rtspUrl,
    required this.location,
    required this.isEnabled,
    required this.sortOrder,
    this.createdAt,
  });

  factory Camera.fromJson(Map<String, dynamic> json) {
    return Camera(
      id: json['id'] ?? '',
      name: json['name'] ?? '',
      rtspUrl: json['rtsp_url'] ?? '',
      location: json['location'] ?? '',
      isEnabled: json['is_enabled'] ?? true,
      sortOrder: json['sort_order'] ?? 0,
      createdAt: json['created_at'] != null ? DateTime.tryParse(json['created_at']) : null,
    );
  }

  Map<String, dynamic> toJson() {
    return {
      'id': id,
      'name': name,
      'rtsp_url': rtspUrl,
      'location': location,
      'is_enabled': isEnabled,
      'sort_order': sortOrder,
      'created_at': createdAt?.toIso8601String(),
    };
  }
}
