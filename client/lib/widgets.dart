// Shared widgets: SVG icons (paths byte-exact from app.mjs), buttons, tags,
// modal, section heads, choice cards — Flutter equivalents of the web kit.
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_svg/flutter_svg.dart';

import 'theme.dart';

/* ---------------- icons (path data byte-exact from the original UI) -------- */

class IconNew extends StatelessWidget {
  final double size;
  const IconNew({super.key, this.size = 18});
  @override
  Widget build(BuildContext c) => _StrokeIcon('M12 5v14M5 12h14', size, T.grn);
}

class IconResume extends StatelessWidget {
  final double size;
  const IconResume({super.key, this.size = 18});
  @override
  Widget build(BuildContext c) => _StrokeIcon(
      'M4 12a8 8 0 101.7-4.9', size, T.pur,
      second: 'M4 5v5h5');
}

class IconEditor extends StatelessWidget {
  final double size;
  const IconEditor({super.key, this.size = 18});
  @override
  Widget build(BuildContext c) => _StrokeIcon(
      'M4 20h4l10-10-4-4L4 16v4z', size, T.blu,
      second: 'M13 7l4 4');
}

class IconList extends StatelessWidget {
  final double size;
  const IconList({super.key, this.size = 14});
  @override
  Widget build(BuildContext c) =>
      _StrokeIcon('M4 6h16M4 12h16M4 18h10', size, Colors.white);
}

class IconBack extends StatelessWidget {
  final double size;
  const IconBack({super.key, this.size = 14});
  @override
  Widget build(BuildContext c) => _StrokeIcon(
      'M15 18l-6-6 6-6', size, T.txt,
      joins: true);
}

class IconNext extends StatelessWidget {
  final double size;
  const IconNext({super.key, this.size = 14});
  @override
  Widget build(BuildContext c) => _StrokeIcon(
      'M5 12h14M13 6l6 6-6 6', size, Colors.white,
      joins: true);
}

class IconSave extends StatelessWidget {
  final double size;
  const IconSave({super.key, this.size = 16});
  @override
  Widget build(BuildContext c) => _StrokeIcon(
      'M12 3v12M8 11l4 4 4-4M4 21h16', size, T.txt,
      joins: true);
}

class IconDownload extends StatelessWidget {
  final double size;
  const IconDownload({super.key, this.size = 18});
  @override
  Widget build(BuildContext c) => _StrokeIcon(
      'M4 20h16M12 4v12M8 12l4 4 4-4', size, T.blu,
      joins: true);
}

class IconPlay extends StatelessWidget {
  final double size;
  const IconPlay({super.key, this.size = 14});
  @override
  Widget build(BuildContext c) => CustomPaint(
        size: Size(size, size),
        painter: _FillPainter(
            'M8 5v14l11-7z', Colors.white, 24),
      );
}

class IconHome extends StatelessWidget {
  final double size;
  final Color color;
  const IconHome({super.key, this.size = 14, this.color = Colors.white});
  @override
  Widget build(BuildContext c) => _StrokeIcon(
      'M4 11l8-7 8 7v8a1.5 1.5 0 01-1.5 1.5h-13A1.5 1.5 0 014 19v-8z',
      size,
      color,
      second: 'M10 20.5v-6h4v6',
      joins: true);
}

/// Minimal SVG-path painter: supports M/l/z style absolute+relative segments
/// used by the original icon set.
class _StrokePainter extends CustomPainter {
  final String d;
  final Color color;
  final double width;
  final bool joins;
  _StrokePainter(this.d, this.color, this.width, {this.joins = false});

  @override
  void paint(Canvas canvas, Size size) {
    final scale = size.width / 24;
    final path = _parsePath(d);
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = width
      ..strokeCap = StrokeCap.round
      ..strokeJoin = joins ? StrokeJoin.round : StrokeJoin.miter;
    canvas.save();
    canvas.scale(scale);
    canvas.drawPath(path, paint);
    canvas.restore();
  }

  @override
  bool shouldRepaint(covariant _StrokePainter old) =>
      old.d != d || old.color != color || old.width != width;
}

class _FillPainter extends CustomPainter {
  final String d;
  final Color color;
  final double vw;
  _FillPainter(this.d, this.color, this.vw);

  @override
  void paint(Canvas canvas, Size size) {
    final scale = size.width / vw;
    final path = _parsePath(d);
    final paint = Paint()..color = color..style = PaintingStyle.fill;
    canvas.save();
    canvas.scale(scale);
    canvas.drawPath(path, paint);
    canvas.restore();
  }

  @override
  bool shouldRepaint(covariant _FillPainter old) => old.d != d;
}

/// Tiny SVG path parser (M, L, H, V, Z + relative m/l/h/v/z).
///
/// Tokenizes by regex instead of whitespace: the icon paths are compact SVG
/// (`M12 5v14M5 12h14`) where letters are glued to their numbers — the old
/// whitespace split produced garbage tokens and drew NOTHING, which emptied
/// every icon box in the app.
Path _parsePath(String d) {
  final path = Path();
  final tokens = RegExp(r'[A-Za-z]|-?\d*\.?\d+(?:e-?\d+)?')
      .allMatches(d)
      .map((m) => m.group(0)!)
      .toList();
  double cx = 0, cy = 0;
  double sx = 0, sy = 0;
  String cmd = '';
  var i = 0;
  double num() {
    if (i >= tokens.length) return 0;
    final v = double.tryParse(tokens[i]) ?? 0;
    i++;
    return v;
  }

  while (i < tokens.length) {
    final t = tokens[i];
    if (t.length == 1 && RegExp(r'^[A-Za-z]$').hasMatch(t)) {
      cmd = t;
      i++;
    } else if (cmd.isEmpty) {
      i++;
      continue;
    }
    switch (cmd) {
      case 'M':
        cx = num();
        cy = num();
        path.moveTo(cx, cy);
        sx = cx;
        sy = cy;
        cmd = 'L';
        break;
      case 'm':
        cx += num();
        cy += num();
        path.moveTo(cx, cy);
        sx = cx;
        sy = cy;
        cmd = 'l';
        break;
      case 'L':
        cx = num();
        cy = num();
        path.lineTo(cx, cy);
        break;
      case 'l':
        cx += num();
        cy += num();
        path.lineTo(cx, cy);
        break;
      case 'H':
        cx = num();
        path.lineTo(cx, cy);
        break;
      case 'h':
        cx += num();
        path.lineTo(cx, cy);
        break;
      case 'V':
        cy = num();
        path.lineTo(cx, cy);
        break;
      case 'v':
        cy += num();
        path.lineTo(cx, cy);
        break;
      case 'Z':
      case 'z':
        path.close();
        cx = sx;
        cy = sy;
        break;
      case 'A':
      case 'a':
        i += 7;
        break;
      case 'C':
      case 'c':
        i += 6;
        break;
      case 'S':
      case 's':
      case 'Q':
      case 'q':
        i += 4;
        break;
      case 'T':
      case 't':
        i += 2;
        break;
      default:
        i++;
    }
  }
  return path;
}

class _StrokeIcon extends StatelessWidget {
  final String d;
  final String? second;
  final double size;
  final Color color;
  final bool joins;
  const _StrokeIcon(this.d, this.size, this.color,
      {this.second, this.joins = false});

  @override
  Widget build(BuildContext c) => CustomPaint(
        size: Size(size, size),
        painter: _MultiStrokePainter([
          _StrokePainter(d, color, 2, joins: joins),
          if (second != null)
            _StrokePainter(second!, color, 2, joins: joins),
        ]),
      );
}

class _MultiStrokePainter extends CustomPainter {
  final List<_StrokePainter> painters;
  _MultiStrokePainter(this.painters);
  @override
  void paint(Canvas canvas, Size size) {
    for (final p in painters) {
      p.paint(canvas, size);
    }
  }

  @override
  bool shouldRepaint(covariant _MultiStrokePainter old) => true;
}

/* ---------------- brand marks (SVG strings from app.mjs) ------------------- */

const xboxSvg = '<svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg"><circle opacity="0.852" cx="16" cy="16" r="14" fill="url(#xb0)"/><path d="M6.75137 26.5333C6.73227 26.5193 6.7127 26.5016 6.69312 26.4801C6.33073 26.1504 5.74045 25.6304 5.12362 24.8444C4.5909 23.6889 4.89287 21.748 6.20386 19.0075C8.05357 15.1409 11.0131 11.2151 12.2265 9.8225C11.8813 9.43732 10.8119 8.38253 9.29659 7.24477C7.7813 6.10701 6.67738 5.99047 6.29264 6.05961C6.64779 5.67443 7.47646 4.94852 8.17196 4.45964C10.3265 3.39299 14.3081 5.19543 16.0148 6.22257V13.2891C11.6642 16.6372 7.63924 21.8075 6.82536 23.8222C6.20418 25.36 6.44975 26.2128 6.69312 26.4801C6.71325 26.4984 6.73269 26.5161 6.75137 26.5333Z" fill="url(#xb1)"/><path d="M25.2779 26.5333C25.297 26.5193 25.3166 26.5016 25.3362 26.4801C25.6986 26.1504 26.2888 25.6304 26.9057 24.8444C27.4384 23.6889 27.1364 21.748 25.8254 19.0075C23.9757 15.1409 21.0162 11.2151 19.8028 9.8225C20.148 9.43732 21.2174 8.38253 22.7327 7.24477C24.248 6.10701 25.3519 5.99047 25.7367 6.05961C25.3815 5.67443 24.5528 4.94852 23.8573 4.45964C21.7028 3.39299 17.7212 5.19543 16.0145 6.22257V13.2891C20.3651 16.6372 24.3901 21.8075 25.2039 23.8222C25.8251 25.36 25.5796 26.2128 25.3362 26.4801C25.316 26.4984 25.2966 26.5161 25.2779 26.5333Z" fill="url(#xb3)"/><defs><radialGradient id="xb0" cx="0" cy="0" r="1" gradientUnits="userSpaceOnUse" gradientTransform="translate(16 14.5) rotate(90.0548) scale(15.5)"><stop stop-color="#FAFAFA"/><stop offset="1" stop-color="#879288"/></radialGradient><linearGradient id="xb1" x1="12.99" y1="4.72" x2="13.22" y2="12.78" gradientUnits="userSpaceOnUse"><stop stop-color="#008B00"/><stop offset="1" stop-color="#6CC329"/></linearGradient><linearGradient id="xb3" x1="19.03" y1="4.72" x2="18.79" y2="12.78" gradientUnits="userSpaceOnUse"><stop stop-color="#008B00"/><stop offset="1" stop-color="#6CC329"/></linearGradient></defs></svg>';

const _tgPlane =
    'M22.9866 10.2088C23.1112 9.40332 22.3454 8.76755 21.6292 9.082L7.36482 15.3448C6.85123 15.5703 6.8888 16.3483 7.42147 16.5179L10.3631 17.4547C10.9246 17.6335 11.5325 17.541 12.0228 17.2023L18.655 12.6203C18.855 12.4821 19.073 12.7665 18.9021 12.9426L14.1281 17.8646C13.665 18.3421 13.7569 19.1512 14.314 19.5005L19.659 22.8523C20.2585 23.2282 21.0297 22.8506 21.1418 22.1261L22.9866 10.2088Z';

String tgSvg(String gid) =>
    '<svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg"><circle cx="16" cy="16" r="14" fill="url(#${gid}c)"/><path d="$_tgPlane" fill="white"/><defs><linearGradient id="${gid}c" x1="16" y1="2" x2="16" y2="30" gradientUnits="userSpaceOnUse"><stop stop-color="#37BBFE"/><stop offset="1" stop-color="#007DBB"/></linearGradient></defs></svg>';

const epicSvg = '<svg fill="#c8c8c8" role="img" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M3.537 0C2.165 0 1.66.506 1.66 1.879V18.44a4.262 4.262 0 00.02.433c.031.3.037.59.316.92.027.033.311.245.311.245.153.075.258.13.43.2l8.335 3.491c.433.199.614.276.928.27h.002c.314.006.495-.071.928-.27l8.335-3.492c.172-.07.277-.124.43-.2 0 0 .284-.211.311-.243.28-.33.285-.621.316-.92a4.261 4.261 0 00.02-.434V1.879c0-1.373-.506-1.88-1.878-1.88zm13.366 3.11h.68c1.138 0 1.688.553 1.688 1.696v1.88h-1.374v-1.8c0-.369-.17-.54-.523-.54h-.235c-.367 0-.537.17-.537.539v5.81c0 .369.17.54.537.54h.262c.353 0 .523-.171.523-.54V8.619h1.373v2.143c0 1.144-.562 1.71-1.7 1.71h-.694c-1.138 0-1.7-.566-1.7-1.71V4.82c0-1.144.562-1.709 1.7-1.709zm-12.186.08h3.114v1.274H6.117v2.603h1.648v1.275H6.117v2.774h1.74v1.275h-3.14zm3.816 0h2.198c1.138 0 1.7.564 1.7 1.708v2.445c0 1.144-.562 1.71-1.7 1.71h-.799v3.338h-1.4zm4.53 0h1.4v9.201h-1.4zm-3.13 1.235v3.392h.575c.354 0 .523-.171.523-.54V4.965c0-.368-.17-.54-.523-.54zm-3.74 10.147a1.708 1.708 0 01.591.108 1.745 1.745 0 01.49.299l-.452.546a1.247 1.247 0 00-.308-.195.91.91 0 00-.363-.068.658.658 0 00-.28.06.703.703 0 00-.224.163.783.783 0 00-.151.243.799.799 0 00-.056.299v.008a.852.852 0 00.056.31.7.7 0 00.157.245.736.736 0 00.238.16.774.774 0 00.303.058.79.79 0 00.445-.116v-.339h-.548v-.565H7.37v1.255a2.019 2.019 0 01-.524.307 1.789 1.789 0 01-.683.123 1.642 1.642 0 01-.602-.107 1.46 1.46 0 01-.478-.3 1.371 1.371 0 01-.318-.455 1.438 1.438 0 01-.115-.58v-.008a1.426 1.426 0 01.113-.57 1.449 1.449 0 01.312-.46 1.418 1.418 0 01.474-.309 1.58 1.58 0 01.598-.111 1.708 1.708 0 01.045 0zm11.963.008a2.006 2.006 0 01.612.094 1.61 1.61 0 01.507.277l-.386.546a1.562 1.562 0 00-.39-.205 1.178 1.178 0 00-.388-.07.347.347 0 00-.208.052.154.154 0 00-.07.127v.008a.158.158 0 00.022.084.198.198 0 00.076.066.831.831 0 00.147.06c.062.02.14.04.236.061a3.389 3.389 0 01.43.122 1.292 1.292 0 01.328.17.678.678 0 01.207.24.739.739 0 01.071.337v.008a.865.865 0 01-.081.382.82.82 0 01-.229.285 1.032 1.032 0 01-.353.18 1.606 1.606 0 01-.46.061 2.16 2.16 0 01-.71-.116 1.718 1.718 0 01-.593-.346l.43-.514c.277.223.578.335.9.335a.457.457 0 00.236-.05.157.157 0 00.082-.142v-.008a.15.15 0 00-.02-.077.204.204 0 00-.073-.066.753.753 0 00-.143-.062 2.45 2.45 0 00-.233-.062 5.036 5.036 0 01-.413-.113 1.26 1.26 0 01-.331-.16.72.72 0 01-.222-.243.73.73 0 01-.082-.36v-.008a.863.863 0 01.074-.359.794.794 0 01.214-.283 1.007 1.007 0 01.34-.185 1.423 1.423 0 01.448-.066 2.006 2.006 0 01.025 0zm-9.358.025h.742l1.183 2.81h-.825l-.203-.499H8.623l-.198.498h-.81zm2.197.02h.814l.663 1.08.663-1.08h.814v2.79h-.766v-1.602l-.711 1.091h-.016l-.707-1.083v1.593h-.754zm3.469 0h2.235v.658h-1.473v.422h1.334v.61h-1.334v.442h1.493v.658h-2.255zm-5.3.897l-.315.793h.624zm-1.145 5.19h8.014l-4.09 1.348z"/></svg>';

class XboxMark extends StatelessWidget {
  final double size;
  const XboxMark({super.key, this.size = 32});
  @override
  Widget build(BuildContext c) =>
      SvgPicture.string(xboxSvg, width: size, height: size);
}

class TgMark extends StatelessWidget {
  final double size;
  final String gid;
  const TgMark({super.key, this.size = 42, this.gid = 'tg0'});
  @override
  Widget build(BuildContext c) =>
      SvgPicture.string(tgSvg(gid), width: size, height: size);
}

class EpicMark extends StatelessWidget {
  final double size;
  const EpicMark({super.key, this.size = 24});
  @override
  Widget build(BuildContext c) =>
      SvgPicture.string(epicSvg, width: size * 0.88, height: size);
}

/* ---------------- buttons -------------------------------------------------- */

enum BtnKind { primary, ghost, ghostOn, danger, secondary, secondaryOn }

class Btn extends StatefulWidget {
  final Widget child;
  final BtnKind kind;
  final VoidCallback? onPressed;
  final bool disabled;
  final EdgeInsets padding;
  const Btn({
    super.key,
    required this.child,
    this.kind = BtnKind.ghost,
    this.onPressed,
    this.disabled = false,
    this.padding = const EdgeInsets.symmetric(horizontal: 15, vertical: 8),
  });

  @override
  State<Btn> createState() => _BtnState();
}

class _BtnState extends State<Btn> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final disabled = widget.disabled || widget.onPressed == null;
    late Color bg;
    late Color border;
    late Color fg;
    final on = widget.kind == BtnKind.ghostOn || widget.kind == BtnKind.secondaryOn;
    switch (widget.kind) {
      case BtnKind.primary:
        bg = const Color(0xFF8B6CFF);
        border = Colors.transparent;
        fg = Colors.white;
        break;
      case BtnKind.danger:
        bg = const Color(0xFF7F1D1D);
        border = T.red;
        fg = const Color(0xFFFECACA);
        break;
      case BtnKind.secondary:
      case BtnKind.secondaryOn:
        bg = on ? const Color(0xFF8B6CFF) : const Color(0xFF1A2132);
        border = on ? Colors.transparent : T.line;
        fg = Colors.white;
        break;
      case BtnKind.ghost:
      case BtnKind.ghostOn:
        bg = on
            ? const Color(0xFF8B6CFF)
            : _hover
                ? const Color(0xFF222B40)
                : const Color(0xFF1A2132);
        border = on
            ? Colors.transparent
            : _hover
                ? T.lineHi
                : T.line;
        fg = Colors.white;
        break;
    }
    final gradient = (widget.kind == BtnKind.primary || on)
        ? const LinearGradient(
            begin: Alignment.topCenter,
            end: Alignment.bottomCenter,
            colors: [Color(0xFF8B6CFF), T.acc2])
        : null;

    return MouseRegion(
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      cursor: disabled ? SystemMouseCursors.basic : SystemMouseCursors.click,
      child: Opacity(
        opacity: disabled ? 0.55 : 1,
        child: AnimatedScale(
          duration: const Duration(milliseconds: 90),
          scale: _hover && !disabled ? 1 : 1,
          child: Material(
            color: Colors.transparent,
            child: Ink(
              decoration: BoxDecoration(
                gradient: gradient,
                color: gradient == null ? bg : null,
                border: Border.all(color: border),
                borderRadius: BorderRadius.circular(10),
                boxShadow: widget.kind == BtnKind.primary || on
                    ? const [
                        BoxShadow(
                            color: Color(0x596A45FF),
                            blurRadius: 10,
                            offset: Offset(0, 2))
                      ]
                    : null,
              ),
              child: InkWell(
                borderRadius: BorderRadius.circular(10),
                onTap: disabled ? null : widget.onPressed,
                child: Padding(
                  padding: widget.padding,
                  child: DefaultTextStyle(
                    style: TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                      color: fg,
                      height: 1.2,
                    ),
                    child: widget.child,
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Simple text button that avoids Row overhead for plain labels.
class TextBtn extends StatelessWidget {
  final String label;
  final BtnKind kind;
  final VoidCallback? onPressed;
  final bool disabled;
  final Widget? icon;
  final EdgeInsets padding;
  const TextBtn({
    super.key,
    required this.label,
    this.kind = BtnKind.ghost,
    this.onPressed,
    this.disabled = false,
    this.icon,
    this.padding = const EdgeInsets.symmetric(horizontal: 15, vertical: 8),
  });

  @override
  Widget build(BuildContext context) => Btn(
        kind: kind,
        onPressed: onPressed,
        disabled: disabled,
        padding: padding,
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (icon != null) ...[icon!, const SizedBox(width: 7)],
            Text(label),
          ],
        ),
      );
}

/* ---------------- tag ------------------------------------------------------ */

class Tag extends StatelessWidget {
  final String text;
  final Widget? child;
  final String kind; // '', green, warn, red, blue, orange, yellow
  final bool bold;
  const Tag({super.key, this.text = '', this.child, this.kind = '', this.bold = false});

  @override
  Widget build(BuildContext context) {
    Color bg, fg, bd;
    switch (kind) {
      case 'green':
        bg = T.tagGreenBg;
        fg = T.tagGreenFg;
        bd = T.tagGreenBd;
        break;
      case 'warn':
        bg = T.tagWarnBg;
        fg = T.tagWarnFg;
        bd = T.tagWarnBd;
        break;
      case 'red':
        bg = T.tagRedBg;
        fg = T.tagRedFg;
        bd = T.tagRedBd;
        break;
      case 'blue':
        bg = T.tagBlueBg;
        fg = T.tagBlueFg;
        bd = T.tagBlueBd;
        break;
      case 'yellow':
        bg = T.tagYellowBg;
        fg = T.tagYellowFg;
        bd = T.tagYellowBd;
        break;
      case 'orange':
        bg = T.tagOrangeBg;
        fg = T.tagOrangeFg;
        bd = T.tagOrangeBd;
        break;
      default:
        bg = T.tagPurpleBg;
        fg = T.tagPurpleFg;
        bd = T.tagPurpleBd;
    }
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(
        color: bg,
        border: Border.all(color: bd),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        child != null ? '' : text,
        style: TextStyle(
          fontSize: 11,
          fontWeight: bold ? FontWeight.w700 : FontWeight.w600,
          color: fg,
        ),
      ),
    );
  }
}

/// Tag whose content is an arbitrary widget (for rich children like `b`).
class TagW extends StatelessWidget {
  final Widget child;
  final String kind;
  const TagW({super.key, required this.child, this.kind = ''});
  @override
  Widget build(BuildContext context) {
    // Reuse Tag's palette by wrapping in the same container:
    return Builder(builder: (context) {
      Color bg, fg, bd;
      switch (kind) {
        case 'green':
          bg = T.tagGreenBg;
          fg = T.tagGreenFg;
          bd = T.tagGreenBd;
          break;
        case 'warn':
          bg = T.tagWarnBg;
          fg = T.tagWarnFg;
          bd = T.tagWarnBd;
          break;
        case 'red':
          bg = T.tagRedBg;
          fg = T.tagRedFg;
          bd = T.tagRedBd;
          break;
        case 'blue':
          bg = T.tagBlueBg;
          fg = T.tagBlueFg;
          bd = T.tagBlueBd;
          break;
        case 'yellow':
          bg = T.tagYellowBg;
          fg = T.tagYellowFg;
          bd = T.tagYellowBd;
          break;
        case 'orange':
          bg = T.tagOrangeBg;
          fg = T.tagOrangeFg;
          bd = T.tagOrangeBd;
          break;
        default:
          bg = T.tagPurpleBg;
          fg = T.tagPurpleFg;
          bd = T.tagPurpleBd;
      }
      return Container(
        padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
        decoration: BoxDecoration(
          color: bg,
          border: Border.all(color: bd),
          borderRadius: BorderRadius.circular(999),
        ),
        child: DefaultTextStyle(
          style: TextStyle(fontSize: 11, fontWeight: FontWeight.w600, color: fg),
          child: child,
        ),
      );
    });
  }
}

/* ---------------- section head / labels ----------------------------------- */

class Eyebrow extends StatelessWidget {
  final String text;
  final Color? color;
  const Eyebrow(this.text, {super.key, this.color});
  @override
  Widget build(BuildContext c) =>
      Text(text.toUpperCase(), style: eyebrowStyle(color));
}

class Muted extends StatelessWidget {
  final String text;
  final double size;
  final Color? color;
  const Muted(this.text, {super.key, this.size = 11.5, this.color});
  @override
  Widget build(BuildContext c) => Text(text, style: mutedStyle(size, color));
}

class SectionHead extends StatelessWidget {
  final String eyebrow;
  final String title;
  final String? sub;
  final Widget? actions;
  const SectionHead(
      {super.key,
      required this.eyebrow,
      required this.title,
      this.sub,
      this.actions});

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Eyebrow(eyebrow),
                  const SizedBox(height: 4),
                  Text(title,
                      style: const TextStyle(
                          fontSize: 14.5, fontWeight: FontWeight.w700)),
                  if (sub != null) ...[
                    const SizedBox(height: 2),
                    Muted(sub!, size: 12.5),
                  ],
                ],
              ),
            ),
            ?actions,
          ],
        ),
      );
}

/* ---------------- panel / section ----------------------------------------- */

class Panel extends StatelessWidget {
  final Widget child;
  final EdgeInsets padding;
  const Panel({super.key, required this.child, this.padding = const EdgeInsets.all(20)});
  @override
  Widget build(BuildContext context) => Container(
        padding: padding,
        decoration: BoxDecoration(
          color: const Color(0xF0121722),
          border: Border.all(color: T.line),
          borderRadius: BorderRadius.circular(16),
          boxShadow: const [
            BoxShadow(color: T.shadowBase, blurRadius: 40, offset: Offset(0, 14)),
          ],
        ),
        child: child,
      );
}

class DividerSoft extends StatelessWidget {
  final double top, bottom;
  const DividerSoft({super.key, this.top = 20, this.bottom = 20});
  @override
  Widget build(BuildContext context) => Padding(
        padding: EdgeInsets.only(top: top, bottom: bottom),
        child: const Divider(height: 1, color: T.lineSoft, thickness: 1),
      );
}

/* ---------------- modal ---------------------------------------------------- */

Future<R?> showAppModal<R>(BuildContext context, Widget child,
    {double width = 520}) {
  return showGeneralDialog<R>(
    context: context,
    barrierDismissible: true,
    barrierLabel: 'modal',
    barrierColor: const Color(0xA803050A),
    transitionDuration: const Duration(milliseconds: 150),
    pageBuilder: (_, _, _) => const SizedBox.shrink(),
    transitionBuilder: (context, anim, _, _) {
      final curved = CurvedAnimation(parent: anim, curve: Curves.easeOut);
      return FadeTransition(
        opacity: curved,
        child: ScaleTransition(
          scale: Tween(begin: 0.97, end: 1.0).animate(curved),
          child: Center(
            child: SingleChildScrollView(
              child: Container(
                width: width,
                padding: const EdgeInsets.all(18),
                decoration: BoxDecoration(
                  color: T.card,
                  border: Border.all(color: T.line),
                  borderRadius: BorderRadius.circular(16),
                  boxShadow: const [
                    BoxShadow(
                        color: Color(0x8C000000),
                        blurRadius: 70,
                        offset: Offset(0, 26)),
                  ],
                ),
                child: child,
              ),
            ),
          ),
        ),
      );
    },
  );
}

/* ---------------- choice card --------------------------------------------- */

class ChoiceCard extends StatefulWidget {
  final Widget icon;
  final String title;
  final String desc;
  final VoidCallback? onTap;
  const ChoiceCard(
      {super.key,
      required this.icon,
      required this.title,
      required this.desc,
      this.onTap});

  @override
  State<ChoiceCard> createState() => _ChoiceCardState();
}

class _ChoiceCardState extends State<ChoiceCard> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    return MouseRegion(
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      cursor: SystemMouseCursors.click,
      child: GestureDetector(
        onTap: widget.onTap,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 130),
          transform: Matrix4.translationValues(0, _hover ? -2 : 0, 0),
          padding: const EdgeInsets.all(16),
          constraints: const BoxConstraints(minHeight: 128),
          decoration: BoxDecoration(
            color: _hover ? const Color(0xFF111728) : T.card2,
            border: Border.all(
                color: _hover ? const Color(0x8C7C5CFF) : T.line),
            borderRadius: BorderRadius.circular(16),
            gradient: _hover
                ? null
                : const LinearGradient(
                    begin: Alignment.topCenter,
                    end: Alignment(0, 0.6),
                    colors: [Color(0x06FFFFFF), Colors.transparent]),
            boxShadow: const [
              BoxShadow(
                  color: Color(0x38000000), blurRadius: 16, offset: Offset(0, 4)),
            ],
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  widget.icon,
                  const Spacer(),
                  AnimatedContainer(
                    duration: const Duration(milliseconds: 130),
                    padding: const EdgeInsets.all(6),
                    decoration: BoxDecoration(
                      color: const Color(0x0DFFFFFF),
                      border: Border.all(
                          color: _hover ? T.lineHi : T.lineSoft),
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: IconNext(size: 14,
                        key: ValueKey(_hover)),
                  ),
                ],
              ),
              const SizedBox(height: 10),
              Text(widget.title,
                  style: const TextStyle(
                      fontSize: 15, fontWeight: FontWeight.w700)),
              const SizedBox(height: 4),
              Muted(widget.desc, size: 12.5),
            ],
          ),
        ),
      ),
    );
  }
}

/// 40px icon tile used inside choice cards.
class IcoBox extends StatelessWidget {
  final Widget child;
  final bool tg;
  const IcoBox({super.key, required this.child, this.tg = false});
  @override
  Widget build(BuildContext context) => Container(
        width: tg ? 42 : 40,
        height: tg ? 42 : 40,
        decoration: BoxDecoration(
          color: tg ? Colors.transparent : const Color(0xFF1B2234),
          border: tg ? null : Border.all(color: T.lineSoft),
          borderRadius: BorderRadius.circular(tg ? 12 : 11),
        ),
        alignment: Alignment.center,
        child: child,
      );
}

/* ---------------- clipboard ------------------------------------------------ */

Future<bool> copyText(String text) async {
  if (text.isEmpty) return false;
  try {
    await Clipboard.setData(ClipboardData(text: text));
    return true;
  } catch (_) {
    return false;
  }
}

/* ---------------- toast ---------------------------------------------------- */

void showToast(BuildContext context, String msg) {
  final sc = ScaffoldMessenger.maybeOf(context);
  if (sc == null) return;
  sc
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(
      content: Text(msg, style: const TextStyle(fontSize: 12.5)),
      duration: const Duration(milliseconds: 1800),
      behavior: SnackBarBehavior.floating,
    ));
}

/* ---------------- segmented control --------------------------------------- */

class Seg extends StatelessWidget {
  final List<String> options;
  final String value;
  final ValueChanged<String> onChange;
  const Seg(
      {super.key,
      required this.options,
      required this.value,
      required this.onChange});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(3),
      decoration: BoxDecoration(
        color: T.bgSoft,
        border: Border.all(color: T.line),
        borderRadius: BorderRadius.circular(11),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: options.map((o) {
          final on = o == value;
          return Padding(
            padding: const EdgeInsets.symmetric(horizontal: 3),
            child: Material(
              color: Colors.transparent,
              child: InkWell(
                borderRadius: BorderRadius.circular(8),
                onTap: () => onChange(o),
                child: AnimatedContainer(
                  duration: const Duration(milliseconds: 130),
                  padding:
                      const EdgeInsets.symmetric(horizontal: 16, vertical: 7),
                  decoration: BoxDecoration(
                    gradient: on
                        ? const LinearGradient(
                            begin: Alignment.topCenter,
                            end: Alignment.bottomCenter,
                            colors: [Color(0xFF8B6CFF), T.acc2])
                        : null,
                    borderRadius: BorderRadius.circular(8),
                    boxShadow: on
                        ? const [
                            BoxShadow(
                                color: Color(0x596A45FF),
                                blurRadius: 8,
                                offset: Offset(0, 2))
                          ]
                        : null,
                  ),
                  child: Text(
                    o,
                    style: TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                      color: on ? Colors.white : T.mut,
                    ),
                  ),
                ),
              ),
            ),
          );
        }).toList(),
      ),
    );
  }
}

/* ---------------- status dot ----------------------------------------------- */

class StatusDot extends StatefulWidget {
  final bool on;
  const StatusDot({super.key, required this.on});
  @override
  State<StatusDot> createState() => _StatusDotState();
}

class _StatusDotState extends State<StatusDot>
    with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(
      vsync: this, duration: const Duration(milliseconds: 1900))
    ..repeat(reverse: true);

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final color = widget.on ? T.grn : const Color(0xFF64748B);
    final glow = widget.on
        ? const Color(0x2E22C55E)
        : const Color(0x2964748B);
    return FadeTransition(
      opacity: Tween(begin: 0.5, end: 1.0).animate(_c),
      child: Container(
        width: 7,
        height: 7,
        decoration: BoxDecoration(
          color: color,
          shape: BoxShape.circle,
          boxShadow: [BoxShadow(color: glow, blurRadius: 0, spreadRadius: 3)],
        ),
      ),
    );
  }
}

class StatusPill extends StatelessWidget {
  final bool on;
  final String label;
  const StatusPill({super.key, required this.on, required this.label});
  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 4),
        decoration: BoxDecoration(
          color: T.card2,
          border: Border.all(color: T.line),
          borderRadius: BorderRadius.circular(999),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            StatusDot(on: on),
            const SizedBox(width: 7),
            Text(label,
                style: TextStyle(
                    fontSize: 12, fontWeight: FontWeight.w600, color: T.mut)),
          ],
        ),
      );
}

/* ---------------- field label group --------------------------------------- */

class Field extends StatelessWidget {
  final String label;
  final Widget child;
  const Field({super.key, required this.label, required this.child});
  @override
  Widget build(BuildContext context) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(bottom: 7),
            child: Text(label.toUpperCase(), style: labelStyle()),
          ),
          child,
        ],
      );
}

/// Labelled text field with the theme's input styling.
class TextInput extends StatelessWidget {
  final TextEditingController? controller;
  final String? hint;
  final ValueChanged<String>? onChanged;
  final VoidCallback? onSubmitted;
  final int maxLines;
  final bool mono;
  const TextInput({
    super.key,
    this.controller,
    this.hint,
    this.onChanged,
    this.onSubmitted,
    this.maxLines = 1,
    this.mono = false,
  });

  @override
  Widget build(BuildContext context) => TextField(
        controller: controller,
        onChanged: onChanged,
        onSubmitted: (_) => onSubmitted?.call(),
        maxLines: maxLines,
        minLines: 1,
        style: mono
            ? const TextStyle(
                fontFamily: 'Consolas', fontSize: 12, color: T.txt, height: 1.5)
            : const TextStyle(fontSize: 14, color: T.txt),
        decoration: fieldInputDecoration(hint: hint),
      );
}
