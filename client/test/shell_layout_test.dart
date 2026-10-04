// Guards the iPhone layout pass for the app shell:
//  * compact widths (< 900px) drop the fixed 252px sidebar in favour of a
//    hamburger + drawer, so the form is no longer squeezed into a ~70px
//    clipped column;
//  * the topbar/statusbar grow behind the iOS status bar and home indicator;
//  * desktop (>= 900px, the Windows window) keeps the classic sidebar layout.
//
// The shell polls the engine over dart:ffi; without SOLO_BRIDGE every call
// fails inside Api._call and is swallowed, so the widget renders fine (it
// just never goes "live").
//
// Note on 'WORKSPACE': it is rendered by BOTH the sidebar and the home
// view's section eyebrow — 2 matches means the sidebar is on screen,
// 1 match means it is not.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:solo_checker/shell.dart';
import 'package:solo_checker/theme.dart';

Future<void> _pumpShell(WidgetTester tester, Size size,
    {EdgeInsets padding = EdgeInsets.zero}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MaterialApp(
    debugShowCheckedModeBanner: false,
    theme: buildTheme(),
    home: Builder(builder: (context) {
      final mq = MediaQuery.of(context);
      return MediaQuery(
        data: mq.copyWith(padding: padding),
        child: const AppShell(),
      );
    }),
  ));
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 100));
}

/// Lets in-flight Isolate.run calls finish on the real event loop, then
/// flushes their completions so Api's 120s guard timers are cancelled —
/// otherwise flutter_test fails the test with a pending-timer error.
Future<void> _settleEngineCalls(WidgetTester tester) async {
  await tester
      .runAsync(() => Future<void>.delayed(const Duration(milliseconds: 750)));
  await tester.pump();
}

ScaffoldState _scaffold(WidgetTester tester) =>
    tester.state<ScaffoldState>(find.byType(Scaffold));

void main() {
  testWidgets('phone width: menu + drawer instead of a sidebar',
      (tester) async {
    await _pumpShell(
      tester,
      const Size(390, 844),
      padding: const EdgeInsets.only(top: 59, bottom: 34),
    );

    // Hamburger + drawer replace the always-on sidebar.
    expect(find.byTooltip('Menu'), findsOneWidget);
    expect(_scaffold(tester).isDrawerOpen, isFalse);
    expect(find.text('WORKSPACE'), findsOneWidget,
        reason: 'only the home eyebrow — no sidebar beside the content');

    // Topbar title clears the iOS status bar (inset = 59).
    final titleTop = tester.getTopLeft(find.text('Home').first).dy;
    expect(titleTop, greaterThan(59),
        reason: 'title must sit below the status bar inset');

    // Status row clears the home indicator (bottom inset = 34).
    final connectedBottom = tester.getBottomLeft(find.text('connected')).dy;
    expect(connectedBottom, lessThanOrEqualTo(844 - 34),
        reason: 'status row must sit above the home indicator');

    // Open the drawer and navigate through it end-to-end.
    await tester.tap(find.byTooltip('Menu'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 350));
    expect(_scaffold(tester).isDrawerOpen, isTrue,
        reason: 'menu button should open the navigation drawer');

    await tester.tap(find.text('New session'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 350));
    expect(find.text('Run configuration'), findsOneWidget,
        reason: 'drawer nav should reach the New session form');
    expect(_scaffold(tester).isDrawerOpen, isFalse,
        reason: 'picking a nav item should close the drawer');

    await _settleEngineCalls(tester);
  });

  testWidgets('desktop width: classic sidebar, no menu button',
      (tester) async {
    await _pumpShell(tester, const Size(1280, 800));

    expect(find.byTooltip('Menu'), findsNothing);
    expect(_scaffold(tester).isDrawerOpen, isFalse);
    expect(find.text('WORKSPACE'), findsNWidgets(2),
        reason: 'sidebar + home eyebrow — the sidebar stays on the Windows '
            'window');

    // Zero safe-area inset on desktop: the bar keeps its original 46px top.
    final titleTop = tester.getTopLeft(find.text('Home').first).dy;
    expect(titleTop, lessThan(46),
        reason: 'desktop topbar must not grow an inset');

    await _settleEngineCalls(tester);
  });
}
