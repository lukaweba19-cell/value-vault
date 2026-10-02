// dart:ffi transport for the Go engine. The Flutter app and the checker
// engine share ONE process: xbox_checker.dll is loaded directly and every
// route runs in-process — no sockets, no HTTP, no second process.
//
// Streaming routes (SSE editor runs, NDJSON mail connect, viewer events)
// push chunks through a NativeCallable listener back into the UI isolate.
import 'dart:async';
import 'dart:convert';
import 'dart:ffi';
import 'dart:io';
import 'dart:isolate';

import 'package:ffi/ffi.dart';

// Legacy labels: the `base:` parameters the views pass around are kept for
// API compatibility, but routing happens on the engine's single in-process
// route table now (routes.go newFFIMux).
const apiBase = 'ffi';
const viewerBase = 'ffi';

class ApiRes {
  final bool ok;
  final int status;
  final dynamic data;
  final String text;
  ApiRes({required this.ok, required this.status, this.data, this.text = ''});
}

/* ---------------- native signatures ---------------- */

typedef _SoloCallNative = Pointer<Utf8> Function(
    Pointer<Utf8> method, Pointer<Utf8> path, Pointer<Utf8> body);
typedef _SoloCallDart = Pointer<Utf8> Function(
    Pointer<Utf8>, Pointer<Utf8>, Pointer<Utf8>);
typedef _SoloFreeNative = Void Function(Pointer<Utf8>);
typedef _SoloFreeDart = void Function(Pointer<Utf8>);
typedef _SoloInitNative = Void Function();
typedef _SoloInitDart = void Function();
typedef _SoloCancelNative = Void Function(Int64);
typedef _SoloCancelDart = void Function(int);
typedef _ChunkNative = Void Function(
    Int64 id, Pointer<Utf8> data, Int32 len);
typedef _SoloStreamNative = Void Function(
    Int64 id,
    Pointer<Utf8> method,
    Pointer<Utf8> path,
    Pointer<Utf8> body,
    Pointer<NativeFunction<_ChunkNative>> cb);
typedef _SoloStreamDart = void Function(
    int,
    Pointer<Utf8>,
    Pointer<Utf8>,
    Pointer<Utf8>,
    Pointer<NativeFunction<_ChunkNative>>);

DynamicLibrary? _libCache;

DynamicLibrary _lib() {
  if (_libCache != null) return _libCache!;
  final override = Platform.environment['SOLO_BRIDGE'];
  if (override != null && override.isNotEmpty) {
    _libCache = DynamicLibrary.open(override);
  } else if (Platform.isWindows) {
    _libCache = DynamicLibrary.open('xbox_checker.dll');
  } else {
    _libCache = DynamicLibrary.open('xbox_checker.so');
  }
  return _libCache!;
}

/// One route call, executed off the UI isolate (file dialogs, engine start
/// and network probes all run here without freezing a single frame).
String _ffiCall(String method, String path, String body) {
  final lib = _lib();
  final call =
      lib.lookupFunction<_SoloCallNative, _SoloCallDart>('SoloCall');
  final free = lib.lookupFunction<_SoloFreeNative, _SoloFreeDart>('SoloFree');
  final pm = method.toNativeUtf8();
  final pp = path.toNativeUtf8();
  final pb = body.toNativeUtf8();
  Pointer<Utf8>? res;
  try {
    res = call(pm, pp, pb);
    return res.toDartString();
  } finally {
    if (res != null) free(res);
    calloc.free(pm);
    calloc.free(pp);
    calloc.free(pb);
  }
}

/* ---------------- api ---------------- */

class Api {
  static bool _isErr(int code) => code < 200 || code >= 300;

  static Future<ApiRes> _call(String method, String path, String body) async {
    try {
      final future = Isolate.run(() => _ffiCall(method, path, body));
      // Native file dialogs may stay open indefinitely; everything else is
      // bounded so a stuck engine call surfaces as an error, not a hang.
      final bounded = path.startsWith('/api/pick')
          ? future
          : future.timeout(const Duration(seconds: 120));
      final raw = await bounded;
      dynamic map;
      try {
        map = jsonDecode(raw);
      } catch (_) {
        map = null;
      }
      final status = map is Map && map['s'] is int ? map['s'] as int : 0;
      final text =
          map is Map && map['b'] is String ? map['b'] as String : raw;
      dynamic data;
      try {
        data = jsonDecode(text);
      } catch (_) {/* plain-text errors from stdhttp.Error */}
      return ApiRes(
          ok: !_isErr(status), status: status, data: data, text: text);
    } catch (e) {
      return ApiRes(ok: false, status: 0, text: '$e');
    }
  }

  /// GET a route (state, lists, stats, pick…).
  static Future<ApiRes> get(String path, {String base = apiBase}) =>
      _call('GET', path, '');

  /// POST a JSON body.
  static Future<ApiRes> post(String path, Map<String, dynamic> body,
          {String base = apiBase}) =>
      _call('POST', path, jsonEncode(body));

  /// POST with an empty JSON object (pause/stop/reload style endpoints).
  static Future<ApiRes> postEmpty(String path, {String base = apiBase}) =>
      _call('POST', path, '{}');

  static Future<ApiRes> delete(String path, {String base = apiBase}) =>
      _call('DELETE', path, '');

  /* ---------------- streaming ---------------- */

  static final Map<int, StreamController<String>> _streams = {};
  static int _streamSeq = 0;
  static NativeCallable<_ChunkNative>? _chunkCb;

  static void _onChunk(int id, Pointer<Utf8> data, int len) {
    final ctl = _streams[id];
    if (ctl == null) return;
    if (data.address == 0) {
      _streams.remove(id);
      if (!ctl.isClosed) ctl.close();
    } else {
      try {
        ctl.add(data.toDartString());
      } catch (_) {/* controller already cancelled */}
    }
  }

  /// Opens a streaming route on the UI isolate. Chunks arrive through the
  /// global NativeCallable; cancelling the returned stream cancels the Go
  /// side too (SoloCancel).
  static Stream<String> _openStream(String method, String path, String body) {
    _chunkCb ??= NativeCallable<_ChunkNative>.listener(_onChunk);
    final id = ++_streamSeq;
    final ctl = StreamController<String>();
    _streams[id] = ctl;
    ctl.onCancel = () {
      _streams.remove(id);
      try {
        final cancel = _lib()
            .lookupFunction<_SoloCancelNative, _SoloCancelDart>('SoloCancel');
        cancel(id);
      } catch (_) {/* engine already gone */}
    };
    final stream = _lib()
        .lookupFunction<_SoloStreamNative, _SoloStreamDart>('SoloStream');
    final pm = method.toNativeUtf8();
    final pp = path.toNativeUtf8();
    final pb = body.toNativeUtf8();
    try {
      stream(id, pm, pp, pb, _chunkCb!.nativeFunction);
    } finally {
      calloc.free(pm);
      calloc.free(pp);
      calloc.free(pb);
    }
    return ctl.stream;
  }

  /// SSE frames (`data: {json}\n\n`) for the editor analyze/filter runs.
  /// Yields the parsed JSON payload of each frame.
  static Future<Stream<Map<String, dynamic>>> sse(String path,
      {String base = apiBase}) async {
    final out = StreamController<Map<String, dynamic>>();
    final buf = StringBuffer();
    _openStream('GET', path, '').listen((chunk) {
      buf.write(chunk);
      var s = buf.toString();
      var idx = s.indexOf('\n\n');
      while (idx >= 0) {
        final frame = s.substring(0, idx);
        s = s.substring(idx + 2);
        for (final line in frame.split('\n')) {
          if (line.startsWith('data:')) {
            try {
              final d = jsonDecode(line.substring(5).trim());
              if (d is Map<String, dynamic>) out.add(d);
            } catch (_) {/* partial frame */}
          }
        }
        idx = s.indexOf('\n\n');
      }
      buf
        ..clear()
        ..write(s);
    }, onDone: out.close, onError: (_) => out.close());
    return out.stream;
  }

  /// NDJSON (one JSON object per line) for /api/mail/connect.
  static Future<Stream<Map<String, dynamic>>> ndjson(String path,
      Map<String, dynamic> body, {String base = apiBase}) async {
    final out = StreamController<Map<String, dynamic>>();
    final buf = StringBuffer();
    _openStream('POST', path, jsonEncode(body)).listen((chunk) {
      buf.write(chunk);
      var s = buf.toString();
      var nl = s.indexOf('\n');
      while (nl >= 0) {
        final line = s.substring(0, nl).trim();
        s = s.substring(nl + 1);
        if (line.isNotEmpty) {
          try {
            final d = jsonDecode(line);
            if (d is Map<String, dynamic>) out.add(d);
          } catch (_) {/* partial line */}
        }
        nl = s.indexOf('\n');
      }
      buf
        ..clear()
        ..write(s);
    }, onDone: out.close, onError: (_) => out.close());
    return out.stream;
  }

  /// Long-lived change feed for the viewer (`data: update` frames).
  static Stream<String> events({String base = viewerBase}) {
    final out = StreamController<String>();
    final buf = StringBuffer();
    final sub = _openStream('GET', '/api/events', '').listen((chunk) {
      buf.write(chunk);
      var s = buf.toString();
      var idx = s.indexOf('\n\n');
      while (idx >= 0) {
        final frame = s.substring(0, idx);
        s = s.substring(idx + 2);
        for (final line in frame.split('\n')) {
          if (line.startsWith('data:')) {
            out.add(line.substring(5).trim());
          }
        }
        idx = s.indexOf('\n\n');
      }
      buf
        ..clear()
        ..write(s);
    }, onDone: out.close, onError: (_) => out.close());
    out.onCancel = sub.cancel;
    return out.stream;
  }
}

/// Loads the in-process Go engine and runs its one-time bootstrap.
class FfiBackend {
  static Future<String?> start() async {
    try {
      await Isolate.run(() {
        final lib = _lib();
        final init =
            lib.lookupFunction<_SoloInitNative, _SoloInitDart>('SoloInit');
        init();
        return true;
      });
      return null;
    } catch (e) {
      return 'Failed to start engine: $e';
    }
  }
}

Future<void> openLink(String url) =>
    Api.get('/api/open-link?u=${Uri.encodeComponent(url)}');

String fileBase(String p) => p.split(RegExp(r'[\\/]')).last;
