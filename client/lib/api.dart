// HTTP client for the local Go backend (apiAddr 127.0.0.1:2718, viewer mux
// 127.0.0.1:2719). Mirrors api()/apiJSON() from the web UI, plus streaming
// helpers for the SSE editor endpoints and the NDJSON mail-connect flow.
import 'dart:async';
import 'dart:convert';
import 'dart:io';

const apiBase = 'http://127.0.0.1:2718';
const viewerBase = 'http://127.0.0.1:2719';

class ApiRes {
  final bool ok;
  final int status;
  final dynamic data;
  final String text;
  ApiRes({required this.ok, required this.status, this.data, this.text = ''});
}

class Api {
  static final HttpClient _http = HttpClient()
    ..connectionTimeout = const Duration(seconds: 8)
    ..badCertificateCallback = (_, _, _) => true;

  static bool _isErr(int code) => code < 200 || code >= 300;

  /// GET a JSON endpoint (api() equivalent).
  static Future<ApiRes> get(String path, {String base = apiBase}) async {
    try {
      final req = await _http.getUrl(Uri.parse(base + path));
      final res = await req.close().timeout(const Duration(seconds: 30));
      final body = await res.transform(utf8.decoder).join();
      dynamic data;
      try {
        data = jsonDecode(body);
      } catch (_) {/* non-JSON */}
      return ApiRes(ok: !_isErr(res.statusCode), status: res.statusCode, data: data, text: body);
    } on Exception catch (e) {
      return ApiRes(ok: false, status: 0, text: e.toString());
    }
  }

  /// POST JSON (apiJSON() equivalent). Returns parsed body when possible.
  static Future<ApiRes> post(String path, Map<String, dynamic> body,
      {String base = apiBase}) async {
    try {
      final req = await _http.postUrl(Uri.parse(base + path));
      req.headers.contentType = ContentType('application', 'json');
      req.write(jsonEncode(body));
      final res = await req.close().timeout(const Duration(seconds: 60));
      final text = await res.transform(utf8.decoder).join();
      dynamic data;
      try {
        data = jsonDecode(text);
      } catch (_) {/* plain-text error from stdhttp.Error */}
      return ApiRes(ok: !_isErr(res.statusCode), status: res.statusCode, data: data, text: text);
    } on Exception catch (e) {
      return ApiRes(ok: false, status: 0, text: e.toString());
    }
  }

  /// POST with an empty body (pause/stop/reload/open-viewer style endpoints).
  static Future<ApiRes> postEmpty(String path, {String base = apiBase}) =>
      post(path, const {}, base: base);

  static Future<ApiRes> delete(String path, {String base = apiBase}) async {
    try {
      final req = await _http.deleteUrl(Uri.parse(base + path));
      final res = await req.close().timeout(const Duration(seconds: 30));
      final text = await res.transform(utf8.decoder).join();
      dynamic data;
      try {
        data = jsonDecode(text);
      } catch (_) {}
      return ApiRes(ok: !_isErr(res.statusCode), status: res.statusCode, data: data, text: text);
    } on Exception catch (e) {
      return ApiRes(ok: false, status: 0, text: e.toString());
    }
  }

  /// SSE stream (text/event-stream, `data: {json}\n\n` frames) used by
  /// /api/editor/analyze-stream and /api/editor/filter-stream. Yields the
  /// parsed JSON payload of each frame; closes when the server ends it.
  static Future<Stream<Map<String, dynamic>>> sse(String path,
      {String base = apiBase}) async {
    final req = await _http.getUrl(Uri.parse(base + path));
    req.headers.set('Accept', 'text/event-stream');
    final res = await req.close();
    final controller = StreamController<Map<String, dynamic>>();
    final buf = StringBuffer();
    res.transform(utf8.decoder).listen((chunk) {
      buf.write(chunk);
      var s = buf.toString();
      var idx = s.indexOf('\n\n');
      while (idx >= 0) {
        final frame = s.substring(0, idx);
        s = s.substring(idx + 2);
        for (final line in frame.split('\n')) {
          if (line.startsWith('data:')) {
            final payload = line.substring(5).trim();
            try {
              final d = jsonDecode(payload);
              if (d is Map<String, dynamic>) controller.add(d);
            } catch (_) {}
          }
        }
        idx = s.indexOf('\n\n');
      }
      buf
        ..clear()
        ..write(s);
    }, onDone: controller.close, onError: (_) => controller.close());
    return controller.stream;
  }

  /// NDJSON stream (one JSON object per line) for /api/mail/connect.
  static Future<Stream<Map<String, dynamic>>> ndjson(String path,
      Map<String, dynamic> body, {String base = apiBase}) async {
    final req = await _http.postUrl(Uri.parse(base + path));
    req.headers.contentType = ContentType('application', 'json');
    req.write(jsonEncode(body));
    final res = await req.close();
    final controller = StreamController<Map<String, dynamic>>();
    final buf = StringBuffer();
    res.transform(utf8.decoder).listen((chunk) {
      buf.write(chunk);
      var s = buf.toString();
      var nl = s.indexOf('\n');
      while (nl >= 0) {
        final line = s.substring(0, nl).trim();
        s = s.substring(nl + 1);
        if (line.isNotEmpty) {
          try {
            final d = jsonDecode(line);
            if (d is Map<String, dynamic>) controller.add(d);
          } catch (_) {}
        }
        nl = s.indexOf('\n');
      }
      buf
        ..clear()
        ..write(s);
    }, onDone: controller.close, onError: (_) => controller.close());
    return controller.stream;
  }

  /// Long-lived SSE for viewer change events (`data: update` frames).
  static Stream<String> events({String base = viewerBase}) {
    final controller = StreamController<String>();
    HttpClientRequest? req;
    _http.getUrl(Uri.parse('$base/api/events')).then((r) {
      req = r;
      r.headers.set('Accept', 'text/event-stream');
      return r.close();
    }).then((res) {
      final buf = StringBuffer();
      res.transform(utf8.decoder).listen((chunk) {
        buf.write(chunk);
        var s = buf.toString();
        var idx = s.indexOf('\n\n');
        while (idx >= 0) {
          final frame = s.substring(0, idx);
          s = s.substring(idx + 2);
          for (final line in frame.split('\n')) {
            if (line.startsWith('data:')) {
              controller.add(line.substring(5).trim());
            }
          }
          idx = s.indexOf('\n\n');
        }
        buf
          ..clear()
          ..write(s);
      }, onDone: controller.close, onError: (_) => controller.close());
    }).catchError((Object _) {
      controller.close();
    });
    controller.onCancel = () {
      try {
        req?.abort();
      } catch (_) {}
    };
    return controller.stream;
  }

  /// Wait until the local backend answers (boot handshake).
  static Future<bool> waitForBackend(
      {Duration timeout = const Duration(seconds: 20)}) async {
    final deadline = DateTime.now().add(timeout);
    while (DateTime.now().isBefore(deadline)) {
      final r = await get('/api/state');
      if (r.ok) return true;
      await Future.delayed(const Duration(milliseconds: 350));
    }
    return false;
  }

  static Future<bool> backendUp() async => (await get('/api/state')).ok;
}

Future<void> openLink(String url) =>
    Api.get('/api/open-link?u=${Uri.encodeComponent(url)}');

String fileBase(String p) => p
    .split(RegExp(r'[\\/]'))
    .last;
