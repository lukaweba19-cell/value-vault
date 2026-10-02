// SOLO Checker — Flutter desktop client entrypoint.
// Talks to the Go backend over 127.0.0.1:2718 (dashboard) and :2719 (viewer).
import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';

import 'api.dart';
import 'boot.dart';
import 'shell.dart';
import 'theme.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Single instance: if the lock port is taken, another window is alive —
  // main.go keeps serving while that lock answers, so we just exit.
  final lock = await acquireLock();
  if (lock == null) {
    exit(0);
  }

  // The backend normally starts us; if we were started first, wake it.
  unawaited(ensureBackend());

  runApp(SoloApp(lock: lock));
}

class SoloApp extends StatefulWidget {
  final ServerSocket lock;
  const SoloApp({super.key, required this.lock});

  @override
  State<SoloApp> createState() => _SoloAppState();
}

class _SoloAppState extends State<SoloApp> {
  @override
  void dispose() {
    // Releasing the lock tells the backend it may exit.
    widget.lock.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'SOLO Checker',
      debugShowCheckedModeBanner: false,
      theme: buildTheme(),
      home: const BootGate(),
    );
  }
}

/// Shows a connecting screen until the local backend answers, then the shell.
class BootGate extends StatefulWidget {
  const BootGate({super.key});

  @override
  State<BootGate> createState() => _BootGateState();
}

class _BootGateState extends State<BootGate> {
  bool _ready = false;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    _wait();
  }

  Future<void> _wait() async {
    final ok = await Api.waitForBackend(
        timeout: const Duration(seconds: 12));
    if (!mounted) return;
    setState(() {
      _ready = ok;
      _failed = !ok;
    });
  }

  @override
  Widget build(BuildContext context) {
    if (_ready) return const AppShell();
    return Scaffold(
      body: appBackdrop(
        Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const SizedBox(
                width: 42,
                height: 42,
                child: CircularProgressIndicator(
                    strokeWidth: 3, color: T.acc),
              ),
              const SizedBox(height: 18),
              Text(
                _failed ? 'Backend not responding' : 'Connecting to backend…',
                style: const TextStyle(fontSize: 13.5, color: T.mut),
              ),
              if (_failed) ...[
                const SizedBox(height: 6),
                const MutedSmall(
                    'Start xbox-checker.exe next to this client, then reopen.'),
                const SizedBox(height: 16),
                TextButton(
                  onPressed: () {
                    setState(() {
                      _failed = false;
                    });
                    _wait();
                  },
                  child: const Text('Retry'),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class MutedSmall extends StatelessWidget {
  final String text;
  const MutedSmall(this.text, {super.key});
  @override
  Widget build(BuildContext context) =>
      Text(text, style: mutedStyle(12));
}
