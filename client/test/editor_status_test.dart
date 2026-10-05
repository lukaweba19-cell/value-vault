// Guards the combo editor's transient status lines: a message produced by
// an action (save/validation) must clear itself after a few seconds, and
// switching tab must wipe it immediately so "Saved" never follows the user
// to another tab.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:solo_checker/theme.dart';
import 'package:solo_checker/views/editor.dart';

const _msg = 'Name and signature text required';

Future<void> _enterSignatures(WidgetTester tester) async {
  await tester.pump(const Duration(milliseconds: 300));
  await tester.tap(find.text('Signatures'));
  await tester.pump(const Duration(milliseconds: 300));
}

void main() {
  testWidgets('editor status messages are transient and tab-scoped',
      (tester) async {
    // The shell hosts views inside a scroll view; the bare test surface is
    // 800x600, which is shorter than the signatures pane. Give it a window.
    tester.view.physicalSize = const Size(1200, 1600);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    await tester.pumpWidget(MaterialApp(
      debugShowCheckedModeBanner: false,
      theme: buildTheme(),
      home: Scaffold(backgroundColor: T.bg, body: EditorView(onHome: () {})),
    ));
    await tester.pump(const Duration(milliseconds: 300));

    // Enter the editor's work view (Create new → Generate + Signatures).
    await tester.tap(find.text('Create new'));
    await tester.pump(const Duration(milliseconds: 300));

    // Empty signature save → validation message on the Signatures tab.
    await _enterSignatures(tester);
    await tester.tap(find.text('Save signature'));
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text(_msg), findsOneWidget,
        reason: 'validation message is shown after the save tap');

    // Leaving the tab wipes it immediately — it must not appear on the
    // Generate tab after switching away.
    await tester.tap(find.text('Generate'));
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text(_msg), findsNothing,
        reason: 'switching tab clears the status line');

    // Produced again: it must clear itself after a few seconds without
    // any navigation.
    await _enterSignatures(tester);
    await tester.tap(find.text('Save signature'));
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text(_msg), findsOneWidget);
    await tester.pump(const Duration(seconds: 3));
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text(_msg), findsNothing,
        reason: 'status line auto-clears after a few seconds');

    // Flush the bounded banner-load call made in initState (Api._call's
    // 120s guard) so no timer is left pending at teardown.
    await tester.pump(const Duration(seconds: 120));
    await tester.pump(const Duration(milliseconds: 300));
  });
}
