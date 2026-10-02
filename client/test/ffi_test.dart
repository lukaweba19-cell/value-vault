// Integration smoke for the dart:ffi bridge. Skipped unless SOLO_BRIDGE
// points at a built shared library, e.g.:
//   SOLO_BRIDGE=/tmp/xbox_checker.so flutter test
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:solo_checker/api.dart';

void main() {
  final bridge = Platform.environment['SOLO_BRIDGE'];
  final skip = (bridge == null || bridge.isEmpty)
      ? 'set SOLO_BRIDGE to a built solo shared library'
      : false;

  test('in-process engine answers /api/state over dart:ffi', skip: skip,
      () async {
    final r = await Api.get('/api/state');
    expect(r.ok, isTrue, reason: r.text);
    expect(r.status, 200);
    expect(r.data is Map, isTrue);
    expect(r.data['running'], isFalse);
    expect(r.data['status'], 'Ready');
  });

  test('plain route round-trips through the bridge', skip: skip, () async {
    final r = await Api.get('/');
    expect(r.ok, isTrue, reason: r.text);
    expect(r.status, 200);
    expect(r.text, contains('SOLO Checker backend running'));
  });

  test('update check route is registered on the FFI mux', skip: skip,
      () async {
    // The launch-time update prompt depends on this route existing in-process.
    // A missing route answers 404; the handler itself always answers 200 JSON.
    final r = await Api.get('/api/update/check');
    expect(r.status, 200, reason: r.text);
    expect(r.data is Map, isTrue, reason: r.text);
    expect(r.data['local'], isNotEmpty);
  });

  test('POST body reaches the handler', skip: skip, () async {
    // pause on a non-running engine answers JSON {ok:false,error:not running}
    final r = await Api.postEmpty('/api/pause');
    expect(r.status, 200);
    expect(r.data is Map, isTrue);
    expect(r.data['ok'], isFalse);
  });
}
