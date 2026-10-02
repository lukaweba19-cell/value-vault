// SOLO Checker — design system ported from internal/ui/assets/theme.css.
// Dark navy base, purple accent, Xbox-green title accents, Segoe UI.
import 'package:flutter/material.dart';

/// Color tokens, byte-identical to the CSS custom properties.
class T {
  static const bg = Color(0xFF07090E);
  static const bgSoft = Color(0xFF0C1018);
  static const card = Color(0xFF121722);
  static const card2 = Color(0xFF0E1320);
  static const cardHi = Color(0xFF151B2A);
  static const line = Color(0xFF2A3348);
  static const lineSoft = Color(0xFF222A3A);
  static const lineHi = Color(0xFF3D4A63);
  static const txt = Color(0xFFE8EDF7);
  static const mut = Color(0xFF8B97AD);
  static const pur = Color(0xFFC084FC);
  static const grn = Color(0xFF22C55E);
  static const blu = Color(0xFF38BDF8);
  static const acc = Color(0xFF7C5CFF);
  static const acc2 = Color(0xFF6A45FF);
  static const red = Color(0xFFF87171);
  static const amber = Color(0xFFFFD48A);
  static const shadowBase = Color(0x57000000);

  // Tag palette (from .tag.* rules)
  static const tagPurpleBg = Color(0x267C5CFF);
  static const tagPurpleFg = Color(0xFFCBBFFF);
  static const tagPurpleBd = Color(0x597C5CFF);
  static const tagGreenBg = Color(0x1F3DDC97);
  static const tagGreenFg = Color(0xFF9AF0C8);
  static const tagGreenBd = Color(0x4D3DDC97);
  static const tagWarnBg = Color(0x1FFFB020);
  static const tagWarnFg = Color(0xFFFFD48A);
  static const tagWarnBd = Color(0x4DFFB020);
  static const tagRedBg = Color(0x24F87171);
  static const tagRedFg = Color(0xFFFCA5A5);
  static const tagRedBd = Color(0x66F87171);
  static const tagBlueBg = Color(0x2460A5FA);
  static const tagBlueFg = Color(0xFF93C5FD);
  static const tagBlueBd = Color(0x6660A5FA);
  static const tagYellowBg = Color(0x24FACC15);
  static const tagYellowFg = Color(0xFFFDE047);
  static const tagYellowBd = Color(0x66FACC15);
  static const tagOrangeBg = Color(0x24FB923C);
  static const tagOrangeFg = Color(0xFFFDBA74);
  static const tagOrangeBd = Color(0x66FB923C);
}

/// Typography helpers matching .eyebrow / .muted / label rules.
TextStyle eyebrowStyle([Color? color]) => TextStyle(
      fontSize: 11,
      fontWeight: FontWeight.w700,
      letterSpacing: 1.32,
      color: color ?? T.mut,
    );

TextStyle mutedStyle([double size = 11.5, Color? color]) => TextStyle(
      fontSize: size,
      fontWeight: FontWeight.w400,
      letterSpacing: 0.46,
      color: color ?? T.mut,
    );

TextStyle labelStyle([Color? color]) => TextStyle(
      fontSize: 11,
      fontWeight: FontWeight.w700,
      letterSpacing: 0.88,
      color: color ?? T.mut,
    );

InputDecoration fieldInputDecoration({String? hint}) => InputDecoration(
      isDense: true,
      hintText: hint,
      hintStyle: const TextStyle(color: Color(0xFF5B6880)),
      filled: true,
      fillColor: T.bgSoft,
      contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 11),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(10),
        borderSide: const BorderSide(color: T.line),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(10),
        borderSide: const BorderSide(color: T.acc, width: 1.2),
      ),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(10),
        borderSide: const BorderSide(color: T.line),
      ),
    );

ThemeData buildTheme() {
  const scheme = ColorScheme.dark(
    primary: T.acc,
    secondary: T.acc2,
    surface: T.card,
    onSurface: T.txt,
    error: T.red,
    onPrimary: Colors.white,
  );
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: T.bg,
    fontFamily: 'Segoe UI',
    splashFactory: InkSparkle.splashFactory,
    textTheme: const TextTheme(
      bodyMedium: TextStyle(fontSize: 14, color: T.txt, height: 1.45),
      bodySmall: TextStyle(fontSize: 12.5, color: T.txt),
      titleMedium: TextStyle(fontSize: 14.5, fontWeight: FontWeight.w700, color: T.txt),
      titleLarge: TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: T.txt),
    ),
    checkboxTheme: CheckboxThemeData(
      fillColor: WidgetStateProperty.resolveWith(
        (states) => states.contains(WidgetState.selected) ? T.acc : Colors.transparent,
      ),
      side: const BorderSide(color: T.lineHi, width: 1.4),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(4)),
    ),
    inputDecorationTheme: InputDecorationTheme(
      isDense: true,
      filled: true,
      fillColor: T.bgSoft,
      contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 11),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(10),
        borderSide: const BorderSide(color: T.line),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(10),
        borderSide: const BorderSide(color: T.acc, width: 1.2),
      ),
      hintStyle: const TextStyle(color: Color(0xFF5B6880)),
    ),
    scrollbarTheme: ScrollbarThemeData(
      thumbColor: WidgetStateProperty.all(T.line),
      radius: const Radius.circular(8),
      thickness: WidgetStateProperty.all(8),
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: T.card,
      surfaceTintColor: Colors.transparent,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: const BorderSide(color: T.line),
      ),
    ),
    snackBarTheme: const SnackBarThemeData(backgroundColor: T.cardHi),
  );
}

/// The page backdrop: radial purple glow over the navy base (body rule).
Widget appBackdrop(Widget child) {
  return DecoratedBox(
    decoration: const BoxDecoration(
      gradient: RadialGradient(
        center: Alignment(0, -1),
        radius: 1.1,
        colors: [Color(0xFF1B1430), T.bg],
        stops: [0.0, 0.55],
      ),
    ),
    child: child,
  );
}
