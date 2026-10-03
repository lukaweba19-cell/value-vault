// Guards the modal text-style fix: content rendered by showAppModal must
// inherit the app's DefaultTextStyle. Without a Material ancestor it fell
// back to MaterialApp's loud debug style (red color + yellow double
// underline), which painted red/underlined text in every popup — the saved
// pickers, the update prompt, the editor modals.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:solo_checker/theme.dart';
import 'package:solo_checker/widgets.dart';

void main() {
  testWidgets('modal text inherits the app style, not the error fallback',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
      debugShowCheckedModeBanner: false,
      theme: buildTheme(),
      home: Builder(builder: (context) {
        return Scaffold(
          backgroundColor: T.bg,
          body: Center(
            child: TextBtn(
              label: 'open',
              onPressed: () => showAppModal<void>(
                context,
                Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text('Saved servers',
                        style: const TextStyle(
                            fontSize: 15, fontWeight: FontWeight.w700)),
                    const SizedBox(height: 12),
                    const Muted('Nothing saved'),
                  ],
                ),
              ),
            ),
          ),
        );
      }),
    ));
    await tester.tap(find.text('open'));
    await tester.pump(const Duration(milliseconds: 300));
    await tester.pump(const Duration(milliseconds: 300));

    for (final label in ['Saved servers', 'Nothing saved']) {
      final dts = DefaultTextStyle.of(tester.element(find.text(label)));
      expect(dts.style.decoration ?? TextDecoration.none,
          TextDecoration.none,
          reason: '$label inherits an underline decoration');
      expect(dts.style.color, isNot(const Color(0xD0FF0000)),
          reason: '$label inherits the fallback red');
      expect(dts.style.color, T.txt,
          reason: '$label should inherit the app body color');
    }
  });
}
