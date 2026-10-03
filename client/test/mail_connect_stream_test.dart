// Regression for the mail viewer's "connection ended unexpectedly" failure:
// the /api/mail/connect NDJSON stream must always end with a terminal
// {"type":"done"} frame. A lost chunk (the engine used to free stream
// buffers before this async callback read them) or a silent stream end left
// `done` null and the UI fell back to a generic error.
//
// Skipped unless SOLO_BRIDGE points at a built shared library, e.g.:
//   SOLO_BRIDGE=/tmp/xbox_checker.so flutter test test/mail_connect_stream_test.dart
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:solo_checker/api.dart';

void main() {
  final bridge = Platform.environment['SOLO_BRIDGE'];
  final skip = (bridge == null || bridge.isEmpty)
      ? 'set SOLO_BRIDGE to a built solo shared library'
      : false;

  test('mail connect stream always ends with a done frame',
      skip: skip, timeout: const Timeout(Duration(minutes: 3)), () async {
    final stream = await Api.ndjson('/api/mail/connect', {
      'email': 'stream-probe@example.com',
      'password': 'not-a-real-password',
    });
    final events = <Map<String, dynamic>>[];
    Map<String, dynamic>? done;
    // timeout(): a gap of >100s between lines means the stream hung —
    // exactly the "infinite loading" the connect deadline now prevents.
    await stream.timeout(const Duration(seconds: 100)).forEach((e) {
      events.add(e);
      if (e['type'] == 'done') done = e;
    });
    expect(done, isNotNull,
        reason: 'stream closed without a terminal done frame: $events');
    expect(done!['ok'], isFalse,
        reason: 'fake credentials must not sign in: $done');
  });
}
