// Dashboard: home menu and session resume views (port of dash/home.mjs).
import 'dart:math';

import 'package:flutter/material.dart';

import '../api.dart';
import '../theme.dart';
import '../widgets.dart';

class HomeView extends StatelessWidget {
  final bool live;
  final String uid;
  final VoidCallback onNew;
  final VoidCallback onResume;
  final VoidCallback onEditor;
  final VoidCallback onOpenViewer;
  final VoidCallback onLive;
  const HomeView({
    super.key,
    required this.live,
    required this.uid,
    required this.onNew,
    required this.onResume,
    required this.onEditor,
    required this.onOpenViewer,
    required this.onLive,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // ---- Workspace section
        SectionHead(
          eyebrow: 'Workspace',
          title: 'Start a session',
          sub: 'Pick a list, tune the run, watch it work.',
        ),
        LayoutBuilder(builder: (context, box) {
          final cols = box.maxWidth < 700 ? 1 : 3;
          return GridView.count(
            crossAxisCount: cols,
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            crossAxisSpacing: 12,
            mainAxisSpacing: 12,
            // Sized to the card's content (~150px) so the cards stop
            // carrying a large empty bottom half.
            childAspectRatio: cols == 1 ? 3.4 : 2.2,
            children: [
              ChoiceCard(
                icon: const IcoBox(child: IconNew(size: 18)),
                title: 'Start new',
                desc: 'New combo list, proxies, keywords and workers.',
                onTap: onNew,
              ),
              ChoiceCard(
                icon: const IcoBox(child: IconResume(size: 18)),
                title: 'Resume session',
                desc:
                    'Pick a saved session and continue unfinished lines + retries.',
                onTap: onResume,
              ),
              ChoiceCard(
                icon: const IcoBox(child: IconEditor(size: 18)),
                title: 'Editor',
                desc: 'Build, clean, extract and export combo lists.',
                onTap: onEditor,
              ),
            ],
          );
        }),
        const SizedBox(height: 16),
        Container(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Row(
            children: [
              TextBtn(
                label: 'View valid',
                kind: BtnKind.primary,
                icon: const IconList(size: 14),
                onPressed: onOpenViewer,
              ),
              if (live) ...[
                const SizedBox(width: 8),
                TextBtn(
                  label: 'Run monitor',
                  kind: BtnKind.primary,
                  onPressed: onLive,
                ),
              ],
              const Spacer(),
              Flexible(
                child: Muted(
                    'Results land in valid.txt — open the viewer to browse them.'),
              ),
            ],
          ),
        ),
        const DividerSoft(top: 22, bottom: 20),

        // ---- Community section
        SectionHead(eyebrow: 'Community', title: 'Stay in the loop'),
        LayoutBuilder(builder: (context, box) {
          final cols = box.maxWidth < 700 ? 1 : 2;
          return GridView.count(
            crossAxisCount: cols,
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            crossAxisSpacing: 12,
            mainAxisSpacing: 12,
            childAspectRatio: cols == 1 ? 3.4 : 3.2,
            children: [
              ChoiceCard(
                icon: const IcoBox(tg: true, child: TgMark(size: 42, gid: 'tgA')),
                title: 'Tools & Updates',
                desc: 'Go here for tools, updates, and new releases.',
                onTap: () => openLink('https://t.me/+naqymNMpt75jYjdh'),
              ),
              ChoiceCard(
                icon: const IcoBox(tg: true, child: TgMark(size: 42, gid: 'tgB')),
                title: 'Chat',
                desc: 'Go here to chat with everyone in the community.',
                onTap: () => openLink('https://t.me/+cueTzd36nb9jNWQx'),
              ),
            ],
          );
        }),
      ],
    );
  }
}

/* ---------------- resume ---------------------------------------------------- */

class ResumeView extends StatefulWidget {
  final VoidCallback onBack;
  final ValueChanged<Map<String, dynamic>?> onPick;
  const ResumeView({super.key, required this.onBack, required this.onPick});

  @override
  State<ResumeView> createState() => _ResumeViewState();
}

class _ResumeViewState extends State<ResumeView> {
  List<Map<String, dynamic>>? _sessions;
  int _page = 0;
  String _picked = '';
  static const _perPage = 5;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final r = await Api.get('/api/sessions');
    if (!mounted) return;
    final list = <Map<String, dynamic>>[];
    if (r.data is Map && r.data['sessions'] is List) {
      for (final s in r.data['sessions']) {
        if (s is Map) list.add(Map<String, dynamic>.from(s));
      }
    }
    setState(() {
      _sessions = list;
      if (list.isNotEmpty) _picked = (list.first['file'] ?? '') as String;
    });
  }

  @override
  Widget build(BuildContext context) {
    final list = _sessions ?? const <Map<String, dynamic>>[];
    final pages = max(1, (list.length / _perPage).ceil());
    final clamped = min(max(_page, 0), pages - 1);
    final start = clamped * _perPage;
    final slice = list.sublist(start, min(start + _perPage, list.length));
    Map<String, dynamic>? selected;
    for (final x in list) {
      if ((x['file'] ?? '') == _picked) selected = x;
    }
    selected ??= list.isEmpty ? null : list.first;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SectionHead(
          eyebrow: 'Saved sessions',
          title: 'Resume where you left off',
          sub: 'Unfinished lines and retries are kept per session.',
        ),
        if (_sessions == null)
          const Muted('Loading sessions...'),
        if (_sessions != null && list.isEmpty)
          const Muted('No saved sessions'),
        ...slice.map((x) {
          final on = _picked == (x['file'] ?? '');
          return _SessCard(
            x: x,
            on: on,
            onTap: () => setState(() => _picked = (x['file'] ?? '') as String),
          );
        }),
        const SizedBox(height: 12),
        Row(
          children: [
            TextBtn(
                label: 'Prev',
                disabled: clamped <= 0,
                onPressed: () => setState(() => _page = clamped - 1)),
            const SizedBox(width: 10),
            Expanded(
              child: Muted(list.isNotEmpty
                  ? 'Page ${clamped + 1} / $pages'
                  : ''),
            ),
            TextBtn(
                label: 'Next',
                disabled: clamped >= pages - 1,
                onPressed: () => setState(() => _page = clamped + 1)),
          ],
        ),
        const SizedBox(height: 14),
        Row(
          children: [
            TextBtn(
                label: 'Back',
                icon: const IconBack(size: 14),
                onPressed: widget.onBack),
            const Spacer(),
            TextBtn(
              label: 'Resume selected',
              kind: BtnKind.primary,
              icon: const IconNext(size: 14),
              onPressed: selected == null
                  ? null
                  : () => widget.onPick(selected),
            ),
          ],
        ),
      ],
    );
  }
}

class _SessCard extends StatelessWidget {
  final Map<String, dynamic> x;
  final bool on;
  final VoidCallback onTap;
  const _SessCard({required this.x, required this.on, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: onTap,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 130),
          padding: const EdgeInsets.fromLTRB(14, 13, 14, 13),
          decoration: BoxDecoration(
            color: on ? const Color(0x0F22C55E) : T.card2,
            border: Border.all(color: on ? T.grn : T.line),
            borderRadius: BorderRadius.circular(12),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      (x['combo'] ?? '') as String,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                          fontSize: 13.5, fontWeight: FontWeight.w700),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 3),
              Muted(
                  'started ${x['started'] ?? '-'} · updated ${x['updated'] ?? '-'}'),
              const SizedBox(height: 9),
              Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  Tag(
                      kind: 'green',
                      text: 'workers ${x['workers'] ?? 0}'),
                  Tag(
                      kind: 'warn',
                      text: 'retries ${x['retries'] ?? 0}'),
                  if ((x['proxy'] ?? '') as String != '')
                    Tag(text: x['proxy'] as String),
                  if ((x['keywords'] ?? '') as String != '')
                    const Tag(text: 'keywords'),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}
