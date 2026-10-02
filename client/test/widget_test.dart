import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:solo_checker/theme.dart';

void main() {
  test('theme keeps the SOLO Checker dark navy palette', () {
    final theme = buildTheme();

    expect(theme.scaffoldBackgroundColor, T.bg);
    expect(theme.colorScheme.primary, T.acc);
    expect(theme.colorScheme.surface, T.card);
    expect(theme.scaffoldBackgroundColor, const Color(0xFF07090E));
  });

  test('design tokens match theme.css custom properties', () {
    expect(T.card, const Color(0xFF121722));
    expect(T.line, const Color(0xFF2A3348));
    expect(T.txt, const Color(0xFFE8EDF7));
    expect(T.mut, const Color(0xFF8B97AD));
    expect(T.grn, const Color(0xFF22C55E));
  });
}
