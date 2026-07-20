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
  final List<EPGProgram> programs;

  ChannelEPG({required this.programs});

  factory ChannelEPG.fromJson(Map<String, dynamic> json) {
    var list = json['programs'] as List?;
    List<EPGProgram> programsList = [];
    if (list != null) {
      programsList = list.map((i) => EPGProgram.fromJson(i)).toList();
    }
    return ChannelEPG(programs: programsList);
  }

  // Helper getters for backward compatibility
  EPGProgram? get currentProgram {
    final now = DateTime.now();
    for (var p in programs) {
      if (!now.isBefore(p.startTime) && now.isBefore(p.endTime)) {
        return p;
      }
    }
    return programs.isNotEmpty ? programs.first : null;
  }

  EPGProgram? get nextProgram {
    final current = currentProgram;
    if (current == null) return null;
    final index = programs.indexOf(current);
    if (index >= 0 && index < programs.length - 1) {
      return programs[index + 1];
    }
    return null;
  }
}
