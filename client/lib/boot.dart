// Boot: single-instance lock on 127.0.0.1:27180 (main.go follows that
// lifetime) and waking the Go backend when it isn't up yet.
import 'dart:async';
import 'dart:io';

import 'api.dart';

/// Hold the native-UI lock for as long as this process is alive.
/// Returns null when another instance already holds it.
Future<ServerSocket?> acquireLock() async {
  try {
    final s = await ServerSocket.bind(InternetAddress.loopbackIPv4, 27180);
    return s;
  } on Exception {
    return null;
  }
}

/// The backend normally launches us. When the user starts the client first
/// (or the backend crashed), wake the exe that sits next to us.
Future<void> ensureBackend() async {
  if (await Api.backendUp()) return;
  final dir = await _exeDir();
  if (dir == null) return;
  for (final name in const ['xbox-checker.exe', 'xbox-checker']) {
    final p = '$dir${Platform.pathSeparator}$name';
    if (File(p).existsSync()) {
      try {
        await Process.start(p, [],
            workingDirectory: dir, mode: ProcessStartMode.detached);
      } catch (_) {/* already running or blocked */}
      break;
    }
  }
  await Api.waitForBackend(
      timeout: const Duration(seconds: 15));
}

Future<String?> _exeDir() async {
  try {
    final exe = Platform.resolvedExecutable;
    return File(exe).parent.path;
  } catch (_) {
    return null;
  }
}
