// Guards the Valid Viewer header/topbar layout pass:
//  * the big "Valid Viewer" title must stay on ONE line — beside the
//    Reload/Export buttons in a Row it was squeezed to ~40pt and broke
//    mid-word ("Valid / View / er") on phones;
//  * the topbar drops the "SOLO Checker /" prefix below 420pt so the page
//    name is not crowded into an ellipsis ("Vali…");
//  * desktop (>= 420pt, the Windows window) keeps the brand prefix.
//
// The viewer opens the engine's event stream in initState, which needs the
// bridge — like ffi_test this test is skipped unless SOLO_BRIDGE points at
// a built shared library, e.g.:
//   SOLO_BRIDGE=/tmp/xbox_checker.so flutter test
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:solo_checker/theme.dart';
import 'package:solo_checker/views/viewer.dart';

Future<void> _pumpViewer(WidgetTester tester, Size size) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MaterialApp(
    debugShowCheckedModeBanner: false,
    theme: buildTheme(),
    home: const ViewerScreen(),
  ));
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 100));
}

/// Lets in-flight Isolate.run calls finish on the real event loop, then
/// flushes their completions so Api's guard timers are cancelled —
/// otherwise flutter_test fails the test with a pending-timer error.
Future<void> _settleEngineCalls(WidgetTester tester) async {
  await tester
      .runAsync(() => Future<void>.delayed(const Duration(milliseconds: 750)));
  await tester.pump();
}

/// Every occurrence of the page name (topbar, header, status bar) must be
/// laid out on a single line. One line measures ~1.45x the font size
/// (the header's 22px renders 32px tall); any wrap doubles that, so 2x
/// the font size cleanly separates the two regardless of test-font
/// metrics.
void _expectNoWrapping(WidgetTester tester, Finder titles) {
  expect(titles, findsNWidgets(3),
      reason: 'topbar + header + status bar all render the page name');
  for (var i = 0; i < titles.evaluate().length; i++) {
    final size = tester.getSize(titles.at(i));
    final fontSize = tester.widget<Text>(titles.at(i)).style?.fontSize ?? 0;
    expect(size.height, lessThan(fontSize * 2),
        reason: 'fontSize $fontSize wrapped to $size — the name must stay '
            'on one line');
  }
}

void main() {
  final bridge = Platform.environment['SOLO_BRIDGE'];
  final skip = bridge == null || bridge.isEmpty;

  testWidgets('phone width: whole name, no mid-word title break',
      skip: skip, (tester) async {
    await _pumpViewer(tester, const Size(390, 844));

    // The narrow topbar shows the name only — the logo keeps the branding
    // and the prefix stops squeezing the name into "Vali…".
    expect(find.text('SOLO Checker'), findsNothing,
        reason: 'prefix must drop below 420pt so the page name fits');

    final titles = find.text('Valid Viewer');
    _expectNoWrapping(tester, titles);

    // The header actions survive the Row → Wrap reflow.
    expect(find.text('Reload file'), findsOneWidget);
    expect(find.text('Export filtered combos'), findsOneWidget);

    await _settleEngineCalls(tester);
  });

  testWidgets('desktop width: brand prefix kept, name still whole',
      skip: skip, (tester) async {
    await _pumpViewer(tester, const Size(1280, 800));

    expect(find.text('SOLO Checker'), findsOneWidget,
        reason: 'wide bars keep the brand prefix');
    _expectNoWrapping(tester, find.text('Valid Viewer'));

    expect(find.text('Reload file'), findsOneWidget);
    expect(find.text('Export filtered combos'), findsOneWidget);

    await _settleEngineCalls(tester);
  });
}
