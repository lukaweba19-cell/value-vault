// Guards the mail HTML crash fix: flutter_html 3.0.0's DeclarationVisitor
// throws LateInitializationError (`_currentProperty` uninitialized) when a
// <style> sheet contains an @media/@supports expression before its first
// plain declaration — the build error that greyed out the mail viewer on
// device. wrapMailHTML must strip at-rules from <style> blocks while
// keeping plain rules and inline style="" attributes intact.
import 'package:flutter/material.dart';
import 'package:flutter_html/flutter_html.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:solo_checker/views/viewer.dart';

const _hostileDoc = '<html><head><style>'
    '@media (max-width:600px){.hdr{background:#222}td{display:block}}'
    '@supports (display:grid){.g{display:grid}}'
    '@import url("more.css");'
    '.keep{color:#333;font-size:14px}'
    '</style></head><body>'
    '<p style="font-size:32px;color:#333">Hello</p>'
    '<div class="keep">Body text</div>'
    '</body></html>';

void main() {
  test('wrapMailHTML strips crashing at-rules from full documents', () {
    final out = wrapMailHTML(_hostileDoc, '');
    expect(out, isNot(contains('@media')),
        reason: '@media expressions crash flutter_html\u2019s CSS visitor');
    expect(out, isNot(contains('@supports')));
    expect(out, isNot(contains('@import')));
    expect(out, contains('.keep{color:#333;font-size:14px}'),
        reason: 'plain rules must survive — they style the message');
    expect(out, contains('style="font-size:32px;color:#333"'),
        reason: 'inline attributes are parsed on the safe declaration path');
    expect(out, contains('Body text'));
  });

  test('wrapMailHTML strips at-rules on the preview-wrapped path too', () {
    // No <html> marker: the body is assembled locally, but a raw <style>
    // inside the fragment must still be sanitized.
    final out = wrapMailHTML(
        '<style>@media screen{p{color:red}}</style><p>hi</p>', 'prev');
    expect(out, isNot(contains('@media')));
    expect(out, contains('<p>hi</p>'));
  });

  test('at-rule stripping keeps comments, strings and nested braces', () {
    final out = wrapMailHTML(
        '<html><head><style>'
        '/* @media noted in a comment */'
        'a::after{content:"}not a brace{"}'
        '@font-face{font-family:X;src:url(x.woff2)}'
        'a{color:red}'
        '</style></head><body>x</body></html>',
        '');
    expect(out, contains('/* @media noted in a comment */'));
    expect(out, contains('content:"}not a brace{"'));
    expect(out, isNot(contains('@font-face')));
    expect(out, contains('a{color:red}'));
  });

  testWidgets('the sanitized HTML builds in flutter_html without throwing',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
      debugShowCheckedModeBanner: false,
      home: Scaffold(
        body: Html(data: wrapMailHTML(_hostileDoc, '')),
      ),
    ));
    await tester.pump();
    expect(find.byType(ErrorWidget), findsNothing,
        reason: 'the CSS visitor must not throw while building the message');
    expect(find.textContaining('Body text', findRichText: true), findsOneWidget,
        reason: 'message content must render');
  });
}
