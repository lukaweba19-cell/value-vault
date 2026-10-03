// Dashboard: setup (run configuration) view — port of dash/setup.mjs.
import 'package:flutter/material.dart';

import '../api.dart';
import '../shell.dart' show RunForm;
import '../theme.dart';
import '../widgets.dart';

class SetupView extends StatefulWidget {
  final RunForm form;
  final String proxyKind;
  final ValueChanged<String> setProxyKind;
  final VoidCallback onBack;
  final VoidCallback onStart;
  const SetupView({
    super.key,
    required this.form,
    required this.proxyKind,
    required this.setProxyKind,
    required this.onBack,
    required this.onStart,
  });

  @override
  State<SetupView> createState() => _SetupViewState();
}

class _SetupViewState extends State<SetupView> {
  List<Map<String, dynamic>> _saved = [];
  final _saveName = TextEditingController();

  // Persistent controllers: the old code built a fresh controller from the
  // form value on every rebuild, so the 1.2s shell poll kept resetting the
  // cursor mid-typing (typing/deleting workers was impossible).
  late final TextEditingController _comboCtrl;
  late final TextEditingController _kwCtrl;
  late final TextEditingController _workersCtrl;
  late final TextEditingController _proxyCtrl;
  final List<TextEditingController> _urlCtrls = [];

  RunForm get form => widget.form;

  @override
  void initState() {
    super.initState();
    _comboCtrl = TextEditingController(text: form.combo);
    _kwCtrl = TextEditingController(text: form.kw);
    _workersCtrl = TextEditingController(text: form.workers);
    _proxyCtrl = TextEditingController(text: form.proxy);
    _syncUrlCtrls();
    _loadSaved();
  }

  @override
  void dispose() {
    _saveName.dispose();
    _comboCtrl.dispose();
    _kwCtrl.dispose();
    _workersCtrl.dispose();
    _proxyCtrl.dispose();
    for (final c in _urlCtrls) {
      c.dispose();
    }
    super.dispose();
  }

  /// Keeps one controller per server-proxy row, in sync with form.proxyUrls.
  /// Call only on structural/programmatic changes (add/remove row, saved
  /// selection) — never during typing.
  void _syncUrlCtrls() {
    while (_urlCtrls.length > form.proxyUrls.length) {
      _urlCtrls.removeLast().dispose();
    }
    while (_urlCtrls.length < form.proxyUrls.length) {
      _urlCtrls.add(TextEditingController(text: form.proxyUrls[_urlCtrls.length]));
    }
    for (var i = 0; i < _urlCtrls.length; i++) {
      if (_urlCtrls[i].text != form.proxyUrls[i]) {
        _urlCtrls[i].text = form.proxyUrls[i];
      }
    }
  }

  Future<void> _openSavedPicker(String kind) async {
    await _loadSaved();
    if (!mounted) return;
    await showAppModal<void>(context, _savedPicker(kind));
  }

  Future<void> _loadSaved() async {
    final r = await Api.get('/api/proxy-lists');
    final out = <Map<String, dynamic>>[];
    if (r.data is List) {
      for (final x in r.data) {
        if (x is Map) out.add(Map<String, dynamic>.from(x));
      }
    }
    if (mounted) setState(() => _saved = out);
  }

  List<Map<String, dynamic>> _rowsFor(String kind) => _saved
      .where((x) =>
          kind == 'server' ? x['type'] == 'server' : x['type'] != 'server')
      .toList();

  Future<void> _useSaved(Map<String, dynamic> x) async {
    if (widget.proxyKind == 'server') {
      final urls = <String>[];
      if (x['urls'] is List && (x['urls'] as List).isNotEmpty) {
        for (final u in x['urls'] as List) {
          urls.add('$u');
        }
      } else {
        urls.add('');
      }
      setState(() => form.proxyUrls = urls);
      _syncUrlCtrls();
    } else {
      setState(() {
        form.proxy = (x['path'] ?? '') as String;
        form.proxyFileCount = '0 proxies';
        _proxyCtrl.text = form.proxy;
      });
    }
  }

  Future<void> _delSaved(Map<String, dynamic> x) async {
    await Api.delete(
        '/api/proxy-lists?name=${Uri.encodeComponent((x['name'] ?? '') as String)}'
        '&path=${Uri.encodeComponent((x['path'] ?? '') as String)}');
    _loadSaved();
  }

  Future<void> _browse(String kind) async {
    final r = await Api.get('/api/pick?kind=${Uri.encodeComponent(kind)}');
    if (r.data is Map && r.data['path'] != null) {
      setState(() {
        if (kind == 'proxy') {
          form.proxy = r.data['path'] as String;
          _proxyCtrl.text = form.proxy;
        } else {
          form.combo = r.data['path'] as String;
          _comboCtrl.text = form.combo;
        }
      });
    }
  }

  Future<void> _testFile() async {
    setState(() => form.proxyFileCount = 'checking…');
    final r = await Api.post('/api/proxy-fetch', {'path': form.proxy});
    setState(() => form.proxyFileCount = r.ok
        ? '${(r.data is Map ? r.data['count'] : 0) ?? 0} proxies'
        : 'failed');
  }

  Future<void> _testServer() async {
    setState(() => form.proxyCount = 'checking…');
    final urls = form.proxyUrls.map((s) => s.trim()).where((s) => s.isNotEmpty).toList();
    final r = await Api.post('/api/proxy-fetch', {'urls': urls});
    setState(() => form.proxyCount = r.ok
        ? '${(r.data is Map ? r.data['count'] : 0) ?? 0} proxies'
        : 'failed');
  }

  Future<void> _remember(bool checked) async {
    if (!checked) return;
    if (form.proxy.trim().isEmpty) {
      setState(() => form.rememberProxy = false);
      return;
    }
    await Api.post('/api/proxy-lists', {
      'path': form.proxy,
      'name': fileBase(form.proxy),
    });
    _loadSaved();
  }

  /// Opens the save-server form as a compact centered modal. It used to
  /// render inline at the bottom of the form — a huge box under the Start
  /// button where the fields sat far below the action that opened it.
  Future<void> _openSaveDialog() async {
    _saveName.clear();
    if (!mounted) return;
    await showAppModal<void>(context, _saveServerBody());
  }

  Future<void> _saveServer() async {
    final urls =
        form.proxyUrls.map((s) => s.trim()).where((s) => s.isNotEmpty).toList();
    final name = _saveName.text.trim();
    if (urls.isEmpty || name.isEmpty) return;
    await Api.post('/api/proxy-lists', {
      'type': 'server',
      'name': name,
      'urls': urls,
    });
    if (mounted) Navigator.of(context).pop();
    _saveName.clear();
    _loadSaved();
  }

  @override
  Widget build(BuildContext context) {
    const caps = [
      ['capKw', 'Keywords'],
      ['capPay', 'Payments'],
      ['capPromo', 'Promos'],
      ['capFn', 'Fortnite'],
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SectionHead(
          eyebrow: 'New session',
          title: 'Run configuration',
          sub: 'Combo list, proxies, keywords and workers.',
        ),

        // ---- Combos
        Field(
          label: 'Combos',
          child: Row(
            children: [
              Expanded(
                child: TextInput(
                  hint: 'combo file',
                  controller: _comboCtrl,
                  onChanged: (v) => form.combo = v,
                ),
              ),
              const SizedBox(width: 8),
              TextBtn(label: 'Browse', onPressed: () => _browse('combo')),
            ],
          ),
        ),

        const DividerSoft(top: 22, bottom: 18),

        // ---- Proxies
        Eyebrow('Proxies'),
        const SizedBox(height: 10),
        Seg(
          options: const ['File', 'Server'],
          value: widget.proxyKind == 'server' ? 'Server' : 'File',
          onChange: (v) =>
              widget.setProxyKind(v == 'Server' ? 'server' : 'file'),
        ),
        const SizedBox(height: 12),
        if (widget.proxyKind == 'file') _fileProxy() else _serverProxy(),

        const DividerSoft(top: 22, bottom: 18),

        // ---- Keywords + workers
        LayoutBuilder(builder: (context, box) {
          final half = box.maxWidth < 640;
          final kw = Field(
            label: 'Keywords',
            child: TextInput(
              controller: _kwCtrl,
              onChanged: (v) => form.kw = v,
            ),
          );
          final wk = Field(
            label: 'Workers',
            child: TextInput(
              controller: _workersCtrl,
              onChanged: (v) => form.workers = v,
            ),
          );
          if (half) {
            return Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  kw,
                  const SizedBox(height: 16),
                  wk,
                ]);
          }
          return Row(
            children: [
              Expanded(child: kw),
              const SizedBox(width: 20),
              Expanded(child: wk),
            ],
          );
        }),

        const DividerSoft(top: 22, bottom: 18),

        // ---- Captures
        Eyebrow('Captures'),
        const SizedBox(height: 10),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: caps.map((c) {
            final key = c[0];
            final val = switch (key) {
              'capKw' => form.capKw,
              'capPay' => form.capPay,
              'capPromo' => form.capPromo,
              _ => form.capFn,
            };
            return _CheckChip(
              label: c[1],
              value: val,
              onChanged: (v) => setState(() {
                switch (key) {
                  case 'capKw':
                    form.capKw = v;
                  case 'capPay':
                    form.capPay = v;
                  case 'capPromo':
                    form.capPromo = v;
                  default:
                    form.capFn = v;
                }
              }),
            );
          }).toList(),
        ),

        if (form.err.isNotEmpty) ...[
          const SizedBox(height: 12),
          Muted(form.err, color: T.red),
        ],

        const SizedBox(height: 20),
        Row(
          children: [
            TextBtn(
                label: 'Back',
                icon: const IconBack(size: 14),
                onPressed: widget.onBack),
            const Spacer(),
            TextBtn(
              label: 'Start new',
              kind: BtnKind.primary,
              icon: const IconPlay(size: 14),
              onPressed: widget.onStart,
            ),
          ],
        ),
      ],
    );
  }

  Widget _fileProxy() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Expanded(
              child: TextInput(
                hint: 'proxy file',
                controller: _proxyCtrl,
                onChanged: (v) => form.proxy = v,
              ),
            ),
            const SizedBox(width: 8),
            TextBtn(label: 'Browse', onPressed: () => _browse('proxy')),
          ],
        ),
        const SizedBox(height: 10),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                SizedBox(
                  width: 16,
                  height: 16,
                  child: Checkbox(
                    value: form.rememberProxy,
                    onChanged: (v) {
                      setState(() => form.rememberProxy = v ?? false);
                      _remember(v ?? false);
                    },
                    materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    visualDensity: VisualDensity.compact,
                  ),
                ),
                const SizedBox(width: 7),
                const Text('Remember',
                    style: TextStyle(fontSize: 12.5, fontWeight: FontWeight.w600)),
              ],
            ),
            TextBtn(label: 'Check', onPressed: _testFile),
            Tag(text: form.proxyFileCount ?? '0 proxies'),
            // saved lists picker (modal — a dropdown here was painted over
            // by the sections below it, so rows could never be clicked)
            TextBtn(label: 'Saved lists', onPressed: () => _openSavedPicker('file')),
          ],
        ),
      ],
    );
  }

  Widget _serverProxy() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < form.proxyUrls.length; i++)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: TextInput(
              hint: 'https://example.com/proxies.txt',
              controller: _urlCtrls[i],
              onChanged: (v) => form.proxyUrls[i] = v,
            ),
          ),
        const SizedBox(height: 2),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TextBtn(
                label: '+',
                onPressed: () => setState(() {
                  form.proxyUrls.add('');
                  _syncUrlCtrls();
                })),
            if (form.proxyUrls.length > 1)
              TextBtn(
                  label: '–',
                  onPressed: () => setState(() {
                    form.proxyUrls.removeLast();
                    _syncUrlCtrls();
                  })),
            TextBtn(label: 'Save list', onPressed: _openSaveDialog),
            TextBtn(label: 'Check', onPressed: _testServer),
            Tag(text: form.proxyCount ?? '0 proxies'),
            TextBtn(
                label: 'Saved servers',
                onPressed: () => _openSavedPicker('server')),
          ],
        ),
      ],
    );
  }

  Widget _savedPicker(String kind) {
    final rows = _rowsFor(kind);
    // No card of its own: showAppModal already draws the panel (one box,
    // one border — nesting a second card here looked broken).
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  kind == 'server' ? 'Saved servers' : 'Saved lists',
                  style: const TextStyle(
                      fontSize: 15, fontWeight: FontWeight.w700),
                ),
              ),
              TextBtn(
                label: 'Close',
                kind: BtnKind.secondary,
                padding: const EdgeInsets.symmetric(
                    horizontal: 12, vertical: 6),
                onPressed: () => Navigator.of(context).pop(),
              ),
            ],
          ),
          const SizedBox(height: 12),
          if (rows.isEmpty)
            const Padding(
              padding: EdgeInsets.symmetric(horizontal: 4, vertical: 8),
              child: Muted('Nothing saved'),
            ),
          ...rows.map((x) {
            final detail = kind == 'server'
                ? '${(x['urls'] as List?)?.length ?? 0} urls · ${x['count'] ?? 0} proxies'
                : '${x['count'] ?? 0} proxies · ${x['path'] ?? ''}';
            return InkWell(
              borderRadius: BorderRadius.circular(8),
              onTap: () {
                _useSaved(x);
                Navigator.of(context).pop();
              },
              child: Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
                child: Row(
                  children: [
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text((x['name'] ?? 'list') as String,
                              style: const TextStyle(
                                  fontSize: 13,
                                  fontWeight: FontWeight.w700)),
                          const SizedBox(height: 3),
                          Tag(text: detail),
                        ],
                      ),
                    ),
                    const SizedBox(width: 8),
                    TextBtn(
                        label: '✕',
                        padding: const EdgeInsets.symmetric(
                            horizontal: 9, vertical: 6),
                        onPressed: () => _delSaved(x)),
                  ],
                ),
              ),
            );
          }),
      ],
    );
  }

  /// Compact modal body: title, one NAME field, Save/Cancel. No card of its
  /// own — showAppModal already draws the panel (same pattern as _savedPicker).
  Widget _saveServerBody() {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            const Expanded(
              child: Text(
                'Save server list',
                style: TextStyle(fontSize: 15, fontWeight: FontWeight.w700),
              ),
            ),
            TextBtn(
              label: 'Close',
              kind: BtnKind.secondary,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              onPressed: () => Navigator.of(context).pop(),
            ),
          ],
        ),
        const SizedBox(height: 14),
        Text('NAME', style: labelStyle()),
        const SizedBox(height: 6),
        TextInput(
          hint: 'proxy-labs https',
          controller: _saveName,
          onSubmitted: _saveServer,
        ),
        const SizedBox(height: 14),
        Row(
          children: [
            TextBtn(
                label: 'Save',
                kind: BtnKind.primary,
                onPressed: _saveServer),
            const SizedBox(width: 8),
            TextBtn(
                label: 'Cancel',
                onPressed: () => Navigator.of(context).pop()),
          ],
        ),
      ],
    );
  }
}

class _CheckChip extends StatelessWidget {
  final String label;
  final bool value;
  final ValueChanged<bool> onChanged;
  const _CheckChip(
      {required this.label, required this.value, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(10),
      onTap: () => onChanged(!value),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: T.card2,
          border: Border.all(color: value ? T.acc : T.line),
          borderRadius: BorderRadius.circular(10),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            SizedBox(
              width: 16,
              height: 16,
              child: Checkbox(
                value: value,
                onChanged: (v) => onChanged(v ?? false),
                materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                visualDensity: VisualDensity.compact,
              ),
            ),
            const SizedBox(width: 8),
            Text(label,
                style: TextStyle(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    color: value ? T.txt : T.mut)),
          ],
        ),
      ),
    );
  }
}
