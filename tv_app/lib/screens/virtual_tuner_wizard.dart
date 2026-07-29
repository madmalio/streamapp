import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../services/api_service.dart';
import '../models/channel.dart';
import '../models/playlist.dart';

class VirtualTunerWizard extends StatefulWidget {
  final List<Playlist> availableTuners;

  const VirtualTunerWizard({Key? key, required this.availableTuners}) : super(key: key);

  @override
  State<VirtualTunerWizard> createState() => _VirtualTunerWizardState();
}

class _VirtualTunerWizardState extends State<VirtualTunerWizard> {
  int _currentStep = 0;
  bool _isBuilding = false;

  final _nameController = TextEditingController(text: "My Custom Tuner");
  
  Map<String, bool> _selectedSources = {};
  
  final Map<String, bool> _selectedGenres = {
    'Movies': true,
    'News': true,
    'Sports': true,
    'Kids': true,
    'Comedy': true,
    'Music': false,
    'Documentary': false,
    'Crime & Mystery': false,
    'Entertainment': false,
    'Local': true,
    'Uncategorized / Other': false,
  };
  
  double _maxChannels = 100; // 0 means unlimited

  @override
  void initState() {
    super.initState();
    // Default: select all tuners by default
    _selectedSources = { for (var t in widget.availableTuners) t.id: true };
  }

  List<Channel> _previewChannels = [];
  Map<String, bool> _selectedChannels = {};

  Future<void> _fetchPreview() async {
    setState(() => _isBuilding = true);
    try {
      final api = context.read<ApiService>();
      final allChannels = await api.getChannels();
      
      final List<String> activeSources = _selectedSources.entries
          .where((e) => e.value)
          .map((e) => e.key)
          .toList();
          
      final List<String> activeGenres = _selectedGenres.entries
          .where((e) => e.value)
          .map((e) => e.key)
          .toList();
          
      final includeUncategorized = _selectedGenres['Uncategorized / Other'] ?? false;

      // 1. Filter
      List<Channel> filtered = allChannels.where((c) {
        if (!activeSources.contains(c.playlistId)) return false;
        
        final cat = c.normalizedCategory;
        if (cat.isEmpty || cat == 'Other') {
          // If a source has no categories (like HDHomeRun), automatically include it
          // Or if the user explicitly checked Uncategorized
          return true; 
        }
        return activeGenres.contains(cat);
      }).toList();
      
      // 2. Deduplicate based on alphanumeric name
      // First, sort `filtered` so that local/antenna tuners come first.
      // This ensures that if there's a duplicate (e.g. CBS on Antenna vs CBS on Pluto),
      // the Antenna version is kept.
      filtered.sort((a, b) {
        final tunerA = widget.availableTuners.firstWhere((t) => t.id == a.playlistId, orElse: () => Playlist(id: '', name: '', urlPath: '', type: ''));
        final tunerB = widget.availableTuners.firstWhere((t) => t.id == b.playlistId, orElse: () => Playlist(id: '', name: '', urlPath: '', type: ''));
        
        final aIsLocal = tunerA.type == 'HDHR' || tunerA.type == 'M3U';
        final bIsLocal = tunerB.type == 'HDHR' || tunerB.type == 'M3U';
        
        if (aIsLocal && !bIsLocal) return -1;
        if (!aIsLocal && bIsLocal) return 1;
        return 0;
      });

      Map<String, Channel> deduped = {};
      for (var c in filtered) {
        // Strip out HD/FHD, spaces, non-alphanumerics to get a pure name
        final key = c.name.toLowerCase().replaceAll(RegExp(r'[^a-z0-9]'), '').replaceAll('hd', '').replaceAll('fhd', '');
        if (!deduped.containsKey(key)) {
          deduped[key] = c;
        }
      }
      List<Channel> finalChannels = deduped.values.toList();
      
      // 3. Balanced Trim if needed
      if (_maxChannels > 0 && finalChannels.length > _maxChannels) {
        final limit = _maxChannels.toInt();
        
        // Group by category
        Map<String, List<Channel>> byCategory = {};
        for (var c in finalChannels) {
          final cat = c.normalizedCategory.isEmpty ? 'Other' : c.normalizedCategory;
          byCategory.putIfAbsent(cat, () => []).add(c);
        }
        
        // Calculate quota per category
        int activeCatsCount = byCategory.keys.length;
        if (activeCatsCount == 0) activeCatsCount = 1;
        int quotaPerCat = (limit / activeCatsCount).ceil();
        
        List<Channel> trimmed = [];
        for (var catChannels in byCategory.values) {
          trimmed.addAll(catChannels.take(quotaPerCat));
        }
        
        // If we still overshoot due to ceil(), just cut off the end
        if (trimmed.length > limit) {
          trimmed = trimmed.sublist(0, limit);
        }
        finalChannels = trimmed;
      }

      _previewChannels = finalChannels;
      _selectedChannels = { for (var c in _previewChannels) c.id: true };
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error: $e')));
    } finally {
      setState(() => _isBuilding = false);
    }
  }

  Future<void> _submitTuner() async {
    setState(() => _isBuilding = true);
    try {
      final api = context.read<ApiService>();
      final selectedIds = _selectedChannels.entries
          .where((e) => e.value)
          .map((e) => e.key)
          .toList();

      await api.generateVirtualTuner(
        _nameController.text,
        selectedIds,
      );
      
      if (mounted) {
        Navigator.pop(context, true);
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error: $e')));
        setState(() => _isBuilding = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: const Color(0xFF0A0A0A),
      appBar: AppBar(
        title: const Text('Virtual Cable Company Wizard'),
        backgroundColor: const Color(0xFF121212),
      ),
      body: Theme(
        data: Theme.of(context).copyWith(
          colorScheme: const ColorScheme.dark(
            primary: Colors.blueAccent,
            background: Color(0xFF0A0A0A),
          ),
        ),
        child: Stepper(
          type: StepperType.horizontal,
          currentStep: _currentStep,
          onStepContinue: () async {
            if (_currentStep == 0) {
              if (_nameController.text.isEmpty) return;
              setState(() => _currentStep++);
            } else if (_currentStep == 1) {
              await _fetchPreview();
              setState(() => _currentStep++);
            } else if (_currentStep == 2) {
              await _submitTuner();
            }
          },
          onStepCancel: () {
            if (_currentStep > 0) {
              setState(() => _currentStep--);
            } else {
              Navigator.pop(context);
            }
          },
          controlsBuilder: (context, details) {
            final isLast = _currentStep == 2;
            return Padding(
              padding: const EdgeInsets.only(top: 24.0),
              child: Row(
                children: [
                  ElevatedButton(
                    onPressed: _isBuilding ? null : details.onStepContinue,
                    style: ElevatedButton.styleFrom(
                      padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 16),
                      backgroundColor: isLast ? Colors.green : Colors.blueAccent,
                      foregroundColor: Colors.white,
                    ),
                    child: _isBuilding 
                        ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
                        : Text(isLast ? 'Build Tuner' : 'Next'),
                  ),
                  const SizedBox(width: 16),
                  if (_currentStep > 0)
                    TextButton(
                      onPressed: _isBuilding ? null : details.onStepCancel,
                      child: const Text('Back', style: TextStyle(color: Colors.white70)),
                    ),
                ],
              ),
            );
          },
          steps: [
            Step(
              title: const Text('Name'),
              isActive: _currentStep >= 0,
              content: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text('Give your new virtual cable company a name:', style: TextStyle(color: Colors.white70, fontSize: 16)),
                  const SizedBox(height: 16),
                  TextField(
                    controller: _nameController,
                    style: const TextStyle(color: Colors.white),
                    decoration: const InputDecoration(
                      labelText: 'Cable Company Name',
                      filled: true,
                      fillColor: Color(0xFF1A1A1A),
                      border: OutlineInputBorder(),
                    ),
                  ),
                ],
              ),
            ),
            Step(
              title: const Text('Sources & Categories'),
              isActive: _currentStep >= 1,
              content: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text('Select Tuners to import channels from:', style: TextStyle(color: Colors.white70, fontSize: 16, fontWeight: FontWeight.bold)),
                  const SizedBox(height: 8),
                  if (widget.availableTuners.isEmpty)
                    const Text('No tuners available.', style: TextStyle(color: Colors.white54))
                  else
                    Wrap(
                      spacing: 8,
                      runSpacing: 8,
                      children: widget.availableTuners.map((tuner) {
                        final isSelected = _selectedSources[tuner.id] ?? false;
                        return FilterChip(
                          label: Text(tuner.name),
                          selected: isSelected,
                          onSelected: (val) {
                            setState(() {
                              _selectedSources[tuner.id] = val;
                            });
                          },
                          selectedColor: Colors.green.withValues(alpha: 0.3),
                          checkmarkColor: Colors.greenAccent,
                          backgroundColor: const Color(0xFF1A1A1A),
                          labelStyle: TextStyle(color: isSelected ? Colors.white : Colors.white54),
                        );
                      }).toList(),
                    ),
                  const SizedBox(height: 24),
                  const Divider(color: Colors.white24),
                  const SizedBox(height: 16),
                  
                  const Text('Select Categories to include:', style: TextStyle(color: Colors.white70, fontSize: 16, fontWeight: FontWeight.bold)),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: _selectedGenres.keys.map((genre) {
                      final isSelected = _selectedGenres[genre]!;
                      return FilterChip(
                        label: Text(genre),
                        selected: isSelected,
                        onSelected: (val) {
                          setState(() {
                            _selectedGenres[genre] = val;
                          });
                        },
                        selectedColor: Colors.blueAccent.withValues(alpha: 0.3),
                        checkmarkColor: Colors.blueAccent,
                        backgroundColor: const Color(0xFF1A1A1A),
                        labelStyle: TextStyle(color: isSelected ? Colors.white : Colors.white54),
                      );
                    }).toList(),
                  ),
                  
                  const SizedBox(height: 24),
                  const Divider(color: Colors.white24),
                  const SizedBox(height: 16),
                  
                  Row(
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      const Text('Max Channels Limit:', style: TextStyle(color: Colors.white70, fontSize: 16, fontWeight: FontWeight.bold)),
                      Text(
                        _maxChannels == 0 ? 'Unlimited' : '${_maxChannels.toInt()}',
                        style: const TextStyle(color: Colors.blueAccent, fontSize: 18, fontWeight: FontWeight.bold),
                      ),
                    ],
                  ),
                  Slider(
                    value: _maxChannels,
                    min: 0,
                    max: 500,
                    divisions: 10,
                    activeColor: Colors.blueAccent,
                    onChanged: (val) {
                      setState(() {
                        _maxChannels = val;
                      });
                    },
                  ),
                  const Text('If you select a limit, the wizard will intelligently pick a balanced mix of channels across your chosen categories to fit the limit.', style: TextStyle(color: Colors.white54, fontSize: 12)),
                ],
              ),
            ),
            Step(
              title: const Text('Review'),
              isActive: _currentStep >= 2,
              content: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('We found ${_previewChannels.length} channels matching your criteria.', style: const TextStyle(color: Colors.white, fontSize: 16, fontWeight: FontWeight.bold)),
                  const SizedBox(height: 8),
                  const Text('Uncheck any channels you do not want in your final lineup. They will automatically be assigned channel numbers 1, 2, 3...', style: TextStyle(color: Colors.white54, fontSize: 14)),
                  const SizedBox(height: 16),
                  Container(
                    height: 400,
                    decoration: BoxDecoration(
                      color: const Color(0xFF121212),
                      borderRadius: BorderRadius.circular(8),
                      border: Border.all(color: Colors.white12),
                    ),
                    child: _previewChannels.isEmpty
                        ? const Center(child: Text('No channels found.', style: TextStyle(color: Colors.white54)))
                        : ListView.builder(
                            itemCount: _previewChannels.length,
                            itemBuilder: (ctx, i) {
                              final c = _previewChannels[i];
                              final isSelected = _selectedChannels[c.id] ?? false;
                              return CheckboxListTile(
                                title: Text(c.name, style: const TextStyle(color: Colors.white)),
                                subtitle: Text(c.normalizedCategory, style: const TextStyle(color: Colors.white54, fontSize: 12)),
                                value: isSelected,
                                activeColor: Colors.blueAccent,
                                onChanged: (val) {
                                  setState(() {
                                    _selectedChannels[c.id] = val ?? false;
                                  });
                                },
                              );
                            },
                          ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
