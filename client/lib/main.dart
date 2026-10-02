// SOLO Checker — Flutter desktop client entrypoint.
// One process: the window is Flutter, the engine is the Go shared library
// loaded in-process via dart:ffi. No sockets, no embedded web content.
import 'package:flutter/material.dart';

import 'api.dart';
import 'boot.dart';
import 'shell.dart';
import 'theme.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Single instance: the OS mutex refuses a second window.
  if (!acquireSingleInstance()) return;

  // Load the Go engine (xbox_checker.dll sits next to this exe).
  final err = await FfiBackend.start();

  runApp(SoloApp(bootError: err));
}

class SoloApp extends StatelessWidget {
  final String? bootError;
  const SoloApp({super.key, this.bootError});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'SOLO Checker',
      debugShowCheckedModeBanner: false,
      theme: buildTheme(),
      home: BootGate(bootError: bootError),
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
                    'xbox_checker.dll could not be loaded — reinstall the app.'),
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
