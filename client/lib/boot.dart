// Boot: the Windows single-instance mutex. The Go engine itself is loaded
// in-process by api.dart (dart:ffi) — nothing to launch, no ports to hold.
import 'dart:ffi';
import 'dart:io';

import 'package:ffi/ffi.dart';

Pointer<Void>? _instanceMutex;

/// The held single-instance mutex handle (kept reachable for the whole
/// process lifetime so the window stays unique).
Pointer<Void>? get heldInstanceMutex => _instanceMutex;

/// Returns false when another instance of the app is already running.
/// The mutex handle is kept alive for the whole process lifetime.
bool acquireSingleInstance() {
  if (!Platform.isWindows) return true;
  try {
    final k32 = DynamicLibrary.open('kernel32.dll');
    final createMutex = k32.lookupFunction<
        Pointer<Void> Function(Pointer<Void>, Int32, Pointer<Utf16>),
        Pointer<Void> Function(Pointer<Void>, int, Pointer<Utf16>)>(
        'CreateMutexW');
    final lastError = k32
        .lookupFunction<Uint32 Function(), int Function()>('GetLastError');
    final name = 'Local\\SoloCheckerSingleInstance'.toNativeUtf16();
    try {
      _instanceMutex = createMutex(nullptr, 0, name);
    } finally {
      calloc.free(name);
    }
    // 183 == ERROR_ALREADY_EXISTS: a second window refuses to start.
    return lastError() != 183;
  } catch (_) {
    // Never brick development builds over the mutex.
    return true;
  }
}
