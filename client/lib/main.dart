// SOLO Checker — Flutter desktop client entrypoint.
// One process: the window is Flutter, the engine is the Go shared library
// loaded in-process via dart:ffi. No sockets, no embedded web content.
import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';

import 'api.dart';
import 'boot.dart';
import 'shell.dart';
import 'theme.dart';

void main() {
  runZonedGuarded(() async {
    WidgetsFlutterBinding.ensureInitialized();

    // Single instance: the OS mutex refuses a second window. The old code
    // returned from main() without ever calling runApp, which left a hidden
    // process holding the mutex and NO window at all — every later launch
    // then "did nothing". Every path now shows a window.
    final gotIt = acquireSingleInstance();
    String? err;
    if (gotIt) {
      try {
        // Load the Go engine (solo.dll sits next to this exe).
        err = await FfiBackend.start();
      } catch (e) {
        err = 'Failed to start engine: $e';
      }
    }
    runApp(SoloApp(bootError: err, alreadyRunning: !gotIt));
  }, (e, st) {
    // A late async error must never take the window down silently.
    try {
      runApp(SoloApp(bootError: 'Unexpected error: $e'));
    } catch (_) {/* engine already tore the app down */}
  });
}

class SoloApp extends StatelessWidget {
  final String? bootError;
  final bool alreadyRunning;
  const SoloApp({super.key, this.bootError, this.alreadyRunning = false});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'SOLO Checker',
      debugShowCheckedModeBanner: false,
      theme: buildTheme(),
      home: alreadyRunning
          ? const AlreadyRunningScreen()
          : BootGate(bootError: bootError),
    );
  }
}

/// Shown when a second instance starts while one is already running.
/// Closes itself immediately so nothing lingers in the background.
class AlreadyRunningScreen extends StatefulWidget {
  const AlreadyRunningScreen({super.key});

  @override
  State<AlreadyRunningScreen> createState() => _AlreadyRunningScreenState();
}

class _AlreadyRunningScreenState extends State<AlreadyRunningScreen> {
  @override
  void initState() {
    super.initState();
    Timer(const Duration(milliseconds: 1600), () {
      exit(0);
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: appBackdrop(
        Center(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: const [
              SizedBox(
                width: 42,
                height: 42,
                child: CircularProgressIndicator(strokeWidth: 3, color: T.acc),
              ),
              SizedBox(height: 18),
              Text(
                'SOLO Checker is already running…',
                style: TextStyle(fontSize: 13.5, color: T.mut),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Smoke-checks the in-process engine once, then shows the shell.
class BootGate extends StatefulWidget {
  final String? bootError;
  const BootGate({super.key, this.bootError});

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
    final r = await Api.get('/api/state');
    if (!mounted) return;
    setState(() {
      _ready = r.ok;
      _failed = !r.ok;
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
                child:
                    CircularProgressIndicator(strokeWidth: 3, color: T.acc),
              ),
              const SizedBox(height: 18),
              Text(
                _failed ? 'Engine not responding' : 'Starting engine…',
                style: const TextStyle(fontSize: 13.5, color: T.mut),
              ),
              if (_failed) ...[
                const SizedBox(height: 6),
                MutedSmall(widget.bootError ??
                    'The engine could not be loaded — reinstall the app.'),
                const SizedBox(height: 16),
                TextButton(
                  onPressed: () {
                    setState(() => _failed = false);
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
  Widget build(BuildContext context) => Text(text, style: mutedStyle(12));
}
