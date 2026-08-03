import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/camera.dart';
import '../services/api_service.dart';
import '../widgets/camera_tile.dart';
import '../widgets/fullscreen_camera_view.dart';

class CameraScreen extends StatefulWidget {
  const CameraScreen({super.key});

  @override
  State<CameraScreen> createState() => _CameraScreenState();
}

class _CameraScreenState extends State<CameraScreen> {
  List<Camera> _cameras = [];
  bool _isLoading = true;
  bool _hasError = false;
  String _errorMessage = '';
  Camera? _fullscreenCamera;

  @override
  void initState() {
    super.initState();
    _loadCameras();
  }

  Future<void> _loadCameras() async {
    setState(() {
      _isLoading = true;
      _hasError = false;
      _errorMessage = '';
    });

    try {
      final api = context.read<ApiService>();
      final cameras = await api.getCameras();
      
      if (mounted) {
        setState(() {
          _cameras = cameras;
          _isLoading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _hasError = true;
          _errorMessage = e.toString();
          _isLoading = false;
        });
      }
    }
  }

  void _enterFullscreen(Camera camera) {
    setState(() {
      _fullscreenCamera = camera;
    });
  }

  void _exitFullscreen() {
    setState(() {
      _fullscreenCamera = null;
    });
  }

  @override
  Widget build(BuildContext context) {
    // Fullscreen mode
    if (_fullscreenCamera != null) {
      return FullscreenCameraView(
        camera: _fullscreenCamera!,
        onClose: _exitFullscreen,
      );
    }

    // Grid mode
    return Scaffold(
      backgroundColor: const Color(0xFF0D0D0D),
      appBar: AppBar(
        title: const Text('Cameras'),
        backgroundColor: const Color(0xFF1A1A1A),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _loadCameras,
            tooltip: 'Refresh',
          ),
        ],
      ),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_isLoading) {
      return const Center(
        child: CircularProgressIndicator(
          color: Colors.blueAccent,
        ),
      );
    }

    if (_hasError) {
      return Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            const Icon(
              Icons.error_outline,
              color: Colors.redAccent,
              size: 64,
            ),
            const SizedBox(height: 16),
            Text(
              'Failed to load cameras',
              style: const TextStyle(
                color: Colors.white,
                fontSize: 18,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              _errorMessage,
              style: const TextStyle(
                color: Colors.white70,
                fontSize: 14,
              ),
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 24),
            ElevatedButton.icon(
              onPressed: _loadCameras,
              icon: const Icon(Icons.refresh),
              label: const Text('Retry'),
              style: ElevatedButton.styleFrom(
                backgroundColor: Colors.blueAccent,
                foregroundColor: Colors.white,
                padding: const EdgeInsets.symmetric(
                  horizontal: 24,
                  vertical: 12,
                ),
              ),
            ),
          ],
        ),
      );
    }

    if (_cameras.isEmpty) {
      return Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            const Icon(
              Icons.videocam_off,
              color: Colors.white54,
              size: 64,
            ),
            const SizedBox(height: 16),
            const Text(
              'No cameras configured',
              style: TextStyle(
                color: Colors.white,
                fontSize: 18,
              ),
            ),
            const SizedBox(height: 8),
            const Text(
              'Add cameras in Settings',
              style: TextStyle(
                color: Colors.white70,
                fontSize: 14,
              ),
            ),
          ],
        ),
      );
    }

    // Camera grid
    return RefreshIndicator(
      onRefresh: _loadCameras,
      color: Colors.blueAccent,
      child: GridView.builder(
        padding: const EdgeInsets.all(16),
        gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
          crossAxisCount: _getGridColumns(),
          crossAxisSpacing: 12,
          mainAxisSpacing: 12,
          childAspectRatio: 16 / 9,
        ),
        itemCount: _cameras.length,
        itemBuilder: (context, index) {
          final camera = _cameras[index];
          return CameraTile(
            camera: camera,
            onTap: () => _enterFullscreen(camera),
          );
        },
      ),
    );
  }

  int _getGridColumns() {
    if (_cameras.length <= 4) {
      return 2; // 2x2 grid for 4 or fewer cameras
    } else if (_cameras.length <= 9) {
      return 3; // 3x3 grid for 5-9 cameras
    } else {
      return 4; // 4 columns for many cameras
    }
  }
}
