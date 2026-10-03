// Dashboard: combo editor — port of dash/editor.mjs (load/clean, generate,
// signatures) with SSE streams for analyze + filter.
import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../theme.dart';
import '../widgets.dart';

String fmtSize(num n) {
  final v = n.toDouble();
  if (v < 1024) return '${v.toInt()} B';
  if (v < 1048576) return '${(v / 1024).toStringAsFixed(1)} KB';
  if (v < 1073741824) return '${(v / 1048576).toStringAsFixed(1)} MB';
  return '${(v / 1073741824).toStringAsFixed(2)} GB';
}

class StatEntry {
  final String name;
  final num count;
  StatEntry({required this.name, required this.count});
  factory StatEntry.from(Map m) => StatEntry(
      name: '${m['name'] ?? ''}',
      count: (m['count'] is num) ? m['count'] as num : 0);
}

class EditorView extends StatefulWidget {
  final VoidCallback onHome;
  const EditorView({super.key, required this.onHome});

  @override
  State<EditorView> createState() => _EditorViewState();
}

class _EditorViewState extends State<EditorView> {
  String _view = 'home'; // home | work
  String _mode = 'load'; // load | new
  String _tab = 'load'; // load | new | sign

  String _path = '';
  int _lines = 0;
  int _size = 0;
  String _msg = '';

  List<StatEntry> _domains = [];
  List<StatEntry> _urls = [];
  List<StatEntry> _filterAll = [];

  String _mailTypes = '';
  String _urlFilter = '';
  String _filterState = 'idle'; // idle | running | done
  String _filterStatus = '';

  List<Map<String, dynamic>> _banners = [];
  int _bannerSel = -1;
  final _bannerText = TextEditingController();
  final _bannerName = TextEditingController();

  String _nameMode = 'paste';
  final _users = TextEditingController();
  final _namePath = TextEditingController();
  final _doms =
      TextEditingController(text: 'outlook.com, hotmail.com, gmail.com');
  String _passMode = 'paste';
  final _passes = TextEditingController();
  final _passPath = TextEditingController();
  final _count = TextEditingController(text: '500');
  String _genOut = '';

  String _sigHead = '';
  String _sigTail = '';
  String _sigHeadGen = '';
  String _sigTailGen = '';
  final _savePathGen = TextEditingController();

  // Persistent controllers — rebuilt-per-build controllers (the old pattern)
  // got their cursor reset by every parent rebuild while typing.
  late final TextEditingController _pathCtrl;
  late final TextEditingController _mailTypesCtrl;
  late final TextEditingController _urlFilterCtrl;
  late final TextEditingController _genOutCtrl;

  StreamSubscription<Map<String, dynamic>>? _anSub;
  StreamSubscription<Map<String, dynamic>>? _fSub;

  @override
  void initState() {
    super.initState();
    _pathCtrl = TextEditingController(text: _path);
    _mailTypesCtrl = TextEditingController(text: _mailTypes);
    _urlFilterCtrl = TextEditingController(text: _urlFilter);
    _genOutCtrl = TextEditingController(text: _genOut);
    _loadBanners();
  }

  @override
  void dispose() {
    _anSub?.cancel();
    _fSub?.cancel();
    _bannerText.dispose();
    _bannerName.dispose();
    _users.dispose();
    _namePath.dispose();
    _doms.dispose();
    _passes.dispose();
    _passPath.dispose();
    _count.dispose();
    _savePathGen.dispose();
    _pathCtrl.dispose();
    _mailTypesCtrl.dispose();
    _urlFilterCtrl.dispose();
    _genOutCtrl.dispose();
    super.dispose();
  }

  Future<void> _loadBanners() async {
    final r = await Api.get('/api/editor/banners');
    final out = <Map<String, dynamic>>[];
    if (r.data is Map && r.data['banners'] is List) {
      for (final b in r.data['banners'] as List) {
        if (b is Map) out.add(Map<String, dynamic>.from(b));
      }
    }
    if (mounted) setState(() => _banners = out);
  }

  String _sigTextOf(String idx) {
    final i = int.tryParse(idx) ?? -1;
    if (i < 0 || i >= _banners.length) return '';
    return (_banners[i]['text'] ?? '') as String;
  }

  void _openWork(String m) {
    setState(() {
      _mode = m;
      _tab = m;
      _view = 'work';
    });
  }

  void _paint(Map<String, dynamic> d) {
    if (d['error'] != null) {
      setState(() => _msg = '${d['error']}');
      return;
    }
    setState(() {
      _msg = d['done'] == true
          ? 'Analyzed ${d['lines'] ?? 0} lines'
          : 'Analyzing… ${d['lines'] ?? 0} lines';
      if (d['path'] != null) {
        _path = '${d['path']}';
        _pathCtrl.text = _path;
      }
      _lines = (d['lines'] is num) ? (d['lines'] as num).toInt() : 0;
      _size = (d['size'] is num) ? (d['size'] as num).toInt() : 0;
      _domains = _statList(d['domains']);
      _urls = _statList(d['urls']);
    });
  }

  List<StatEntry> _statList(dynamic v) {
    final out = <StatEntry>[];
    if (v is List) {
      for (final x in v) {
        if (x is Map) out.add(StatEntry.from(x));
      }
    }
    return out;
  }

  Future<void> _loadFile([String? p0]) async {
    final p = (p0 ?? _path).trim();
    if (p.isEmpty) {
      setState(() => _msg = 'Choose a file first');
      return;
    }
    setState(() => _msg = 'Analyzing…');
    await _anSub?.cancel();
    final stream = await Api.sse(
        '/api/editor/analyze-stream?path=${Uri.encodeComponent(p)}');
    _anSub = stream.listen((d) {
      _paint(d);
      if (d['done'] == true) {
        _anSub?.cancel();
        setState(() => _msg = 'Analyzed ${d['lines'] ?? 0} lines');
      }
    }, onError: (_) => _anSub?.cancel());
  }

  Future<void> _browse(String kind) async {
    final r = await Api.get('/api/pick?kind=${Uri.encodeComponent(kind)}');
    if (r.data is! Map || r.data['path'] == null) return;
    final p = r.data['path'] as String;
    if (kind == 'editor') {
      setState(() {
        _path = p;
        _pathCtrl.text = p;
      });
      _loadFile(p);
    } else if (kind == 'editor-names') {
      setState(() => _namePath.text = p);
      final res = await Api.post('/api/editor/analyze', {'path': p});
      if (res.data is Map) {
        final d = res.data;
        setState(() => _users.text = d['large'] == true
            ? '${d['preview'] ?? ''}'
            : '${d['text'] ?? d['preview'] ?? ''}');
      }
    } else if (kind == 'editor-pass') {
      setState(() => _passPath.text = p);
      final res = await Api.post('/api/editor/analyze', {'path': p});
      if (res.data is Map) {
        final d = res.data;
        setState(() => _passes.text = d['large'] == true
            ? '${d['preview'] ?? ''}'
            : '${d['text'] ?? d['preview'] ?? ''}');
      }
    }
  }

  Future<void> _saveOne(String kind, String name) async {
    final def = '${name.replaceAll(RegExp(r'[^\w.\-]+'), '_')}.txt';
    final r = await Api.get('/api/pick-save?name=${Uri.encodeComponent(def)}');
    if (r.data is! Map || r.data['path'] == null) return;
    setState(() => _msg = 'Exporting $name…');
    final body = <String, dynamic>{
      'path': _path,
      'dest': r.data['path'],
      'banner': _sigTextOf(_sigHead),
      'banner_end': _sigTextOf(_sigTail),
    };
    if (kind == 'email') {
      body['email_types'] = '@$name';
    } else {
      body['url'] = name;
    }
    final res = await Api.post('/api/editor/filter', body);
    if (!res.ok) {
      setState(() => _msg =
          (res.data is Map && res.data['error'] != null)
              ? '${res.data['error']}'
              : 'Save failed');
      return;
    }
    setState(() => _msg =
        'Saved ${res.data is Map ? res.data['extracted'] ?? 0 : 0} to '
        '${res.data is Map ? res.data['dest'] ?? r.data['dest'] : r.data['dest']}');
  }

  Future<void> _runFilter() async {
    setState(() {
      _filterState = 'running';
      _filterStatus = 'Filtering…';
    });
    await _fSub?.cancel();
    // A file without URLs must never be filtered by a stale URL typed for a
    // previous file — the URL filter is ignored when the file has none.
    final effUrl = _urls.isNotEmpty ? _urlFilter : '';
    final q = 'path=${Uri.encodeComponent(_path)}'
        '&email_types=${Uri.encodeComponent(_mailTypes)}'
        '&url=${Uri.encodeComponent(effUrl)}';
    final stream = await Api.sse('/api/editor/filter-stream?$q');
    _fSub = stream.listen((d) {
      setState(() {
        if (d['domains'] is List) _filterAll = _statList(d['domains']);
        if (d['done'] == true) {
          _filterState = 'done';
          _filterStatus = 'Filtered ${d['extracted'] ?? 0} combos';
        } else {
          _filterStatus =
              'Filtering… ${d['extracted'] ?? 0} hits / ${d['scanned'] ?? 0} scanned';
        }
      });
      if (d['done'] == true) _fSub?.cancel();
    }, onError: (_) {
      _fSub?.cancel();
      setState(() {
        _filterState = 'idle';
        _filterStatus = 'Filter stopped';
      });
    });
  }

  Future<void> _exportFiltered() async {
    final effUrl = _urls.isNotEmpty ? _urlFilter : '';
    final url = effUrl.isEmpty
        ? 'filtered'
        : effUrl.replaceAll(RegExp(r'[^\w.\-]+'), '_');
    final r = await Api.get('/api/pick-save?name=${Uri.encodeComponent('$url.txt')}');
    if (r.data is! Map || r.data['path'] == null) return;
    setState(() => _filterStatus = 'Saving…');
    final res = await Api.post('/api/editor/filter', {
      'path': _path,
      'dest': r.data['path'],
      'email_types': _mailTypes,
      'url': effUrl,
      'banner': _sigTextOf(_sigHead),
      'banner_end': _sigTextOf(_sigTail),
    });
    if (!res.ok) {
      setState(() => _filterStatus =
          (res.data is Map && res.data['error'] != null)
              ? '${res.data['error']}'
              : 'Save failed');
      return;
    }
    setState(() {
      _filterStatus =
          'Saved ${res.data is Map ? res.data['extracted'] ?? 0 : 0} combos to '
          '${res.data is Map ? res.data['dest'] ?? '' : ''}';
    });
  }

  Future<void> _exportGenerated() async {
    final r = await Api.get('/api/pick-save?name=combos-export.txt');
    if (r.data is! Map || r.data['path'] == null) return;
    String text = _genOut;
    final head = _sigTextOf(_sigHeadGen);
    final tail = _sigTextOf(_sigTailGen);
    if (head.isNotEmpty) text = '${head.trimRight()}\n$text';
    if (tail.isNotEmpty) text = '${text.trimRight()}\n$tail';
    await Api.post('/api/editor/save', {
      'path': r.data['path'],
      'text': text,
    });
  }

  Future<void> _saveGenerated() async {
    var p = _savePathGen.text.trim();
    if (p.isEmpty) {
      final r = await Api.get('/api/pick-save');
      p = (r.data is Map ? r.data['path'] as String? : null) ?? 'combos-export.txt';
      _savePathGen.text = p;
    }
    final res = await Api.post('/api/editor/save', {
      'path': p,
      'text': _genOut,
      'banner': _sigTextOf(_sigHeadGen).isNotEmpty
          ? _sigTextOf(_sigHeadGen)
          : _sigTextOf(_sigHead),
      'banner_end': _sigTextOf(_sigTailGen).isNotEmpty
          ? _sigTextOf(_sigTailGen)
          : _sigTextOf(_sigTail),
    });
    if (res.ok) {
      setState(() => _msg = 'Exported to $p');
    } else {
      setState(() => _msg = (res.data is Map && res.data['error'] != null)
          ? '${res.data['error']}'
          : 'Save failed');
    }
  }

  Future<void> _generate() async {
    final r = await Api.post('/api/editor/generate', {
      'users': _users.text,
      'domains': _doms.text,
      'passwords': _passes.text,
      'count': int.tryParse(_count.text) ?? 500,
    });
    if (r.data is Map) {
      setState(() {
        _genOut = '${r.data['text'] ?? ''}';
        _genOutCtrl.text = _genOut;
      });
      _paint(Map<String, dynamic>.from(r.data));
    }
  }

  Future<void> _deleteSig() async {
    if (_bannerSel < 0 || _bannerSel >= _banners.length) return;
    final b = _banners[_bannerSel];
    final name = (b['name'] ?? '') as String;
    if (name.isEmpty) return;
    await Api.delete('/api/editor/banners?name=${Uri.encodeComponent(name)}');
    _bannerText.clear();
    _bannerName.clear();
    _loadBanners();
  }

  Future<void> _saveSig() async {
    if (_bannerName.text.trim().isEmpty || _bannerText.text.isEmpty) {
      setState(() => _msg = 'Name and signature text required');
      return;
    }
    final r = await Api.post('/api/editor/banners', {
      'name': _bannerName.text.trim(),
      'text': _bannerText.text,
    });
    if (r.ok) {
      setState(() => _msg = 'Signature saved');
      _loadBanners();
    } else {
      setState(() => _msg = 'Save failed');
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_view == 'home') return _home();
    return _work();
  }

  void _openListModal(String kind) {
    showAppModal<void>(
      context,
      StatefulBuilder(builder: (context, setModal) {
        final items = kind == 'host'
            ? _urls
            : kind == 'filter'
                ? _filterAll
                : _domains;
        final saveKind = kind == 'host' ? 'host' : 'email';
        final title = kind == 'host'
            ? 'URL / DOMAINS'
            : kind == 'filter'
                ? 'FILTERED EMAIL TYPES'
                : 'EMAIL TYPES';
        return Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(child: Muted(title)),
                TextBtn(
                    label: 'Close',
                    onPressed: () => Navigator.of(context).pop()),
              ],
            ),
            ...items.map((x) =>
                _StatRow(x: x, kind: saveKind, onSave: _saveOne)),
          ],
        );
      }),
    );
  }

  void _openExportModal() {
    showAppModal<void>(
      context,
      StatefulBuilder(builder: (context, setModal) {
        return Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Muted('EXPORT FILTERED'),
            const SizedBox(height: 12),
            Text('START SIGNATURE', style: labelStyle()),
            const SizedBox(height: 6),
            _sigSelect(
                _sigHead.isEmpty ? -1 : int.tryParse(_sigHead) ?? -1,
                (v) => setModal(() => _sigHead = '$v')),
            const SizedBox(height: 12),
            Text('END SIGNATURE', style: labelStyle()),
            const SizedBox(height: 6),
            _sigSelect(
                _sigTail.isEmpty ? -1 : int.tryParse(_sigTail) ?? -1,
                (v) => setModal(() => _sigTail = '$v')),
            const SizedBox(height: 16),
            Row(
              children: [
                TextBtn(
                    label: 'Save',
                    kind: BtnKind.primary,
                    onPressed: () {
                      Navigator.of(context).pop();
                      _exportFiltered();
                    }),
                const SizedBox(width: 8),
                TextBtn(label: 'Cancel', onPressed: () {
                  Navigator.of(context).pop();
                }),
              ],
            ),
          ],
        );
      }),
    );
  }

  void _openGenExportModal() {
    showAppModal<void>(
      context,
      StatefulBuilder(builder: (context, setModal) {
        return Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Muted('EXPORT GENERATED'),
            const SizedBox(height: 12),
            Text('FILE NAME', style: labelStyle()),
            const SizedBox(height: 6),
            Row(
              children: [
                Expanded(
                  child: TextInput(
                      hint: 'combos-export.txt',
                      controller: _savePathGen),
                ),
                const SizedBox(width: 8),
                TextBtn(label: 'Browse', onPressed: () async {
                  final r = await Api.get('/api/pick-save');
                  if (r.data is Map && r.data['path'] != null) {
                    _savePathGen.text = '${r.data['path']}';
                    setModal(() {});
                  }
                }),
              ],
            ),
            const SizedBox(height: 12),
            Text('START SIGNATURE', style: labelStyle()),
            const SizedBox(height: 6),
            _sigSelect(
                _sigHeadGen.isEmpty ? -1 : int.tryParse(_sigHeadGen) ?? -1,
                (v) => setModal(() => _sigHeadGen = '$v')),
            const SizedBox(height: 12),
            Text('END SIGNATURE', style: labelStyle()),
            const SizedBox(height: 6),
            _sigSelect(
                _sigTailGen.isEmpty ? -1 : int.tryParse(_sigTailGen) ?? -1,
                (v) => setModal(() => _sigTailGen = '$v')),
            const SizedBox(height: 16),
            Row(
              children: [
                TextBtn(
                    label: 'Save',
                    kind: BtnKind.primary,
                    onPressed: () {
                      Navigator.of(context).pop();
                      _exportGenerated();
                    }),
                const SizedBox(width: 8),
                TextBtn(label: 'Cancel', onPressed: () {
                  Navigator.of(context).pop();
                }),
              ],
            ),
          ],
        );
      }),
    );
  }

  /* ---------------- home ---------------- */

  Widget _home() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SectionHead(
          eyebrow: 'Combo editor',
          title: 'What do you want to do?',
          sub: 'Open an existing list or generate a fresh one.',
        ),
        LayoutBuilder(builder: (context, box) {
          final cols = box.maxWidth < 700 ? 1 : 2;
          return GridView.count(
            crossAxisCount: cols,
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            crossAxisSpacing: 12,
            mainAxisSpacing: 12,
            childAspectRatio: cols == 1 ? 3.0 : 2.1,
            children: [
              ChoiceCard(
                icon: const IcoBox(child: IconDownload(size: 18)),
                title: 'Load file',
                desc:
                    'Open a combo or ULP list. Large files are scanned, not loaded into RAM.',
                onTap: () => _openWork('load'),
              ),
              ChoiceCard(
                icon: const IcoBox(child: IconNew(size: 18)),
                title: 'Create new',
                desc: 'Generate combos from names, domains and a password list.',
                onTap: () => _openWork('new'),
              ),
            ],
          );
        }),
        const SizedBox(height: 16),
        Row(
          children: [
            TextBtn(label: 'Back', onPressed: widget.onHome),
          ],
        ),
      ],
    );
  }

  /* ---------------- work ---------------- */

  Widget _work() {
    final eyebrow = _tab == 'new'
        ? 'GENERATE'
        : _tab == 'sign'
            ? 'SIGNATURES'
            : 'LOAD / CLEAN';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SectionHead(
          eyebrow: eyebrow,
          title: 'Combo editor',
          actions: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Tag(text: '$_lines lines'),
              const SizedBox(width: 6),
              Tag(kind: 'green', text: fmtSize(_size)),
            ],
          ),
        ),
        Container(
          padding: const EdgeInsets.all(3),
          decoration: BoxDecoration(
            color: T.bgSoft,
            border: Border.all(color: T.line),
            borderRadius: BorderRadius.circular(11),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (_mode == 'load')
                _TabBtn('File', _tab == 'load', () => setState(() => _tab = 'load')),
              if (_mode == 'new')
                _TabBtn(
                    'Generate', _tab == 'new', () => setState(() => _tab = 'new')),
              _TabBtn(
                  'Signatures', _tab == 'sign', () => setState(() => _tab = 'sign')),
            ],
          ),
        ),
        const SizedBox(height: 14),
        if (_tab == 'load') _loadTab(),
        if (_tab == 'new') _newTab(),
        if (_tab == 'sign') _signTab(),
        const SizedBox(height: 20),
        Row(
          children: [
            TextBtn(
                label: 'Back',
                onPressed: () => setState(() => _view = 'home')),
            const SizedBox(width: 8),
            TextBtn(label: 'Home', onPressed: widget.onHome),
          ],
        ),
      ],
    );
  }

  Widget _loadTab() {
    // Staged reveal: right after opening, only the FILE field + Browse are
    // shown. The stats panes and the filter row appear once a file has been
    // picked/analyzed, so the screen shows exactly what applies to it.
    final analyzed = _lines > 0 ||
        _domains.isNotEmpty ||
        _urls.isNotEmpty ||
        _msg.startsWith('Analyzing');
    // The URL column and URL filter only make sense when the file actually
    // holds URL lines — otherwise the email types get the full width and the
    // filter row is email-only.
    final hasUrls = _urls.isNotEmpty;

    final children = <Widget>[
      Text('FILE', style: labelStyle()),
      const SizedBox(height: 6),
      Row(
        children: [
          Expanded(
            child: TextInput(
              hint: 'combo / ULP path',
              controller: _pathCtrl,
              onChanged: (v) => _path = v,
            ),
          ),
          const SizedBox(width: 8),
          TextBtn(label: 'Browse', onPressed: () => _browse('editor')),
        ],
      ),
      if (_msg.isNotEmpty)
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 6),
          child: Muted(_msg, color: const Color(0xFF9AF0C8)),
        ),
    ];

    if (!analyzed) {
      children.addAll(const [
        SizedBox(height: 4),
        Muted('Pick a file to see its email types, URL stats and filters.'),
      ]);
      return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch, children: children);
    }

    children.add(const SizedBox(height: 8));
    final topEmails = _statListPane(
        'TOP EMAIL TYPES', _domains.take(10).toList(), 'email',
        () => _openListModal('email'));
    if (hasUrls) {
      final topUrls = _statListPane(
          'TOP URL / DOMAINS', _urls.take(10).toList(), 'host',
          () => _openListModal('host'));
      children.add(LayoutBuilder(builder: (context, box) {
        final split = box.maxWidth >= 1100;
        if (!split) {
          return Column(children: [
            topEmails,
            const SizedBox(height: 16),
            topUrls,
          ]);
        }
        return Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(child: topEmails),
            const SizedBox(width: 20),
            Expanded(child: topUrls),
          ],
        );
      }));
    } else {
      // No URLs in the file: the email types span the full width instead of
      // sitting on one half next to an empty column.
      children.add(topEmails);
    }

    children.add(const SizedBox(height: 16));
    final emailField = Field(
      label: 'Email types',
      child: TextInput(
        hint: '@outlook @hotmail @msn',
        controller: _mailTypesCtrl,
        onChanged: (v) {
          _mailTypes = v;
          _filterState = 'idle';
        },
      ),
    );
    if (hasUrls) {
      final urlField = Field(
        label: 'URL / domain',
        child: TextInput(
          hint: 'epicgames.com',
          controller: _urlFilterCtrl,
          onChanged: (v) {
            _urlFilter = v;
            _filterState = 'idle';
          },
        ),
      );
      children.add(LayoutBuilder(builder: (context, box) {
        if (box.maxWidth < 640) {
          return Column(children: [
            emailField,
            const SizedBox(height: 16),
            urlField,
          ]);
        }
        return Row(children: [
          Expanded(child: emailField),
          const SizedBox(width: 20),
          Expanded(child: urlField),
        ]);
      }));
    } else {
      children.add(emailField);
    }

    children.add(const SizedBox(height: 12));
    children.add(Row(
      children: [
        TextBtn(
          label: switch (_filterState) {
            'running' => 'Filtering',
            'done' => 'Filtered',
            _ => 'Filter',
          },
          onPressed: _runFilter,
        ),
        if (_filterState == 'done') ...[
          const SizedBox(width: 8),
          TextBtn(
              label: 'Export',
              kind: BtnKind.primary,
              onPressed: _openExportModal),
        ],
      ],
    ));
    if (_filterStatus.isNotEmpty) {
      children.add(Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: Muted(_filterStatus, color: const Color(0xFF9AF0C8)),
      ));
    }
    if (_filterState == 'done') {
      children.add(const SizedBox(height: 14));
      children.add(_statListPane(
          'FILTERED EMAIL TYPES', _filterAll.take(10).toList(), 'email',
          () => _openListModal('filter'),
          headerBottom: 4));
    }
    return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch, children: children);
  }

  Widget _statListPane(String title, List<StatEntry> items, String kind,
      VoidCallback onViewAll,
      {double headerBottom = 8}) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: EdgeInsets.only(bottom: headerBottom),
          child: Row(
            children: [
              Eyebrow(title),
              const Spacer(),
              TextBtn(label: 'View all', onPressed: onViewAll),
            ],
          ),
        ),
        ...items.map((x) => _StatRow(
            x: x, kind: kind, onSave: _saveOne)),
      ],
    );
  }

  Widget _newTab() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            TextBtn(
              label: 'Paste names',
              kind: _nameMode == 'paste' ? BtnKind.ghostOn : BtnKind.ghost,
              onPressed: () => setState(() => _nameMode = 'paste'),
            ),
            const SizedBox(width: 8),
            TextBtn(
              label: 'Load names file',
              kind: _nameMode == 'file' ? BtnKind.ghostOn : BtnKind.ghost,
              onPressed: () => setState(() => _nameMode = 'file'),
            ),
          ],
        ),
        const SizedBox(height: 10),
        if (_nameMode == 'paste')
          TextInput(
              hint: 'one name per line',
              maxLines: 4,
              controller: _users)
        else
          Row(children: [
            Expanded(
              child:              TextInput(
                  hint: 'names list file',
                  controller: _namePath),
            ),
            const SizedBox(width: 8),
            TextBtn(label: 'Browse', onPressed: () => _browse('editor-names')),
          ]),
        const SizedBox(height: 14),
        Text('EMAIL DOMAINS', style: labelStyle()),
        const SizedBox(height: 6),
        TextInput(controller: _doms),
        const SizedBox(height: 14),
        Row(
          children: [
            TextBtn(
              label: 'Paste passwords',
              kind: _passMode == 'paste' ? BtnKind.ghostOn : BtnKind.ghost,
              onPressed: () => setState(() => _passMode = 'paste'),
            ),
            const SizedBox(width: 8),
            TextBtn(
              label: 'Load password file',
              kind: _passMode == 'file' ? BtnKind.ghostOn : BtnKind.ghost,
              onPressed: () => setState(() => _passMode = 'file'),
            ),
          ],
        ),
        const SizedBox(height: 10),
        if (_passMode == 'paste')
          TextInput(
              hint: 'one per line', maxLines: 3, controller: _passes)
        else
          Row(children: [
            Expanded(
              child:              TextInput(
                  hint: 'password list file',
                  controller: _passPath),
            ),
            const SizedBox(width: 8),
            TextBtn(label: 'Browse', onPressed: () => _browse('editor-pass')),
          ]),
        const SizedBox(height: 12),
        Row(
          children: [
            SizedBox(
              width: 120,
              child: TextInput(controller: _count),
            ),
            const SizedBox(width: 8),
            TextBtn(
                label: 'Generate',
                kind: BtnKind.primary,
                onPressed: _generate),
          ],
        ),
        const SizedBox(height: 14),
        Text('GENERATED PREVIEW', style: labelStyle()),
        const SizedBox(height: 6),
        TextInput(maxLines: 7, mono: true, controller: _genOutCtrl, onChanged: (v) => _genOut = v),
        const SizedBox(height: 12),
        Row(
          children: [
            TextBtn(
                label: 'Export generated',
                kind: BtnKind.primary,
                onPressed: _openGenExportModal),
            const SizedBox(width: 8),
            TextBtn(label: 'Save', onPressed: _saveGenerated),
          ],
        ),
        if (_msg.isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 6),
            child: Muted(_msg, color: const Color(0xFF9AF0C8)),
          ),
      ],
    );
  }

  Widget _signTab() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (_msg.isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 6),
            child: Muted(_msg, color: const Color(0xFF9AF0C8)),
          ),
        Text('SAVED SIGNATURES', style: labelStyle()),
        const SizedBox(height: 6),
        Row(
          children: [
            Expanded(child: _sigSelect(_bannerSel, (v) {
              setState(() => _bannerSel = v);
            })),
            const SizedBox(width: 8),
            TextBtn(label: 'Load signature', onPressed: () {
              if (_bannerSel >= 0 && _bannerSel < _banners.length) {
                final b = _banners[_bannerSel];
                setState(() {
                  _bannerText.text = '${b['text'] ?? ''}';
                  _bannerName.text = '${b['name'] ?? ''}';
                });
              }
            }),
            const SizedBox(width: 8),
            TextBtn(label: 'Delete', onPressed: _deleteSig),
          ],
        ),
        const SizedBox(height: 14),
        Text('SIGNATURE TEXT', style: labelStyle()),
        const SizedBox(height: 6),
        TextInput(
            hint: '# captured by @solodolosouls',
            maxLines: 4,
            controller: _bannerText),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: TextInput(
                  hint: 'Name this signature', controller: _bannerName),
            ),
            const SizedBox(width: 8),
            TextBtn(
                label: 'Save signature',
                kind: BtnKind.primary,
                onPressed: _saveSig),
          ],
        ),
      ],
    );
  }

  Widget _sigSelect(int value, ValueChanged<int> onChange) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10),
      decoration: BoxDecoration(
        color: T.bgSoft,
        border: Border.all(color: T.line),
        borderRadius: BorderRadius.circular(10),
      ),
      child: DropdownButton<int>(
        value: value >= 0 && value < _banners.length ? value : null,
        isExpanded: true,
        underline: const SizedBox.shrink(),
        dropdownColor: T.card,
        style: const TextStyle(fontSize: 13, color: T.txt),
        items: [
          const DropdownMenuItem<int>(value: -1, child: Text('(none)')),
          ..._banners.asMap().entries.map((e) => DropdownMenuItem<int>(
              value: e.key, child: Text('${e.value['name'] ?? ''}'))),
        ],
        onChanged: (v) => onChange(v ?? -1),
      ),
    );
  }

}

class _TabBtn extends StatelessWidget {
  final String label;
  final bool on;
  final VoidCallback onTap;
  const _TabBtn(this.label, this.on, this.onTap);

  @override
  Widget build(BuildContext context) {
    return Material(
      color: Colors.transparent,
      child: InkWell(
        borderRadius: BorderRadius.circular(8),
        onTap: onTap,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 130),
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 7),
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
                        offset: Offset(0, 2)),
                  ]
                : null,
          ),
          child: Text(
            label,
            style: TextStyle(
              fontSize: 13,
              fontWeight: FontWeight.w600,
              color: on ? Colors.white : T.mut,
            ),
          ),
        ),
      ),
    );
  }
}

class _StatRow extends StatelessWidget {
  final StatEntry x;
  final String kind;
  final void Function(String kind, String name) onSave;
  const _StatRow({required this.x, required this.kind, required this.onSave});

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(top: 6),
      padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 9),
      decoration: BoxDecoration(
        color: T.card2,
        border: Border.all(color: T.lineSoft),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Row(
        children: [
          Expanded(
            child: Text(x.name,
                style: const TextStyle(
                    fontSize: 13, fontWeight: FontWeight.w700)),
          ),
          Text('${x.count}', style: const TextStyle(color: T.mut)),
          const SizedBox(width: 8),
          SizedBox(
            width: 32,
            height: 32,
            child: Material(
              color: const Color(0xFF1B2233),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(9),
                side: const BorderSide(color: T.lineSoft),
              ),
              child: InkWell(
                borderRadius: BorderRadius.circular(9),
                onTap: () => onSave(kind, x.name),
                child: const IconSave(size: 16),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
