// Guards the mail-viewer fixes:
//  * `_mainHead` overflowed its Row by 91pt on phones (address squeezed to
//    "jom…") — now an eyebrow-less LayoutBuilder below 430pt;
//  * flutter_html 3.0.0 threw LateInitializationError while building any
//    message whose <style> starts with an @media rule — on device (release)
//    that build error greys out the whole viewer. wrapMailHTML now strips
//    at-rules from <style> blocks.
//
// Needs a stub bridge that answers /api/mail/probe with ok:true (the real
// engine has no saved session), e.g. build a c-shared lib exporting
// SoloCall/SoloFree/SoloInit/SoloCancel and:
//   SOLO_BRIDGE=/tmp/stub_solo.so flutter test test/mail_main_repro_test.dart
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:solo_checker/theme.dart';
import 'package:solo_checker/views/viewer.dart';

void main() {
  final bridge = Platform.environment['SOLO_BRIDGE'] ?? '';
  // testWidgets' skip is bool? — needs the stub bridge, which answers
  // /api/mail/probe with ok:true (the real engine has no saved session).
  final skip = !bridge.contains('stub_solo');

  testWidgets('mail viewer main stage renders its panes', skip: skip,
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    await tester.pumpWidget(MaterialApp(
      debugShowCheckedModeBanner: false,
      theme: buildTheme(),
      home: Scaffold(
        body: MailViewer(
          account: Account(email: 'jom@example.com'),
          onClose: () {},
        ),
      ),
    ));
    await tester.pump();

    // Let the probe isolate call finish, then advance the saved→main timer.
    await tester
        .runAsync(() => Future<void>.delayed(const Duration(milliseconds: 500)));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 1100));
    await tester.pump();

    // The main stage must render the pane switcher and the message list —
    // an exception anywhere in the LayoutBuilder shows up as a failed test
    // with the stack trace, and as finders below failing.
    expect(find.text('Folders'), findsOneWidget,
        reason: 'pane switcher must render below the header');
    expect(find.text('Message'), findsOneWidget);
    expect(find.byType(ErrorWidget), findsNothing,
        reason: 'no widget in the main stage may throw while building');

    // Now tap the first message — the path the user took before the whole
    // body went gray: list → reader pane (subject card + flutter_html body
    // fed by real-world Outlook HTML: <style>/@media, tables, tracking
    // pixels, video/audio, unicode, conditional comments).
    await tester.tap(find.text('Statement for October — account 0').first);
    await tester.pump();
    await tester
        .runAsync(() => Future<void>.delayed(const Duration(milliseconds: 400)));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    await tester.pump();

    expect(find.byType(ErrorWidget), findsNothing,
        reason: 'tapping a message must not throw while building the reader');
    expect(find.text('Folders'), findsOneWidget,
        reason: 'switcher must survive the pane change');
    // The reader must show the message header AND painted body content from
    // the @media-heavy HTML — pre-fix this tap threw the LateInitialization-
    // Error that greyed out the whole viewer on device.
    expect(
        find.text('Statement for October — account 0', findRichText: true),
        findsWidgets,
        reason: 'subject card must render');
    expect(find.textContaining('centered', findRichText: true), findsOneWidget,
        reason: 'message body must actually render');

    await tester
        .runAsync(() => Future<void>.delayed(const Duration(milliseconds: 300)));
  }, timeout: const Timeout(Duration(minutes: 2)));
}
