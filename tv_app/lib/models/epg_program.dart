class EPGProgram {
  final String id;
  final String channelId;
  final String title;
  final String description;
  final DateTime startTime;
  final DateTime endTime;

  EPGProgram({
    required this.id,
    required this.channelId,
    required this.title,
    required this.description,
    required this.startTime,
    required this.endTime,
  });

  factory EPGProgram.fromJson(Map<String, dynamic> json) {
    return EPGProgram(
      id: json['id'] ?? '',
      channelId: json['channel_id'] ?? '',
      title: json['title'] ?? 'Unknown Program',
      description: json['description'] ?? '',
      startTime: DateTime.parse(json['start_time']).toLocal(),
      endTime: DateTime.parse(json['end_time']).toLocal(),
    );
  }
}

class ChannelEPG {
  final EPGProgram? currentProgram;
  final EPGProgram? nextProgram;

  ChannelEPG({this.currentProgram, this.nextProgram});

  factory ChannelEPG.fromJson(Map<String, dynamic> json) {
    return ChannelEPG(
      currentProgram: json['current'] != null ? EPGProgram.fromJson(json['current']) : null,
      nextProgram: json['next'] != null ? EPGProgram.fromJson(json['next']) : null,
    );
  }
}
