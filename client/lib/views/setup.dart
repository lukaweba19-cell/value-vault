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
  bool _proxyMenu = false;
  List<Map<String, dynamic>> _saved = [];
  bool _saveOpen = false;
  final _saveName = TextEditingController();

  RunForm get form => widget.form;

  @override
  void initState() {
    super.initState();
    _loadSaved();
  }

  @override
  void dispose() {
    _saveName.dispose();
    super.dispose();
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
    } else {
      setState(() {
        form.proxy = (x['path'] ?? '') as String;
        form.proxyFileCount = '0 proxies';
      });
    }
    setState(() => _proxyMenu = false);
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
        } else {
          form.combo = r.data['path'] as String;
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
    setState(() => _saveOpen = false);
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
                  controller: TextEditingController.fromValue(
                      TextEditingValue(text: form.combo)),
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
              controller: TextEditingController.fromValue(
                  TextEditingValue(text: form.kw)),
              onChanged: (v) => form.kw = v,
            ),
          );
          final wk = Field(
            label: 'Workers',
            child: TextInput(
              controller: TextEditingController.fromValue(
                  TextEditingValue(text: form.workers)),
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

        if (_saveOpen) _saveServerDialog(),
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
                controller: TextEditingController.fromValue(
                    TextEditingValue(text: form.proxy)),
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
            // saved lists dropdown
            Stack(
              children: [
                TextBtn(
                  label: 'Saved lists',
                  onPressed: () {
                    setState(() => _proxyMenu = !_proxyMenu);
                    _loadSaved();
                  },
                ),
                if (_proxyMenu)
                  Positioned(
                    top: 42,
                    left: 0,
                    child: _savedMenu('file'),
                  ),
              ],
            ),
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
              controller: TextEditingController.fromValue(
                  TextEditingValue(text: form.proxyUrls[i])),
              onChanged: (v) => setState(() => form.proxyUrls[i] = v),
            ),
          ),
        const SizedBox(height: 2),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TextBtn(
                label: '+',
                onPressed: () =>
                    setState(() => form.proxyUrls.add(''))),
            if (form.proxyUrls.length > 1)
              TextBtn(
                  label: '–',
                  onPressed: () => setState(() => form.proxyUrls.removeLast())),
            TextBtn(
                label: 'Save list',
                onPressed: () => setState(() => _saveOpen = true)),
            TextBtn(label: 'Check', onPressed: _testServer),
            Tag(text: form.proxyCount ?? '0 proxies'),
            Stack(
              children: [
                TextBtn(
                  label: 'Saved servers',
                  onPressed: () {
                    setState(() => _proxyMenu = !_proxyMenu);
                    _loadSaved();
                  },
                ),
                if (_proxyMenu)
                  Positioned(
                    top: 42,
                    left: 0,
                    child: _savedMenu('server'),
                  ),
              ],
            ),
          ],
        ),
      ],
    );
  }

  Widget _savedMenu(String kind) {
    final rows = _rowsFor(kind);
    return Container(
      width: 340,
      constraints: const BoxConstraints(maxHeight: 260),
      padding: const EdgeInsets.all(7),
      decoration: BoxDecoration(
        color: T.card,
        border: Border.all(color: T.line),
        borderRadius: BorderRadius.circular(12),
        boxShadow: const [
          BoxShadow(color: Color(0x80000000), blurRadius: 40, offset: Offset(0, 16)),
        ],
      ),
      child: SingleChildScrollView(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (rows.isEmpty)
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 10, vertical: 8),
                child: Muted('Nothing saved'),
              ),
            ...rows.map((x) {
              final detail = kind == 'server'
                  ? '${(x['urls'] as List?)?.length ?? 0} urls · ${x['count'] ?? 0} proxies'
                  : '${x['count'] ?? 0} proxies · ${x['path'] ?? ''}';
              return InkWell(
                borderRadius: BorderRadius.circular(8),
                onTap: () => _useSaved(x),
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
        ),
      ),
    );
  }

  Widget _saveServerDialog() {
    return Container(
      margin: const EdgeInsets.only(top: 18),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: T.card,
        border: Border.all(color: T.line),
        borderRadius: BorderRadius.circular(16),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Eyebrow('Save server list'),
          const SizedBox(height: 12),
          Text('NAME', style: labelStyle()),
          const SizedBox(height: 6),
          TextInput(
            hint: 'proxy-labs https',
            controller: _saveName,
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              TextBtn(
                  label: 'Save',
                  kind: BtnKind.primary,
                  onPressed: _saveServer),
              const SizedBox(width: 8),
              TextBtn(
                  label: 'Cancel',
                  onPressed: () => setState(() => _saveOpen = false)),
            ],
          ),
        ],
      ),
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
