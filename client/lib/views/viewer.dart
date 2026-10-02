// Valid Viewer: accounts browser + mailbox viewer — Flutter port of
// internal/ui/assets/viewer/main.mjs and viewer/mail.mjs. Talks to the
// viewer mux (127.0.0.1:2719), which serves accounts/stats/reload/events,
// mail/*, proxy-lists, proxy-fetch and pick.
import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_html/flutter_html.dart';

import '../api.dart';
import '../theme.dart';
import '../widgets.dart';

/* -------------------------------------------------------------------------- */
/* helpers (ported 1:1 from the web viewer)                                    */
/* -------------------------------------------------------------------------- */

String fmtBytes(num n) {
  final v = n.toDouble();
  if (v < 1024) return '${v.toInt()} B';
  if (v < 1024 * 1024) return '${(v / 1024).toStringAsFixed(1)} KB';
  return '${(v / 1024 / 1024).toStringAsFixed(2)} MB';
}

String tagKindFor(String t) {
  final x = t.toLowerCase();
  if (x.contains('ultimate') ||
      x.contains('game pass') ||
      x.contains('promo') ||
      x.contains('discord')) {
    return 'green';
  }
  if (x.contains('no payment') || x.contains('2fa')) return 'warn';
  return '';
}

const kPreferredFields = [
  'Active', 'Country', 'Services', 'Active Cards', 'Removed Cards', 'Expires',
  'Fortnite', 'Specials', 'Promos', 'Claimed Promos', 'Inbox', 'Keywords',
  'Level', 'Account Level', 'V-Bucks', 'Display', 'Notes',
];

List<String> sortedFieldKeys(Map<String, dynamic> fields) {
  bool truthy(dynamic v) {
    if (v == null) return false;
    if (v is bool) return v;
    if (v is num) return v != 0;
    return v.toString().isNotEmpty;
  }

  final keys = fields.keys.where((k) => truthy(fields[k])).toList();
  keys.sort((a, b) {
    final ia = kPreferredFields.indexWhere((p) => p.toLowerCase() == a.toLowerCase());
    final ib = kPreferredFields.indexWhere((p) => p.toLowerCase() == b.toLowerCase());
    if (ia == -1 && ib == -1) return a.toLowerCase().compareTo(b.toLowerCase());
    if (ia == -1) return 1;
    if (ib == -1) return -1;
    return ia - ib;
  });
  return keys;
}

const _emailRe = r'[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}';

String displayFromEmail(dynamic raw) {
  final s = (raw ?? '').toString().trim();
  final m = RegExp(_emailRe, caseSensitive: false).firstMatch(s);
  return m != null ? m.group(0)! : s;
}

String senderEmailOf(dynamic raw) {
  final s = (raw ?? '').toString().trim();
  final m = RegExp(_emailRe, caseSensitive: false).firstMatch(s);
  return m != null ? m.group(0)! : s;
}

String senderKeyOf(dynamic from) {
  final raw = (from ?? '').toString().trim();
  final m = RegExp(_emailRe, caseSensitive: false).firstMatch(raw);
  if (m != null) return m.group(0)!.toLowerCase();
  final low = raw.toLowerCase();
  return low.isEmpty ? 'unknown' : low;
}

String mailKeyOf(dynamic m) {
  if (m is! Map) return '';
  if (m['id'] != null && '${m['id']}'.isNotEmpty) return 'id:${m['id']}';
  return '${m['subject'] ?? ''}|${m['from'] ?? ''}|${m['date'] ?? ''}';
}

String formatMailDate(dynamic raw) {
  if (raw == null || '$raw'.isEmpty) return '';
  final s = '$raw'.replaceAll(RegExp(r'\.\d{3}\d+Z$'), 'Z');
  final d = DateTime.tryParse(s)?.toLocal();
  if (d == null) return '$raw';
  const months = [
    'January', 'February', 'March', 'April', 'May', 'June',
    'July', 'August', 'September', 'October', 'November', 'December',
  ];
  final h = d.hour;
  final m = d.minute;
  final am = h < 12 ? 'AM' : 'PM';
  var h12 = h % 12;
  if (h12 == 0) h12 = 12;
  final mm = m < 10 ? '0$m' : '$m';
  return '${months[d.month]} ${d.day}, at $h12:$mm $am';
}

String wrapMailHTML(String htmlBody, String preview) {
  final raw = htmlBody;
  if (raw.isNotEmpty && RegExp(r'<html', caseSensitive: false).hasMatch(raw)) {
    var s = raw;
    if (!RegExp(r'<meta[^>]+charset', caseSensitive: false).hasMatch(s)) {
      s = s.replaceFirstMapped(RegExp(r'<head[^>]*>', caseSensitive: false),
          (m) => '${m.group(0)}<meta charset="utf-8">');
    }
    if (!RegExp(r'<base ', caseSensitive: false).hasMatch(s)) {
      s = s.replaceFirstMapped(RegExp(r'<head[^>]*>', caseSensitive: false),
          (m) => '${m.group(0)}<base target="_blank">');
    }
    return s;
  }
  String esc(String c) =>
      c == '&' ? '&amp;' : c == '<' ? '&lt;' : c == '>' ? '&gt;' : c;
  final body = raw.contains('<')
      ? raw
      : '<pre style="white-space:pre-wrap;font:14px/1.55 Segoe UI,Arial,sans-serif;margin:0">'
          '${preview.replaceAllMapped(RegExp(r'[&<>]'), (m) => esc(m.group(0)!))}'
          '</pre>';
  return '<!DOCTYPE html><html><head><meta charset="utf-8">'
      '<meta name="viewport" content="width=device-width,initial-scale=1">'
      '<base target="_blank">'
      '<style>img,video,iframe{max-width:100%;height:auto}table{max-width:100%}</style>'
      '</head><body style="margin:0;padding:16px;background:#fff">'
      '$body'
      '</body></html>';
}

int jint(dynamic v, [int d = 0]) => v is num ? v.toInt() : d;

List<Map<String, dynamic>> jmaps(dynamic v) {
  if (v is! List) return [];
  final out = <Map<String, dynamic>>[];
  for (final e in v) {
    if (e is Map) out.add(Map<String, dynamic>.from(e));
  }
  return out;
}

class Account {
  final int id;
  final int line;
  final String email;
  final String password;
  final List<String> tags;
  final Map<String, dynamic> fields;
  final String raw;
  Account({
    this.id = 0,
    this.line = 0,
    this.email = '',
    this.password = '',
    this.tags = const [],
    this.fields = const {},
    this.raw = '',
  });

  factory Account.fromJson(Map j) => Account(
        id: jint(j['id']),
        line: jint(j['line']),
        email: '${j['email'] ?? ''}',
        password: '${j['password'] ?? ''}',
        tags: (j['tags'] is List)
            ? (j['tags'] as List).map((e) => '$e').toList()
            : const [],
        fields: j['fields'] is Map
            ? Map<String, dynamic>.from(j['fields'])
            : const {},
        raw: '${j['raw'] ?? ''}',
      );
}

/* -------------------------------------------------------------------------- */
/* small shared controls                                                       */
/* -------------------------------------------------------------------------- */

class _DropSelect extends StatelessWidget {
  final String value;
  final String hint;
  final List<MapEntry<String, String>> items;
  final ValueChanged<String> onChange;
  final double width;
  const _DropSelect({
    required this.value,
    required this.hint,
    required this.items,
    required this.onChange,
    this.width = 190,
  });

  @override
  Widget build(BuildContext context) {
    final label = value.isEmpty
        ? hint
        : (items.firstWhere((e) => e.value == value, orElse: () => MapEntry(value, value)).key);
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(10),
        onTap: () async {
          final box = context.findRenderObject() as RenderBox?;
          if (box == null || !box.hasSize) return;
          final off = box.localToGlobal(Offset.zero);
          final sel = await showMenu<String>(
            context: context,
            elevation: 10,
            color: T.card,
            surfaceTintColor: Colors.transparent,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(12),
              side: const BorderSide(color: T.line),
            ),
            position: RelativeRect.fromLTRB(
                off.dx, off.dy + box.size.height + 6, off.dx + box.size.width, off.dy),
            items: [
              for (final e in items)
                PopupMenuItem(
                  value: e.value,
                  child: Row(
                    children: [
                      Expanded(
                        child: Text(
                          e.key,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            fontSize: 13,
                            fontWeight: FontWeight.w600,
                            color: e.value == value ? T.acc : T.txt,
                          ),
                        ),
                      ),
                      if (e.value == value)
                        const Icon(Icons.check, size: 14, color: T.acc),
                    ],
                  ),
                ),
            ],
          );
          if (sel != null) onChange(sel);
        },
        child: Container(
          width: width,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 11),
          decoration: BoxDecoration(
            color: T.bgSoft,
            border: Border.all(color: T.line),
            borderRadius: BorderRadius.circular(10),
          ),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  label,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontSize: 13, color: T.txt),
                ),
              ),
              const SizedBox(width: 6),
              const Icon(Icons.keyboard_arrow_down, size: 16, color: T.mut),
            ],
          ),
        ),
      ),
    );
  }
}

const _avColors = [
  Color(0xFF3B82F6), Color(0xFF22C55E), Color(0xFFA855F7), Color(0xFFF59E0B),
  Color(0xFFEF4444), Color(0xFF06B6D4), Color(0xFF84CC16), Color(0xFFF97316),
];

class _Avatar extends StatelessWidget {
  final String from;
  final bool big;
  const _Avatar({required this.from, this.big = false});

  @override
  Widget build(BuildContext context) {
    final key = senderEmailOf(from).toLowerCase();
    final cleaned = key.replaceFirst(RegExp(r'^[^a-z0-9]+', caseSensitive: false), '');
    final letter = ((cleaned.isNotEmpty ? cleaned[0] : '?')).toUpperCase();
    final domain = key.contains('@') ? key.split('@')[1] : '';
    var n = 0;
    for (var i = 0; i < key.length; i++) {
      n = (n + key.codeUnitAt(i)) % _avColors.length;
    }
    final d = big ? 44.0 : 32.0;
    return Container(
      width: d,
      height: d,
      clipBehavior: Clip.antiAlias,
      decoration: BoxDecoration(
        color: _avColors[n],
        shape: BoxShape.circle,
      ),
      child: Stack(
        alignment: Alignment.center,
        children: [
          Text(
            letter,
            style: TextStyle(
              color: Colors.white,
              fontWeight: FontWeight.w700,
              fontSize: big ? 16 : 13,
            ),
          ),
          if (domain.isNotEmpty)
            Positioned.fill(
              child: Image.network(
                'https://www.google.com/s2/favicons?sz=128&domain_url=${Uri.encodeComponent('https://$domain')}',
                fit: BoxFit.cover,
                errorBuilder: (_, _, _) => const SizedBox.shrink(),
              ),
            ),
        ],
      ),
    );
  }
}

class _ConnOrb extends StatefulWidget {
  const _ConnOrb();

  @override
  State<_ConnOrb> createState() => _ConnOrbState();
}

class _ConnOrbState extends State<_ConnOrb> with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(
      vsync: this, duration: const Duration(milliseconds: 1900))
    ..repeat();

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    Widget ring(double begin) {
      final t = Tween(begin: begin, end: begin + 0.55).animate(
          CurvedAnimation(parent: _c, curve: const Interval(0, 1, curve: Curves.easeOut)));
      final o = Tween(begin: 0.55, end: 0.0).animate(
          CurvedAnimation(parent: _c, curve: const Interval(0, 1, curve: Curves.easeOut)));
      return AnimatedBuilder(
        animation: _c,
        builder: (_, _) => Opacity(
          opacity: o.value,
          child: Transform.scale(
            scale: t.value,
            child: Container(
              width: 52,
              height: 52,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(color: T.acc, width: 1.6),
              ),
            ),
          ),
        ),
      );
    }

    return SizedBox(
      width: 84,
      height: 84,
      child: Stack(
        alignment: Alignment.center,
        children: [
          ring(1.0),
          Container(
            width: 52,
            height: 52,
            decoration: const BoxDecoration(
              shape: BoxShape.circle,
              gradient: RadialGradient(
                  colors: [Color(0xFF8B6CFF), Color(0xFF3B2A7A)]),
              boxShadow: [
                BoxShadow(color: Color(0x666A45FF), blurRadius: 24),
              ],
            ),
            child: const Center(child: XboxMark(size: 30)),
          ),
        ],
      ),
    );
  }
}

/* -------------------------------------------------------------------------- */
/* ViewerScreen — accounts browser (viewer/main.mjs)                           */
/* -------------------------------------------------------------------------- */

class ViewerScreen extends StatefulWidget {
  const ViewerScreen({super.key});

  @override
  State<ViewerScreen> createState() => _ViewerScreenState();
}

class _ViewerScreenState extends State<ViewerScreen> {
  Map<String, dynamic> _stats = {};
  String _fq = '';
  String _ftag = '';
  String _fcountry = '';
  String _fsort = 'id';
  List<Account> _accounts = [];
  int _count = 0;
  int _page = 1;
  int _pages = 1;
  Account? _mailAcc;
  String _toast = '';
  Timer? _toastTimer;
  Timer? _evDebounce;
  StreamSubscription<String>? _evSub;
  bool _loading = false;

  @override
  void initState() {
    super.initState();
    _loadStats();
    _loadAccounts();
    _evSub = Api.events().listen((_) {
      if (_mailAcc != null) return;
      _evDebounce?.cancel();
      _evDebounce = Timer(const Duration(milliseconds: 2500), () {
        if (!mounted || _mailAcc != null) return;
        _loadStats();
        _loadAccounts();
      });
    });
  }

  @override
  void dispose() {
    _evSub?.cancel();
    _evDebounce?.cancel();
    _toastTimer?.cancel();
    super.dispose();
  }

  void _toastMsg(String m) {
    if (!mounted) return;
    setState(() => _toast = m);
    _toastTimer?.cancel();
    _toastTimer = Timer(const Duration(milliseconds: 1800), () {
      if (mounted) setState(() => _toast = '');
    });
  }

  Future<void> _loadStats() async {
    final r = await Api.get('/api/stats', base: viewerBase);
    if (!mounted || r.data is! Map) return;
    setState(() => _stats = Map<String, dynamic>.from(r.data));
  }

  Future<void> _loadAccounts({int? page}) async {
    if (_loading) return;
    _loading = true;
    try {
      final uri = Uri(queryParameters: {
        'q': _fq,
        'tag': _ftag,
        'country': _fcountry,
        'sort': _fsort,
        'page': '${page ?? 1}',
        'limit': '24',
      });
      final r = await Api.get('/api/accounts?$uri', base: viewerBase);
      if (!mounted || r.data is! Map) return;
      final data = Map<String, dynamic>.from(r.data);
      final list = jmaps(data['accounts']).map(Account.fromJson).toList();
      setState(() {
        _accounts = list;
        _count = data['count'] is num ? jint(data['count']) : list.length;
        _page = jint(data['page'], 1);
        _pages = jint(data['pages'], math.max(1, (_count / 24).ceil()));
      });
    } finally {
      _loading = false;
    }
  }

  void _search() {
    _loadAccounts();
  }

  void _clear() {
    setState(() {
      _fq = '';
      _ftag = '';
      _fcountry = '';
      _fsort = 'id';
    });
    _loadAccounts();
    _loadStats();
  }

  void _gotoPage(int p) {
    final np = math.max(1, math.min(p, _pages));
    _loadAccounts(page: np);
  }

  Future<void> _reloadFile() async {
    final r = await Api.postEmpty('/api/reload', base: viewerBase);
    if (!mounted) return;
    if (!r.ok) {
      _toastMsg('Reload failed');
      return;
    }
    _toastMsg('File reloaded');
    await _loadStats();
    await _loadAccounts();
  }

  Future<void> _copyAll() async {
    final uri = Uri(queryParameters: {
      'q': _fq,
      'tag': _ftag,
      'country': _fcountry,
      'sort': _fsort,
    });
    final r = await Api.get('/api/accounts?$uri', base: viewerBase);
    if (!mounted) return;
    final data = r.data is Map ? Map<String, dynamic>.from(r.data) : <String, dynamic>{};
    final emails = jmaps(data['accounts'])
        .map((a) => '${a['email'] ?? ''}')
        .where((e) => e.isNotEmpty)
        .join('\n');
    final ok = await copyText(emails);
    _toastMsg(ok ? 'Copied ${jint(data['count'])} emails' : 'Copy failed');
  }

  List<String> get _tagKeys {
    final tc = _stats['tag_counts'];
    if (tc is! Map) return [];
    final keys = tc.keys.map((k) => '$k').toList();
    keys.sort((a, b) => jint(tc[b]).compareTo(jint(tc[a])));
    return keys;
  }

  List<String> get _countryKeys {
    final c = _stats['countries'];
    if (c is! Map) return [];
    final keys = c.keys.map((k) => '$k').toList();
    keys.sort();
    return keys;
  }

  List<String> get _chips {
    const pinned = [
      'Has Payment', 'No Payment', 'PayPal', 'Fortnite', 'Promos', 'Private', 'Free',
    ];
    final tc = _stats['tag_counts'];
    final counts = tc is Map ? tc : <String, dynamic>{};
    final shown = <String>[];
    for (final t in pinned) {
      if (counts[t] != null && jint(counts[t]) > 0) shown.add(t);
    }
    for (final t in _tagKeys) {
      if (!shown.contains(t)) shown.add(t);
      if (shown.length >= 16) break;
    }
    return shown.take(16).toList();
  }

  @override
  Widget build(BuildContext context) {
    final s = _stats;
    final file = '${s['file_path'] ?? 'valid.txt'}';
    return Scaffold(
      body: appBackdrop(
        Stack(
          children: [
            Column(
              children: [
                _topBar(context),
                Expanded(
                  child: SingleChildScrollView(
                    padding: const EdgeInsets.fromLTRB(24, 22, 24, 40),
                    child: Center(
                      child: ConstrainedBox(
                        constraints: const BoxConstraints(maxWidth: 1180),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            _header(context, file),
                            const SizedBox(height: 16),
                            _controls(),
                            const SizedBox(height: 14),
                            _chipRow(),
                            const SizedBox(height: 20),
                            _statsRow(s),
                            const SizedBox(height: 14),
                            Muted('$_count account(s) matched'),
                            if (_pages > 1) ...[
                              const SizedBox(height: 12),
                              _pager(),
                            ],
                            const SizedBox(height: 16),
                            _grid(),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
                _statusBar(file),
              ],
            ),
            if (_mailAcc != null)
              Positioned.fill(
                child: MailViewer(
                  account: _mailAcc!,
                  onClose: () => setState(() => _mailAcc = null),
                ),
              ),
            if (_toast.isNotEmpty)
              Positioned(
                bottom: 46,
                left: 0,
                right: 0,
                child: Center(
                  child: Container(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                    decoration: BoxDecoration(
                      color: T.cardHi,
                      border: Border.all(color: T.line),
                      borderRadius: BorderRadius.circular(10),
                      boxShadow: const [
                        BoxShadow(color: Color(0x66000000), blurRadius: 18),
                      ],
                    ),
                    child: Text(_toast,
                        style: const TextStyle(fontSize: 12.5, color: T.txt)),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _topBar(BuildContext context) {
    return Container(
      height: 46,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      decoration: const BoxDecoration(
        color: Color(0xEB090C13),
        border: Border(bottom: BorderSide(color: T.line)),
      ),
      child: Row(
        children: [
          Container(
            width: 22,
            height: 22,
            decoration: BoxDecoration(
              gradient: const LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [Color(0xFF8B6CFF), T.acc2]),
              borderRadius: BorderRadius.circular(7),
            ),
            alignment: Alignment.center,
            child: const XboxMark(size: 15),
          ),
          const SizedBox(width: 9),
          Text('SOLO Checker',
              style:
                  TextStyle(fontSize: 13, fontWeight: FontWeight.w600, color: T.mut)),
          const SizedBox(width: 9),
          const Text('/', style: TextStyle(fontSize: 13, color: Color(0xFF46536E))),
          const SizedBox(width: 9),
          const Expanded(
            child: Text('Valid Viewer',
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                    fontSize: 13, fontWeight: FontWeight.w700, color: T.txt)),
          ),
          const StatusPill(on: true, label: 'Online'),
          const SizedBox(width: 10),
          TextBtn(
            label: 'Back',
            icon: const IconBack(size: 14),
            padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 6),
            onPressed: () => Navigator.of(context).pop(),
          ),
        ],
      ),
    );
  }

  Widget _header(BuildContext context, String file) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('Valid Viewer',
                  style: TextStyle(fontSize: 22, fontWeight: FontWeight.w800)),
              const SizedBox(height: 5),
              Muted(
                  '$file · modified ${_stats['modified'] ?? '—'} · loaded ${_stats['loaded_at'] ?? '—'}',
                  size: 12.5),
            ],
          ),
        ),
        const SizedBox(width: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TextBtn(
              label: 'Reload file',
              kind: BtnKind.secondary,
              onPressed: _reloadFile,
            ),
            TextBtn(
              label: 'Copy filtered emails',
              kind: BtnKind.secondary,
              onPressed: _copyAll,
            ),
          ],
        ),
      ],
    );
  }

  Widget _controls() {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      crossAxisAlignment: WrapCrossAlignment.center,
      children: [
        SizedBox(
          width: 340,
          child: TextField(
            style: const TextStyle(fontSize: 13.5, color: T.txt),
            decoration: fieldInputDecoration(
                hint:
                    'Search email, password, country, services, promos…'),
            onChanged: (v) => _fq = v,
            onSubmitted: (_) => _search(),
          ),
        ),
        _DropSelect(
          hint: 'All tags',
          value: _ftag,
          items: [
            const MapEntry('All tags', ''),
            for (final t in _tagKeys)
              MapEntry('$t (${jint((_stats['tag_counts'] as Map)[t])})', t),
          ],
          onChange: (v) {
            setState(() => _ftag = v);
            _loadAccounts();
          },
        ),
        _DropSelect(
          hint: 'All countries',
          value: _fcountry,
          width: 210,
          items: [
            const MapEntry('All countries', ''),
            for (final c in _countryKeys)
              MapEntry('$c (${jint((_stats['countries'] as Map)[c])})', c),
          ],
          onChange: (v) {
            setState(() => _fcountry = v);
            _loadAccounts();
          },
        ),
        _DropSelect(
          hint: 'Sort',
          value: _fsort,
          width: 170,
          items: const [
            MapEntry('File order', 'id'),
            MapEntry('Newest', 'newest'),
            MapEntry('Oldest', 'oldest'),
            MapEntry('Email A–Z', 'email'),
            MapEntry('Email Z–A', 'email-desc'),
            MapEntry('Country', 'country'),
            MapEntry('Line number', 'line'),
          ],
          onChange: (v) {
            setState(() => _fsort = v);
            _loadAccounts();
          },
        ),
        TextBtn(label: 'Filter', kind: BtnKind.primary, onPressed: _search),
        TextBtn(label: 'Clear', kind: BtnKind.secondary, onPressed: _clear),
      ],
    );
  }

  Widget _chipRow() {
    final counts = _stats['tag_counts'];
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        for (final t in _chips)
          InkWell(
            borderRadius: BorderRadius.circular(999),
            onTap: () {
              setState(() => _ftag = _ftag == t ? '' : t);
              _loadAccounts();
              _loadStats();
            },
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 5),
              decoration: BoxDecoration(
                color: _ftag == t ? const Color(0x297C5CFF) : T.bgSoft,
                border: Border.all(
                    color: _ftag == t ? const Color(0x617C5CFF) : T.line),
                borderRadius: BorderRadius.circular(999),
              ),
              child: Text(
                '$t · ${jint((counts as Map)[t])}',
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: _ftag == t ? Colors.white : T.mut,
                ),
              ),
            ),
          ),
      ],
    );
  }

  Widget _statsRow(Map<String, dynamic> s) {
    Widget stat(String label, String value) => Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 13),
          decoration: BoxDecoration(
            color: const Color(0xF0121722),
            border: Border.all(color: T.line),
            borderRadius: BorderRadius.circular(14),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(label.toUpperCase(), style: labelStyle()),
              const SizedBox(height: 5),
              Text(value,
                  style: const TextStyle(
                      fontSize: 17, fontWeight: FontWeight.w800, color: T.txt)),
            ],
          ),
        );

    return Row(
      children: [
        Expanded(
          child: stat(
              'Total valids',
              s['total'] != null ? '${jint(s['total'])}' : '—'),
        ),
        const SizedBox(width: 14),
        Expanded(
          child: stat(
              'File size',
              s['file_size'] != null
                  ? fmtBytes((s['file_size'] as num))
                  : '—'),
        ),
      ],
    );
  }

  Widget _pager() {
    return Row(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        TextBtn(
          label: 'Back',
          kind: BtnKind.secondary,
          disabled: _page <= 1,
          onPressed: _page <= 1 ? null : () => _gotoPage(_page - 1),
        ),
        const SizedBox(width: 10),
        Muted('Page $_page / $_pages', size: 12.5),
        const SizedBox(width: 10),
        TextBtn(
          label: 'Next',
          kind: BtnKind.secondary,
          disabled: _page >= _pages,
          onPressed: _page >= _pages ? null : () => _gotoPage(_page + 1),
        ),
      ],
    );
  }

  Widget _grid() {
    if (_accounts.isEmpty) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 34),
        child: Center(child: Muted('No accounts match your filters.', size: 13)),
      );
    }
    return Wrap(
      spacing: 14,
      runSpacing: 14,
      children: [
        for (final a in _accounts)
          SizedBox(
            width: 380,
            child: _AccountCard(
              a: a,
              onToast: _toastMsg,
              onMail: (acc) => setState(() => _mailAcc = acc),
            ),
          ),
      ],
    );
  }

  Widget _statusBar(String file) {
    return Container(
      height: 30,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      decoration: const BoxDecoration(
        color: Color(0xF0090C13),
        border: Border(top: BorderSide(color: T.line)),
      ),
      child: Row(
        children: [
          const StatusDot(on: true),
          const SizedBox(width: 10),
          Text('connected', style: mutedStyle(11.5)),
          const SizedBox(width: 10),
          Container(width: 1, height: 12, color: T.line),
          const SizedBox(width: 10),
          Expanded(
            child: Text(file,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                    fontSize: 11.5,
                    fontWeight: FontWeight.w600,
                    color: Color(0xFFB6C2D8))),
          ),
          Text('Valid Viewer',
              style: const TextStyle(
                  fontSize: 11.5,
                  fontWeight: FontWeight.w600,
                  color: Color(0xFFB6C2D8))),
          const SizedBox(width: 10),
          Container(width: 1, height: 12, color: T.line),
          const SizedBox(width: 10),
          Text('$_count account(s) matched', style: mutedStyle(11.5)),
        ],
      ),
    );
  }
}

class _AccountCard extends StatelessWidget {
  final Account a;
  final ValueChanged<String> onToast;
  final ValueChanged<Account> onMail;
  const _AccountCard({required this.a, required this.onToast, required this.onMail});

  @override
  Widget build(BuildContext context) {
    final rows = sortedFieldKeys(a.fields);
    return Container(
      padding: const EdgeInsets.all(15),
      decoration: BoxDecoration(
        color: const Color(0xF0121722),
        border: Border.all(color: T.line),
        borderRadius: BorderRadius.circular(16),
        boxShadow: const [
          BoxShadow(color: Color(0x38000000), blurRadius: 16, offset: Offset(0, 4)),
        ],
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                child: Text(
                  a.email.isEmpty ? '(no email)' : a.email,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                      fontSize: 13.5, fontWeight: FontWeight.w700, color: T.txt),
                ),
              ),
              const SizedBox(width: 8),
              Text(
                '#${a.id} · L${a.line}',
                style: const TextStyle(
                    fontSize: 11,
                    fontFamily: 'Consolas',
                    color: T.mut),
              ),
            ],
          ),
          const SizedBox(height: 7),
          Text(
            a.password.isEmpty ? '—' : a.password,
            style: const TextStyle(
                fontSize: 12, fontFamily: 'Consolas', color: Color(0xFFB6C2D8)),
          ),
          if (a.tags.isNotEmpty) ...[
            const SizedBox(height: 9),
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                for (final t in a.tags) Tag(text: t, kind: tagKindFor(t)),
              ],
            ),
          ],
          if (rows.isNotEmpty) ...[
            const SizedBox(height: 10),
            const Divider(height: 1, color: T.lineSoft),
            const SizedBox(height: 8),
            Column(
              children: [
                for (final k in rows)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 5),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        SizedBox(
                          width: 124,
                          child: Text(k,
                              style: mutedStyle(11.5)),
                        ),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Text(
                            '${a.fields[k]}',
                            style: const TextStyle(
                                fontSize: 12, color: T.txt, height: 1.35),
                          ),
                        ),
                      ],
                    ),
                  ),
              ],
            ),
          ],
          const SizedBox(height: 8),
          Wrap(
            spacing: 7,
            runSpacing: 7,
            children: [
              TextBtn(
                label: 'Copy email',
                padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 6),
                onPressed: () async {
                  final ok = await copyText(a.email);
                  onToast(ok ? 'Copied' : 'Copy failed');
                },
              ),
              TextBtn(
                label: 'Copy combo',
                padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 6),
                onPressed: () async {
                  final ok = await copyText('${a.email}:${a.password}');
                  onToast(ok ? 'Copied' : 'Copy failed');
                },
              ),
              TextBtn(
                label: 'Copy line',
                padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 6),
                onPressed: () async {
                  final ok = await copyText(a.raw);
                  onToast(ok ? 'Copied' : 'Copy failed');
                },
              ),
              TextBtn(
                label: 'Email Viewer',
                kind: BtnKind.primary,
                padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 6),
                onPressed: () => onMail(a),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

/* -------------------------------------------------------------------------- */
/* MailViewer — mailbox client (viewer/mail.mjs)                               */
/* -------------------------------------------------------------------------- */

const kStageOrder = ['connecting', 'login_page', 'credentials', 'token', 'mailbox'];
const kStageLabel = {
  'connecting': 'Connecting through proxy',
  'login_page': 'Opening Microsoft login page',
  'credentials': 'Sending credentials',
  'token': 'Getting mailbox token',
  'mailbox': 'Loading inbox',
};

const kFolders = [
  ['inbox', 'Inbox'],
  ['sent', 'Sent'],
  ['spam', 'Spam'],
  ['deleted', 'Deleted'],
];

class _Pack {
  List<Map<String, dynamic>> mails = [];
  int total = 0;
  bool more = true;
  bool ready = false;
  int skip = 0;
}

class _DeleteAsk {
  List<String> ids;
  bool purge;
  String text;
  String err;
  bool busy;
  _DeleteAsk({
    required this.ids,
    required this.purge,
    required this.text,
    this.err = '',
  }) : busy = false;
}

class MailViewer extends StatefulWidget {
  final Account account;
  final VoidCallback onClose;
  const MailViewer({super.key, required this.account, required this.onClose});

  @override
  State<MailViewer> createState() => _MailViewerState();
}

class _MailViewerState extends State<MailViewer> {
  String _stage = 'checking'; // checking | saved | ask | px | connect | error | main
  List<Map<String, dynamic>> _mails = [];
  final Map<String, _Pack> _packs = {};
  String _fromFilter = '';
  String _folder = 'inbox';
  String _query = '';
  String _appliedQuery = 'kind:email';
  int _total = 0;
  bool _more = false;
  int _sel = -1;
  final Map<String, bool> _picked = {};
  bool _selAll = false;
  String _listErr = '';
  String? _readHtml;

  String _pxMode = 'file';
  String _pxPath = '';
  List<String> _pxUrls = [''];
  String _pxFileCount = '0 proxies';
  String _pxCount = '0 proxies';
  List<dynamic> _pxLists = [];
  String _pxMenu = '';
  Map<String, dynamic>? _proxy;

  int _connStep = 0;
  String _connProxy = '';
  final List<Map<String, dynamic>> _connTries = [];
  String _connErr = '';

  _DeleteAsk? _delAsk;
  Timer? _savedTimer;
  int _openSeq = 0;
  final TextEditingController _searchCtrl = TextEditingController();

  @override
  void initState() {
    super.initState();
    _open();
  }

  @override
  void dispose() {
    _savedTimer?.cancel();
    _searchCtrl.dispose();
    super.dispose();
  }

  /* ---- state helpers ---- */

  List<Map<String, dynamic>> get _activeList {
    if (_fromFilter.isEmpty) return _mails;
    final p = _packs[_fromFilter];
    return p?.mails ?? [];
  }

  void _reset() {
    _proxy = null;
    _mails = [];
    _packs.clear();
    _fromFilter = '';
    _folder = 'inbox';
    _appliedQuery = 'kind:email';
    _total = 0;
    _more = false;
    _picked.clear();
    _sel = -1;
    _listErr = '';
    _query = '';
    _selAll = false;
    _pxPath = '';
    _pxUrls = [''];
    _pxFileCount = '0 proxies';
    _pxCount = '0 proxies';
    _pxMode = 'file';
    _pxMenu = '';
    _readHtml = null;
    _connStep = 0;
    _connProxy = '';
    _connTries.clear();
    _connErr = '';
    _searchCtrl.clear();
    if (mounted) setState(() {});
  }

  /* ---- opening flow ---- */

  void _open() {
    final seq = ++_openSeq;
    _reset();
    setState(() => _stage = 'checking');
    Api.post('/api/mail/probe', {'email': widget.account.email},
            base: viewerBase)
        .then((res) {
      if (!mounted || seq != _openSeq) return;
      final data = res.data;
      if (data is Map && data['ok'] == true) {
        final msgs = jmaps(data['messages']);
        setState(() {
          _mails = msgs;
          _total = jint(data['total']);
          _more = data['more'] == true || _total > msgs.length;
          _stage = 'saved';
        });
        _savedTimer?.cancel();
        _savedTimer = Timer(const Duration(milliseconds: 950), () {
          if (mounted && seq == _openSeq) setState(() => _stage = 'main');
        });
        return;
      }
      setState(() => _stage = 'ask');
    });
  }

  /* ---- list loading ---- */

  Future<void> _loadPage({bool moreReq = false, String? fromFilterOverride}) async {
    final f = fromFilterOverride ?? _fromFilter;
    if (f.isNotEmpty) {
      await _loadSenderPage(f, moreReq);
      return;
    }
    final q = _appliedQuery;
    final from = moreReq ? _mails.length : 0;
    if (!moreReq) {
      setState(() {
        _mails = [];
        _sel = -1;
        _listErr = '';
      });
    }
    final body = <String, dynamic>{
      'email': widget.account.email,
      'password': widget.account.password,
      'from': from,
      'size': 100,
      'query': q,
      'folder': _folder,
      ...?_proxy,
    };
    final res = await Api.post('/api/mail/list', body, base: viewerBase);
    if (!mounted) return;
    if (!res.ok) {
      setState(() => _listErr = (res.data is Map && res.data['error'] != null)
          ? '${res.data['error']}'
          : 'Could not load mail');
      return;
    }
    final data = res.data is Map ? Map<String, dynamic>.from(res.data) : <String, dynamic>{};
    final seen = <String>{};
    for (final m in _mails) {
      seen.add(mailKeyOf(m));
    }
    final added = <Map<String, dynamic>>[];
    for (final m in jmaps(data['messages'])) {
      final k = mailKeyOf(m);
      if (seen.contains(k)) continue;
      seen.add(k);
      added.add(m);
    }
    final base = moreReq ? List<Map<String, dynamic>>.from(_mails) : <Map<String, dynamic>>[];
    final next = [...base, ...added];
    var total = _total;
    if (data['total'] is num) total = jint(data['total'], total);
    var skipExtra = 0;
    if (moreReq && added.isEmpty) skipExtra = 100;
    final loaded = (moreReq ? base.length : 0) + added.length + skipExtra;
    final moreFlag = added.isNotEmpty &&
        (data['more'] == true ||
            (total > 0 && loaded < total) ||
            added.length >= 10);
    setState(() {
      _mails = next;
      _total = total;
      _more = moreFlag;
      if (!moreReq && added.isNotEmpty) _sel = 0;
    });
    if (!moreReq && added.isNotEmpty) _showMail(0);
  }

  Future<void> _loadSenderPage(String from, bool moreReq) async {
    if (from.isEmpty) return;
    final cur = Map<String, _Pack>.from(_packs);
    var p = cur[from] ?? _Pack();
    if (!moreReq) {
      final seed = _mails.where((m) => senderKeyOf(m['from']) == from).toList();
      p = _Pack()
        ..mails = List.of(seed)
        ..more = true;
    }
    final off = moreReq ? p.mails.length + p.skip : 0;
    final body = <String, dynamic>{
      'email': widget.account.email,
      'password': widget.account.password,
      'from': off,
      'size': 100,
      'query': 'from:$from kind:email',
      'folder': _folder,
      ...?_proxy,
    };
    final res = await Api.post('/api/mail/list', body, base: viewerBase);
    if (!mounted) return;
    if (!res.ok) {
      p
        ..ready = true
        ..more = false;
      setState(() => _packs[from] = p);
      return;
    }
    final data = res.data is Map ? Map<String, dynamic>.from(res.data) : <String, dynamic>{};
    final seen = <String>{};
    for (final m in p.mails) {
      seen.add(mailKeyOf(m));
    }
    var addedN = 0;
    final newMails = List<Map<String, dynamic>>.from(p.mails);
    for (final m in jmaps(data['messages'])) {
      final k = mailKeyOf(m);
      if (seen.contains(k)) continue;
      seen.add(k);
      newMails.add(m);
      addedN++;
    }
    final total = data['total'] is num ? jint(data['total'], p.total) : p.total;
    p
      ..mails = newMails
      ..total = total
      ..ready = true
      ..skip = (moreReq && addedN == 0) ? p.skip + 100 : p.skip
      ..more = (total > 0 && newMails.length < total) ||
          (total == 0 && (data['more'] == true || addedN >= 10));
    setState(() => _packs[from] = p);
    if (!moreReq && _fromFilter == from && newMails.isNotEmpty) _showMail(0);
  }

  /* ---- reading ---- */

  void _showMail(int i) {
    final list = _activeList;
    if (i < 0 || i >= list.length) return;
    final m = list[i];
    setState(() {
      _sel = i;
      _listErr = '';
      _readHtml = wrapMailHTML('${m['html'] ?? ''}', '${m['preview'] ?? ''}');
    });
    final id = '${m['id'] ?? ''}';
    if (id.isNotEmpty && m['_full'] != true) {
      Api.post('/api/mail/body', {'email': widget.account.email, 'id': id},
              base: viewerBase)
          .then((res) {
        if (!mounted) return;
        final data = res.data;
        if (data is! Map || data['ok'] != true || data['html'] == null) return;
        if (_sel != i) return;
        m['html'] = data['html'];
        m['_full'] = true;
        setState(() => _readHtml = wrapMailHTML(
            '${data['html']}', '${m['preview'] ?? ''}'));
      });
    }
  }

  /* ---- deletes ---- */

  void _removeEverywhere(Map<String, dynamic> m) {
    final k = mailKeyOf(m);
    setState(() {
      _mails.removeWhere((x) => mailKeyOf(x) == k);
      _packs.forEach((_, pk) {
        pk.mails.removeWhere((x) => mailKeyOf(x) == k);
        if (pk.total > 0) pk.total -= 1;
      });
    });
  }

  void _askDelete(List<String> ids, String text, [String err = '']) {
    setState(() => _delAsk = _DeleteAsk(
        ids: ids,
        purge: _folder == 'deleted',
        text: text,
        err: err));
  }

  void _deleteMailAt(int i) {
    final list = _activeList;
    if (i < 0 || i >= list.length) return;
    final m = list[i];
    final id = '${m['id'] ?? ''}';
    if (id.isEmpty) {
      _askDelete([], 'Are you sure you want to delete this email?',
          'This message has no Outlook id, so it cannot be deleted.');
      return;
    }
    _askDelete([id], 'Are you sure you want to delete this email?');
  }

  void _deletePicked() {
    final ids = _picked.keys.where((k) => k.isNotEmpty).toList();
    if (ids.isEmpty) return;
    _askDelete(ids, 'Are you sure you want to delete these emails?');
  }

  Future<void> _confirmDelete() async {
    final d = _delAsk;
    if (d == null || d.ids.isEmpty) return;
    setState(() {
      d.busy = true;
      d.err = '';
    });
    final res = await Api.post('/api/mail/delete', {
      'email': widget.account.email,
      'ids': d.ids,
      'purge': d.purge,
    }, base: viewerBase);
    if (!mounted) return;
    if (!res.ok) {
      setState(() {
        d.busy = false;
        d.err = (res.data is Map && res.data['error'] != null)
            ? '${res.data['error']}'
            : 'Could not delete';
      });
      return;
    }
    final set = d.ids.toSet();
    for (final m in List<Map<String, dynamic>>.from(_activeList)) {
      if (set.contains('${m['id'] ?? ''}')) _removeEverywhere(m);
    }
    setState(() {
      if (_total > 0) _total = math.max(0, _total - d.ids.length);
      _picked.clear();
      _sel = -1;
      _selAll = false;
      _delAsk = null;
      _readHtml = null;
    });
  }

  Future<void> _emptyDeleted() async {
    setState(() => _folder = 'deleted');
    for (var n = 0; n < 30; n++) {
      final ids = _activeList
          .map((m) => '${m['id'] ?? ''}')
          .where((s) => s.isNotEmpty)
          .toList();
      if (ids.isEmpty) {
        if (!_more) break;
        await _loadPage(moreReq: true);
        continue;
      }
      final res = await Api.post('/api/mail/delete', {
        'email': widget.account.email,
        'ids': ids,
        'purge': true,
      }, base: viewerBase);
      if (!res.ok) break;
      final set = ids.toSet();
      for (final m in List<Map<String, dynamic>>.from(_activeList)) {
        if (set.contains('${m['id'] ?? ''}')) _removeEverywhere(m);
      }
      if (mounted) setState(() => _picked.clear());
      if (_more) await _loadPage(moreReq: true);
    }
    if (mounted) setState(() => _readHtml = null);
  }

  /* ---- proxy source ---- */

  Future<void> _loadPxLists() async {
    final r = await Api.get('/api/proxy-lists', base: viewerBase);
    if (!mounted) return;
    setState(() => _pxLists = r.data is List ? r.data : []);
  }

  List<dynamic> _pxRows(String kind) => _pxLists
      .where((x) =>
          x is Map && (kind == 'server' ? x['type'] == 'server' : x['type'] != 'server'))
      .toList();

  Future<void> _pxCheckFile() async {
    setState(() => _pxFileCount = 'checking…');
    final r = await Api.post('/api/proxy-fetch', {'path': _pxPath},
        base: viewerBase);
    if (!mounted) return;
    setState(() => _pxFileCount = r.ok && r.data is Map
        ? '${jint(r.data['count'])} proxies'
        : 'failed');
  }

  Future<void> _pxCheckServer() async {
    setState(() => _pxCount = 'checking…');
    final urls = _pxUrls.map((s) => s.trim()).where((s) => s.isNotEmpty).toList();
    final r = await Api.post('/api/proxy-fetch', {'urls': urls},
        base: viewerBase);
    if (!mounted) return;
    setState(() => _pxCount = r.ok && r.data is Map
        ? '${jint(r.data['count'])} proxies'
        : 'failed');
  }

  void _pxUseSaved(dynamic x) {
    if (x is! Map) return;
    if (_pxMode == 'server') {
      final urls = (x['urls'] is List && (x['urls'] as List).isNotEmpty)
          ? (x['urls'] as List).map((e) => '$e').toList()
          : [''];
      setState(() {
        _pxUrls = urls;
        _pxCount = '0 proxies';
        _pxMenu = '';
      });
    } else {
      setState(() {
        _pxPath = '${x['path'] ?? ''}';
        _pxFileCount = '0 proxies';
        _pxMenu = '';
      });
    }
  }

  Future<void> _pxBrowse() async {
    final r = await Api.get('/api/pick?kind=proxy', base: viewerBase);
    if (!mounted || r.data is! Map) return;
    final p = '${r.data['path'] ?? ''}';
    if (p.isNotEmpty) setState(() => _pxPath = p);
  }

  void _startMail(bool usePx) {
    if (usePx) {
      setState(() => _stage = 'px');
      _loadPxLists();
      return;
    }
    _proxy = null;
    _runConnect();
  }

  void _pxGo() {
    // Write the proxy BEFORE connecting: the connect call reads it right away.
    _proxy = {
      'proxy_mode': _pxMode.isEmpty ? 'file' : _pxMode,
      'proxy': _pxPath,
      'proxy_urls':
          _pxUrls.map((s) => s.trim()).where((s) => s.isNotEmpty).toList(),
    };
    _runConnect();
  }

  /* ---- animated sign-in (NDJSON stream) ---- */

  Future<void> _runConnect() async {
    setState(() {
      _stage = 'connect';
      _connStep = 0;
      _connProxy = '';
      _connTries.clear();
      _connErr = '';
    });
    final body = <String, dynamic>{
      'email': widget.account.email,
      'password': widget.account.password,
      ...?_proxy,
    };

    var step = 0;
    var proxy = '';
    final tries = <Map<String, dynamic>>[];
    Map<String, dynamic>? done;

    void handleLine(String raw) {
      final line = raw.trim();
      if (line.isEmpty) return;
      dynamic ev;
      try {
        ev = jsonDecode(line);
      } catch (_) {
        return;
      }
      if (ev is! Map) return;
      final e = Map<String, dynamic>.from(ev);
      final type = '${e['type'] ?? ''}';
      if (type == 'stage') {
        final i = kStageOrder.indexOf('${e['stage'] ?? ''}');
        if (i > 0) step = i;
      } else if (type == 'try') {
        if (e['start'] == true) {
          step = 0;
          proxy = '${e['proxy'] ?? ''}';
          tries.add({'proxy': proxy, 'stage': e['stage'], 'trying': true});
        } else {
          final row = <String, dynamic>{
            'proxy': '${e['proxy'] ?? ''}',
            'stage': e['stage'],
            'ok': e['ok'] == true,
            'err': '${e['err'] ?? ''}',
          };
          if (tries.isNotEmpty &&
              tries.last['trying'] == true &&
              tries.last['proxy'] == row['proxy']) {
            final last = tries.last;
            last['trying'] = false;
            last['ok'] = row['ok'];
            last['err'] = row['err'];
            last['stage'] = row['stage'];
          } else {
            tries.add(row);
          }
          proxy = '${row['proxy']}';
        }
      } else if (type == 'done') {
        done = e;
      }
    }

    try {
      final stream = await Api.ndjson('/api/mail/connect', body,
          base: viewerBase);
      var lastPaint = DateTime.now();
      await for (final ev in stream) {
        // Api.ndjson already parsed each line to a map; translate it back
        // through the same handler shape used by the web port.
        handleLine(jsonEncode(ev));
        final now = DateTime.now();
        if (now.difference(lastPaint).inMilliseconds > 60) {
          lastPaint = now;
          if (mounted && _stage == 'connect') {
            setState(() {
              _connStep = step;
              _connProxy = proxy;
              _connTries
                ..clear()
                ..addAll(tries.map((t) => Map<String, dynamic>.from(t)));
            });
          }
        }
      }
      if (!mounted) return;
      setState(() {
        _connStep = step;
        _connProxy = proxy;
        _connTries
          ..clear()
          ..addAll(tries.map((t) => Map<String, dynamic>.from(t)));
      });
      final d = done ?? {'ok': false, 'error': 'connection ended unexpectedly'};
      if (d['ok'] == true) {
        final msgs = jmaps(d['messages']);
        setState(() {
          _connStep = 4;
          _mails = msgs;
          _total = jint(d['total']);
          _more = d['more'] == true || _total > msgs.length;
          _stage = 'main';
        });
        return;
      }
      setState(() {
        _connErr = '${d['error'] ?? 'Could not sign in to this mailbox'}';
        _stage = 'error';
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _connErr = '$e';
        _stage = 'error';
      });
    }
  }

  /* ---- build ---- */

  @override
  Widget build(BuildContext context) {
    return Stack(
      fit: StackFit.expand,
      children: [
        Container(color: const Color(0xCC03050A)),
        _buildStage(context),
        if (_delAsk != null) _deleteModal(context),
      ],
    );
  }

  Widget _buildStage(BuildContext context) {
    switch (_stage) {
      case 'main':
        return _mainClient(context);
      case 'checking':
        return _smallCard(
          context,
          'EMAIL VIEWER',
          'Checking saved token…',
          _connBody(
            children: [
              const _ConnOrb(),
              const SizedBox(height: 14),
              const Text('Looking for a saved session',
                  style: TextStyle(fontSize: 13.5, fontWeight: FontWeight.w600)),
              const SizedBox(height: 4),
              Muted(widget.account.email, size: 12),
              const SizedBox(height: 14),
              _stepRow('Checking saved token', active: true),
              const SizedBox(height: 8),
              _stepRow('Loading inbox'),
              const SizedBox(height: 16),
              const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(
                      strokeWidth: 2, color: T.acc)),
            ],
          ),
          width: 420,
        );
      case 'saved':
        return _smallCard(
          context,
          'EMAIL VIEWER',
          'Signed in with saved token',
          _connBody(
            children: [
              const _ConnOrb(),
              const SizedBox(height: 14),
              const Text('Using saved token — no proxy needed',
                  style: TextStyle(fontSize: 13.5, fontWeight: FontWeight.w600)),
              const SizedBox(height: 4),
              Muted(widget.account.email, size: 12),
              const SizedBox(height: 14),
              _stepRow('Saved token found', done: true),
              const SizedBox(height: 8),
              _stepRow('Opening inbox', active: true),
              const SizedBox(height: 16),
              const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2, color: T.acc)),
            ],
          ),
          width: 420,
        );
      case 'ask':
        return _smallCard(
          context,
          'EMAIL VIEWER',
          'Use proxies?',
          Padding(
            padding: const EdgeInsets.fromLTRB(18, 0, 18, 18),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Muted('Use proxies for this mailbox login?', size: 13),
                const SizedBox(height: 14),
                Row(
                  children: [
                    TextBtn(label: 'No', onPressed: () => _startMail(false)),
                    const SizedBox(width: 10),
                    TextBtn(
                        label: 'Yes',
                        kind: BtnKind.primary,
                        onPressed: () => _startMail(true)),
                  ],
                ),
              ],
            ),
          ),
          width: 420,
        );
      case 'px':
        return _pxCard(context);
      case 'connect':
        return _smallCard(
          context,
          'EMAIL VIEWER',
          'Signing in…',
          _connBody(
            children: [
              const _ConnOrb(),
              const SizedBox(height: 14),
              Text(
                kStageLabel[kStageOrder[_connStep]] ?? 'Working…',
                textAlign: TextAlign.center,
                style: const TextStyle(
                    fontSize: 13.5, fontWeight: FontWeight.w600, color: T.txt),
              ),
              if (_connProxy.isNotEmpty) ...[
                const SizedBox(height: 5),
                Muted('via $_connProxy', size: 12),
              ],
              const SizedBox(height: 14),
              for (var i = 1; i < kStageOrder.length; i++) ...[
                _stepRow(
                  kStageLabel[kStageOrder[i]]!,
                  done: _connStep > i,
                  active: _connStep == i,
                ),
                const SizedBox(height: 8),
              ],
              if (_connTries.isNotEmpty) ...[
                const SizedBox(height: 6),
                _triesBox(),
              ],
              const SizedBox(height: 14),
              const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2, color: T.acc)),
            ],
          ),
          width: 460,
        );
      case 'error':
        return _smallCard(
          context,
          'EMAIL VIEWER',
          "Couldn't sign in",
          _connBody(
            children: [
              Container(
                width: 54,
                height: 54,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: const Color(0x24F87171),
                  border: Border.all(color: T.red),
                ),
                alignment: Alignment.center,
                child: const Text('!',
                    style: TextStyle(
                        fontSize: 26,
                        fontWeight: FontWeight.w800,
                        color: T.red)),
              ),
              const SizedBox(height: 14),
              Text(
                _connErr.isEmpty
                    ? 'Could not sign in to this mailbox'
                    : _connErr,
                textAlign: TextAlign.center,
                style: const TextStyle(fontSize: 13, color: T.txt, height: 1.45),
              ),
              if (_connTries.isNotEmpty) ...[
                const SizedBox(height: 14),
                _triesBox(),
              ],
              const SizedBox(height: 16),
              Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  TextBtn(
                      label: 'Retry',
                      kind: BtnKind.primary,
                      onPressed: _runConnect),
                  const SizedBox(width: 10),
                  TextBtn(
                    label: 'Change proxies',
                    kind: BtnKind.secondary,
                    onPressed: () {
                      setState(() => _stage = 'px');
                      _loadPxLists();
                    },
                  ),
                ],
              ),
            ],
          ),
          width: 460,
        );
      default:
        return const SizedBox.shrink();
    }
  }

  Widget _connBody({required List<Widget> children}) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 0, 20, 22),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: children,
      ),
    );
  }

  Widget _stepRow(String label, {bool done = false, bool active = false}) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 17,
          height: 17,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            border: Border.all(
                color: done ? T.grn : active ? T.acc : T.lineHi, width: 1.4),
            color: done ? const Color(0x3322C55E) : Colors.transparent,
          ),
          alignment: Alignment.center,
          child: done
              ? const Text('✓',
                  style:
                      TextStyle(fontSize: 10, fontWeight: FontWeight.w800, color: T.grn))
              : null,
        ),
        const SizedBox(width: 9),
        Flexible(
          child: Text(
            label,
            style: TextStyle(
              fontSize: 13,
              fontWeight: active || done ? FontWeight.w600 : FontWeight.w400,
              color: active ? T.txt : done ? T.grn : T.mut,
            ),
          ),
        ),
      ],
    );
  }

  Widget _triesBox() {
    return Container(
      width: double.infinity,
      constraints: const BoxConstraints(maxHeight: 140),
      padding: const EdgeInsets.all(9),
      decoration: BoxDecoration(
        color: T.bgSoft,
        border: Border.all(color: T.lineSoft),
        borderRadius: BorderRadius.circular(10),
      ),
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            for (final t in _connTries)
              Padding(
                padding: const EdgeInsets.only(bottom: 5),
                child: Row(
                  children: [
                    Container(
                      width: 7,
                      height: 7,
                      decoration: BoxDecoration(
                        shape: BoxShape.circle,
                        color: t['trying'] == true
                            ? T.blu
                            : t['ok'] == true
                                ? T.grn
                                : T.red,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        '${t['proxy'] ?? ''}',
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                            fontSize: 11.5,
                            fontFamily: 'Consolas',
                            color: Color(0xFFB6C2D8)),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Text(
                      t['trying'] == true
                          ? 'trying…'
                          : t['ok'] == true
                              ? 'connected'
                              : ('${t['err'] ?? ''}'.isEmpty
                                  ? 'failed'
                                  : '${t['err']}'),
                      style: TextStyle(
                        fontSize: 11.5,
                        color: t['trying'] == true
                            ? T.blu
                            : t['ok'] == true
                                ? T.grn
                                : T.red,
                      ),
                    ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _smallCard(
      BuildContext context, String eyebrow, String title, Widget body,
      {double width = 460}) {
    final maxW = math.min(width, MediaQuery.of(context).size.width - 32);
    return Center(
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: maxW),
        child: Container(
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
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _head(context, eyebrow, title),
              body,
            ],
          ),
        ),
      ),
    );
  }

  Widget _head(BuildContext context, String eyebrow, String title,
      {bool busyClose = false}) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(18, 15, 12, 12),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Muted(eyebrow),
                const SizedBox(height: 3),
                Text(title,
                    style: const TextStyle(
                        fontSize: 15, fontWeight: FontWeight.w700, color: T.txt)),
              ],
            ),
          ),
          TextBtn(
            label: 'Close',
            kind: BtnKind.secondary,
            disabled: busyClose,
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            onPressed: busyClose ? null : widget.onClose,
          ),
        ],
      ),
    );
  }

  /* ---- proxy source card ---- */

  Widget _pxCard(BuildContext context) {
    final maxW = math.min(520.0, MediaQuery.of(context).size.width - 32);
    return Center(
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: maxW),
        child: Container(
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
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _head(context, 'PROXY SOURCE', 'Choose list'),
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Seg(
                      options: const ['File', 'Server'],
                      value: _pxMode == 'server' ? 'Server' : 'File',
                      onChange: (v) =>
                          setState(() => _pxMode = v.toLowerCase()),
                    ),
                    const SizedBox(height: 12),
                    if (_pxMode != 'server')
                      _pxFileUI()
                    else
                      _pxServerUI(),
                    const SizedBox(height: 14),
                    Row(
                      children: [
                        TextBtn(
                            label: 'Continue',
                            kind: BtnKind.primary,
                            onPressed: _pxGo),
                        const SizedBox(width: 10),
                        TextBtn(
                          label: 'Cancel',
                          kind: BtnKind.secondary,
                          onPressed: () => setState(() => _stage = 'ask'),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _pxFileUI() {
    final rows = _pxRows('file');
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: TextField(
                style: const TextStyle(fontSize: 13, color: T.txt),
                decoration: fieldInputDecoration(hint: 'proxy file'),
                controller: TextEditingController.fromValue(
                    TextEditingValue(text: _pxPath, selection: TextSelection.collapsed(offset: _pxPath.length))),
                onChanged: (v) => _pxPath = v,
              ),
            ),
            const SizedBox(width: 8),
            TextBtn(label: 'Browse', kind: BtnKind.secondary, onPressed: _pxBrowse),
            const SizedBox(width: 8),
            TextBtn(label: 'Check', kind: BtnKind.secondary, onPressed: _pxCheckFile),
            const SizedBox(width: 8),
            Tag(text: _pxFileCount),
          ],
        ),
        const SizedBox(height: 10),
        Row(
          children: [
            TextBtn(
              label: _pxMenu == 'file' ? 'Saved lists ▾' : 'Saved lists',
              kind: BtnKind.secondary,
              onPressed: () {
                setState(
                    () => _pxMenu = _pxMenu == 'file' ? '' : 'file');
                _loadPxLists();
              },
            ),
          ],
        ),
        if (_pxMenu == 'file') ...[
          const SizedBox(height: 8),
          Container(
            constraints: const BoxConstraints(maxHeight: 180),
            width: double.infinity,
            padding: const EdgeInsets.all(6),
            decoration: BoxDecoration(
              color: T.bgSoft,
              border: Border.all(color: T.line),
              borderRadius: BorderRadius.circular(10),
            ),
            child: rows.isEmpty
                ? Padding(
                    padding: const EdgeInsets.all(6),
                    child: Muted('Nothing saved'),
                  )
                : SingleChildScrollView(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        for (final x in rows)
                          InkWell(
                            onTap: () => _pxUseSaved(x),
                            child: Padding(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 6, vertical: 6),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text('${(x as Map)['name'] ?? 'list'}',
                                      style: const TextStyle(
                                          fontSize: 12.5,
                                          fontWeight: FontWeight.w700)),
                                  Tag(
                                      text:
                                          '${x['count'] ?? 0} proxies · ${x['path'] ?? ''}'),
                                ],
                              ),
                            ),
                          ),
                      ],
                    ),
                  ),
          ),
        ],
      ],
    );
  }

  Widget _pxServerUI() {
    final rows = _pxRows('server');
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (var i = 0; i < _pxUrls.length; i++)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: TextField(
              style: const TextStyle(fontSize: 13, color: T.txt),
              decoration:
                  fieldInputDecoration(hint: 'https://list/proxies.txt'),
              controller: TextEditingController.fromValue(TextEditingValue(
                  text: _pxUrls[i],
                  selection: TextSelection.collapsed(
                      offset: _pxUrls[i].length))),
              onChanged: (v) => _pxUrls[i] = v,
            ),
          ),
        Row(
          children: [
            TextBtn(
              label: '+',
              kind: BtnKind.secondary,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
              onPressed: () => setState(() => _pxUrls = [..._pxUrls, '']),
            ),
            if (_pxUrls.length > 1) ...[
              const SizedBox(width: 8),
              TextBtn(
                label: '–',
                kind: BtnKind.secondary,
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                onPressed: () =>
                    setState(() => _pxUrls = _pxUrls.sublist(0, _pxUrls.length - 1)),
              ),
            ],
            const SizedBox(width: 8),
            TextBtn(label: 'Check', kind: BtnKind.secondary, onPressed: _pxCheckServer),
            const SizedBox(width: 8),
            Tag(text: _pxCount),
          ],
        ),
        const SizedBox(height: 10),
        TextBtn(
          label: _pxMenu == 'server' ? 'Saved servers ▾' : 'Saved servers',
          kind: BtnKind.secondary,
          onPressed: () {
            setState(() => _pxMenu = _pxMenu == 'server' ? '' : 'server');
            _loadPxLists();
          },
        ),
        if (_pxMenu == 'server') ...[
          const SizedBox(height: 8),
          Container(
            constraints: const BoxConstraints(maxHeight: 180),
            width: double.infinity,
            padding: const EdgeInsets.all(6),
            decoration: BoxDecoration(
              color: T.bgSoft,
              border: Border.all(color: T.line),
              borderRadius: BorderRadius.circular(10),
            ),
            child: rows.isEmpty
                ? Padding(
                    padding: const EdgeInsets.all(6),
                    child: Muted('Nothing saved'),
                  )
                : SingleChildScrollView(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        for (final x in rows)
                          InkWell(
                            onTap: () => _pxUseSaved(x),
                            child: Padding(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 6, vertical: 6),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.start,
                                children: [
                                  Text('${(x as Map)['name'] ?? 'list'}',
                                      style: const TextStyle(
                                          fontSize: 12.5,
                                          fontWeight: FontWeight.w700)),
                                  Tag(
                                      text:
                                          '${(x['urls'] as List?)?.length ?? 0} urls · ${x['count'] ?? 0} proxies'),
                                ],
                              ),
                            ),
                          ),
                      ],
                    ),
                  ),
          ),
        ],
      ],
    );
  }

  /* ---- main client ---- */

  Widget _mainClient(BuildContext context) {
    final size = MediaQuery.of(context).size;
    final w = math.min(1440.0, size.width - 28);
    final h = math.min(940.0, size.height - 28);
    return Center(
      child: Container(
        width: w,
        height: h,
        clipBehavior: Clip.antiAlias,
        decoration: BoxDecoration(
          color: T.card,
          border: Border.all(color: T.line),
          borderRadius: BorderRadius.circular(16),
          boxShadow: const [
            BoxShadow(
                color: Color(0x8C000000), blurRadius: 70, offset: Offset(0, 26)),
          ],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _mainHead(context),
            Expanded(
              child: LayoutBuilder(builder: (context, box) {
                final side = _sidePane();
                final list = _listPane();
                final read = _readPane();
                if (box.maxWidth >= 1120) {
                  return Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      side,
                      list,
                      Expanded(child: read),
                    ],
                  );
                }
                return SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      SizedBox(width: 240, child: side),
                      SizedBox(width: 350, child: list),
                      SizedBox(width: math.max(420.0, box.maxWidth - 590), child: read),
                    ],
                  ),
                );
              }),
            ),
          ],
        ),
      ),
    );
  }

  Widget _mainHead(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 11, 12, 11),
      decoration: const BoxDecoration(
        border: Border(bottom: BorderSide(color: T.line)),
      ),
      child: Row(
        children: [
          Container(
            width: 22,
            height: 22,
            decoration: BoxDecoration(
              color: const Color(0xFF1B2234),
              border: Border.all(color: T.lineSoft),
              borderRadius: BorderRadius.circular(7),
            ),
            alignment: Alignment.center,
            child: const Icon(Icons.mail_outline, size: 13, color: Colors.white),
          ),
          const SizedBox(width: 9),
          Text('EMAIL VIEWER',
              style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  letterSpacing: 0.6,
                  color: T.mut)),
          const SizedBox(width: 9),
          const Text('/', style: TextStyle(fontSize: 12, color: Color(0xFF46536E))),
          const SizedBox(width: 9),
          Expanded(
            child: Text(widget.account.email,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                    fontSize: 12.5, fontWeight: FontWeight.w700, color: T.txt)),
          ),
          const StatusPill(on: true, label: 'Online'),
          const SizedBox(width: 8),
          TextBtn(
            label: 'Close',
            kind: BtnKind.secondary,
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            onPressed: widget.onClose,
          ),
        ],
      ),
    );
  }

  /* ---- side pane (folders / senders / actions) ---- */

  Widget _sidePane() {
    final counts = <String, int>{};
    for (final m in _mails) {
      final k = senderKeyOf(m['from']);
      counts[k] = (counts[k] ?? 0) + 1;
    }
    final senderKeys = counts.keys.toList()
      ..sort((a, b) => counts[b]!.compareTo(counts[a]!));

    return Container(
      width: 250,
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 14),
      decoration: const BoxDecoration(
        border: Border(right: BorderSide(color: T.line)),
      ),
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // account block
            Row(
              children: [
                _Avatar(from: widget.account.email),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(widget.account.email,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                              fontSize: 12.5,
                              fontWeight: FontWeight.w700,
                              color: T.txt)),
                      Muted('MAILBOX', size: 10.5),
                    ],
                  ),
                ),
              ],
            ),
            const SizedBox(height: 14),
            Text('MAILBOX', style: labelStyle()),
            const SizedBox(height: 7),
            for (final f in kFolders) _folderBtn(f[0], f[1]),
            const SizedBox(height: 14),
            Text('SENDERS', style: labelStyle()),
            const SizedBox(height: 7),
            _senderItem(
              title: 'All senders',
              selected: _fromFilter.isEmpty,
              chips: [
                'Total messages: ${_total > 0 ? _total : _mails.length}',
                '${senderKeys.length} senders',
              ],
              onTap: () => setState(() {
                _fromFilter = '';
                _sel = -1;
              }),
            ),
            for (final k in senderKeys)
              _senderItem(
                avatar: k,
                title: k,
                selected: _fromFilter == k,
                chips: [
                  '${(_packs[k]?.total ?? 0) > 0 ? _packs[k]!.total : counts[k]}'
                ],
                onTap: () {
                  setState(() {
                    _fromFilter = k;
                    _sel = -1;
                  });
                  if (!_packs.containsKey(k)) _loadSenderPage(k, false);
                },
              ),
            const SizedBox(height: 16),
            const Divider(height: 1, color: T.lineSoft),
            const SizedBox(height: 10),
            Text('ACTIONS', style: labelStyle()),
            const SizedBox(height: 7),
            Row(
              children: [
                Checkbox(
                  value: _selAll,
                  materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  visualDensity: VisualDensity.compact,
                  onChanged: (on) {
                    final v = on == true;
                    setState(() {
                      _selAll = v;
                      _picked.clear();
                      if (v) {
                        for (final m in _activeList) {
                          final id = '${m['id'] ?? ''}';
                          if (id.isNotEmpty) _picked[id] = true;
                        }
                      }
                    });
                  },
                ),
                const SizedBox(width: 4),
                const Text('Select all',
                    style: TextStyle(fontSize: 12.5, color: T.txt)),
              ],
            ),
            const SizedBox(height: 6),
            TextBtn(
              label: 'Delete selected',
              kind: BtnKind.secondary,
              onPressed: _deletePicked,
            ),
            if (_folder == 'deleted') ...[
              const SizedBox(height: 6),
              TextBtn(
                label: 'Empty deleted',
                kind: BtnKind.danger,
                onPressed: _emptyDeleted,
              ),
            ],
            if (_picked.isNotEmpty) ...[
              const SizedBox(height: 6),
              Muted('${_picked.length} selected', size: 11.5),
            ],
          ],
        ),
      ),
    );
  }

  Widget _folderBtn(String id, String label) {
    final on = _folder == id;
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () {
        setState(() {
          _folder = id;
          _fromFilter = '';
          _appliedQuery = 'kind:email';
          _packs.clear();
          _picked.clear();
          _mails = [];
          _total = 0;
          _query = '';
          _more = false;
          _sel = -1;
          _selAll = false;
          _searchCtrl.clear();
        });
        _loadPage(fromFilterOverride: '');
      },
      child: Container(
        width: double.infinity,
        margin: const EdgeInsets.only(bottom: 3),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        decoration: BoxDecoration(
          color: on ? const Color(0x297C5CFF) : Colors.transparent,
          border: Border.all(
              color: on ? const Color(0x617C5CFF) : Colors.transparent),
          borderRadius: BorderRadius.circular(8),
        ),
        child: Text(label,
            style: TextStyle(
                fontSize: 13,
                fontWeight: on ? FontWeight.w700 : FontWeight.w600,
                color: on ? Colors.white : T.mut)),
      ),
    );
  }

  Widget _senderItem({
    String? avatar,
    required String title,
    required bool selected,
    required List<String> chips,
    required VoidCallback onTap,
  }) {
    return InkWell(
      borderRadius: BorderRadius.circular(9),
      onTap: onTap,
      child: Container(
        width: double.infinity,
        margin: const EdgeInsets.only(bottom: 3),
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 7),
        decoration: BoxDecoration(
          color: selected ? const Color(0x297C5CFF) : Colors.transparent,
          border: Border.all(
              color: selected ? const Color(0x617C5CFF) : Colors.transparent),
          borderRadius: BorderRadius.circular(9),
        ),
        child: Row(
          children: [
            if (avatar != null) ...[
              _Avatar(from: avatar),
              const SizedBox(width: 8),
            ],
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                          fontSize: 12.5,
                          fontWeight: FontWeight.w700,
                          color: selected ? Colors.white : T.txt)),
                  const SizedBox(height: 4),
                  Wrap(
                    spacing: 5,
                    children: [for (final c in chips) Tag(text: c, kind: 'green')],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  /* ---- list pane ---- */

  Widget _listPane() {
    final q = _query.trim().toLowerCase();
    final active = _activeList;
    final rows = <MapEntry<int, Map<String, dynamic>>>[];
    for (var i = 0; i < active.length; i++) {
      final m = active[i];
      if (q.isEmpty || q == '*') {
        rows.add(MapEntry(i, m));
        continue;
      }
      final t =
          ('${m['subject'] ?? ''} ${m['from'] ?? ''} ${m['preview'] ?? ''}')
              .toLowerCase();
      if (t.contains(q)) rows.add(MapEntry(i, m));
    }
    final pack = _fromFilter.isNotEmpty ? _packs[_fromFilter] : null;
    final hasMore = _fromFilter.isNotEmpty
        ? (pack?.more ?? false)
        : _more;
    final folderLabel = kFolders.firstWhere((f) => f[0] == _folder,
        orElse: () => ['', ''])[1];

    return Container(
      width: 370,
      padding: const EdgeInsets.fromLTRB(13, 12, 13, 10),
      decoration: const BoxDecoration(
        border: Border(right: BorderSide(color: T.line)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                    (_fromFilter.isNotEmpty ? _fromFilter : folderLabel)
                        .toUpperCase(),
                    overflow: TextOverflow.ellipsis,
                    style: eyebrowStyle()),
              ),
              Tag(
                  text: '${_total > 0 ? _total : active.length} messages',
                  kind: ''),
            ],
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _searchCtrl,
                  style: const TextStyle(fontSize: 13, color: T.txt),
                  decoration: fieldInputDecoration(hint: 'Search mailbox'),
                  onChanged: (v) => setState(() => _query = v),
                  onSubmitted: (_) {
                    final nq = _query.trim().isEmpty
                        ? 'kind:email'
                        : _query.trim();
                    setState(() => _appliedQuery = nq);
                    _loadPage();
                  },
                ),
              ),
              const SizedBox(width: 8),
              TextBtn(
                label: 'Search',
                kind: BtnKind.secondary,
                onPressed: () {
                  final nq =
                      _query.trim().isEmpty ? 'kind:email' : _query.trim();
                  setState(() => _appliedQuery = nq);
                  _loadPage();
                },
              ),
            ],
          ),
          const SizedBox(height: 10),
          Expanded(
            child: ListView(
              children: [
                if (_listErr.isNotEmpty && rows.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.all(10),
                    child: Muted(_listErr, size: 12, color: T.red),
                  ),
                if (rows.isEmpty)
                  Padding(
                    padding: const EdgeInsets.all(14),
                    child: Muted(
                        _listErr.isNotEmpty ? _listErr : 'No messages',
                        size: 12.5,
                        color: _listErr.isNotEmpty ? T.red : null),
                  ),
                if (rows.isEmpty && hasMore)
                  Center(
                    child: TextBtn(
                      label: 'Load more',
                      kind: BtnKind.secondary,
                      onPressed: () => _loadPage(moreReq: true),
                    ),
                  ),
                for (final e in rows) _mailItem(e.key, e.value),
                if (rows.isNotEmpty && hasMore)
                  Center(
                    child: TextBtn(
                      label: _fromFilter.isNotEmpty
                          ? 'Load more${(_packs[_fromFilter]?.total ?? 0) > 0 ? ' · ${_packs[_fromFilter]!.mails.length} / ${_packs[_fromFilter]!.total}' : ''}'
                          : 'Load more${_total > 0 ? ' · ${_mails.length} / $_total' : ''}',
                      kind: BtnKind.secondary,
                      onPressed: () => _loadPage(moreReq: true),
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _mailItem(int i, Map<String, dynamic> m) {
    final id = '${m['id'] ?? ''}';
    final on = _sel == i;
    return InkWell(
      borderRadius: BorderRadius.circular(10),
      onTap: () => _showMail(i),
      child: Container(
        margin: const EdgeInsets.only(bottom: 4),
        padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 9),
        decoration: BoxDecoration(
          color: on ? const Color(0x297C5CFF) : Colors.transparent,
          border: Border.all(
              color: on ? const Color(0x617C5CFF) : Colors.transparent),
          borderRadius: BorderRadius.circular(10),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (id.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Checkbox(
                  value: _picked[id] == true,
                  materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  visualDensity: VisualDensity.compact,
                  onChanged: (v) => setState(() {
                    if (v == true) {
                      _picked[id] = true;
                    } else {
                      _picked.remove(id);
                    }
                    _selAll = false;
                  }),
                ),
              ),
            const SizedBox(width: 6),
            _Avatar(from: '${m['from'] ?? ''}'),
            const SizedBox(width: 9),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Expanded(
                        child: Text(
                          '${m['subject'] ?? ''}'.isEmpty
                              ? '(no subject)'
                              : '${m['subject']}',
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                              fontSize: 12.5,
                              fontWeight: FontWeight.w700,
                              color: T.txt),
                        ),
                      ),
                      InkWell(
                        onTap: () => _deleteMailAt(i),
                        child: const Padding(
                          padding: EdgeInsets.all(2),
                          child: Text('✕',
                              style: TextStyle(fontSize: 12, color: T.mut)),
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 5),
                  Wrap(
                    spacing: 5,
                    runSpacing: 4,
                    children: [
                      if ('${m['from'] ?? ''}'.isNotEmpty)
                        Tag(
                            text: displayFromEmail(m['from']), kind: 'yellow'),
                      if ('${m['date'] ?? ''}'.isNotEmpty)
                        Tag(text: formatMailDate(m['date']), kind: 'blue'),
                    ],
                  ),
                  const SizedBox(height: 6),
                  Text(
                    '${m['preview'] ?? ''}'.length > 110
                        ? '${m['preview']}'.substring(0, 110)
                        : '${m['preview'] ?? ''}',
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: mutedStyle(11.5),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  /* ---- reader pane ---- */

  Widget _readPane() {
    final active = _activeList;
    if (_sel < 0 || _sel >= active.length) {
      return Center(child: Muted('Select a message', size: 13));
    }
    final m = active[_sel];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          decoration: const BoxDecoration(
            border: Border(bottom: BorderSide(color: T.lineSoft)),
          ),
          child: Row(
            children: [
              TextBtn(
                label: 'Delete',
                kind: BtnKind.danger,
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                onPressed: () => _deleteMailAt(_sel),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(displayFromEmail(m['from']),
                    overflow: TextOverflow.ellipsis,
                    style: mutedStyle(12)),
              ),
            ],
          ),
        ),
        Expanded(
          child: Padding(
            padding: const EdgeInsets.all(14),
            child: Container(
              decoration: BoxDecoration(
                color: const Color(0xF00E1320),
                border: Border.all(color: T.line),
                borderRadius: BorderRadius.circular(12),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 15, 16, 12),
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        _Avatar(from: '${m['from'] ?? ''}', big: true),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                '${m['subject'] ?? ''}'.isEmpty
                                    ? '(no subject)'
                                    : '${m['subject']}',
                                style: const TextStyle(
                                    fontSize: 16.5,
                                    fontWeight: FontWeight.w800,
                                    color: T.txt,
                                    height: 1.3),
                              ),
                              const SizedBox(height: 8),
                              Wrap(
                                spacing: 6,
                                runSpacing: 6,
                                children: [
                                  if ('${m['from'] ?? ''}'.isNotEmpty)
                                    Tag(
                                        text: displayFromEmail(m['from']),
                                        kind: 'yellow'),
                                  if ('${m['date'] ?? ''}'.isNotEmpty)
                                    Tag(
                                        text: formatMailDate(m['date']),
                                        kind: 'blue'),
                                ],
                              ),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
                  const Divider(height: 1, color: T.lineSoft),
                  Expanded(
                    child: Container(
                      color: Colors.white,
                      padding: const EdgeInsets.all(4),
                      child: SingleChildScrollView(
                        child: Html(data: _readHtml ?? ''),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ],
    );
  }

  /* ---- delete confirm modal ---- */

  Widget _deleteModal(BuildContext context) {
    final d = _delAsk!;
    return Positioned.fill(
      child: Container(
        color: const Color(0xB303050A),
        alignment: Alignment.center,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 460),
          child: Container(
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
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Muted('DELETE'),
                          const SizedBox(height: 3),
                          const Text('Are you sure?',
                              style: TextStyle(
                                  fontSize: 15,
                                  fontWeight: FontWeight.w700)),
                        ],
                      ),
                    ),
                    TextBtn(
                      label: 'Close',
                      kind: BtnKind.secondary,
                      padding: const EdgeInsets.symmetric(
                          horizontal: 12, vertical: 6),
                      onPressed: () => setState(() => _delAsk = null),
                    ),
                  ],
                ),
                const SizedBox(height: 16),
                Muted(d.text, size: 13),
                const SizedBox(height: 16),
                Row(
                  children: [
                    TextBtn(
                      label: d.busy ? 'Deleting…' : 'Yes',
                      kind: BtnKind.danger,
                      disabled: d.busy || d.ids.isEmpty,
                      onPressed: d.busy || d.ids.isEmpty ? null : _confirmDelete,
                    ),
                    const SizedBox(width: 10),
                    TextBtn(
                      label: 'No',
                      kind: BtnKind.secondary,
                      onPressed: () => setState(() => _delAsk = null),
                    ),
                  ],
                ),
                if (d.err.isNotEmpty) ...[
                  const SizedBox(height: 10),
                  Muted(d.err, size: 12, color: T.red),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
