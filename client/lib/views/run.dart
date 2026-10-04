// Dashboard: live run view (polls /api/state, stats, logs, hits) — port of
// dash/run.mjs, including the stick-to-bottom log behavior.
import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

import '../api.dart';
import '../theme.dart';
import '../widgets.dart';

class RunView extends StatefulWidget {
  final VoidCallback onPause;
  final Future<void> Function() onStop;
  final VoidCallback onHome;
  const RunView(
      {super.key,
      required this.onPause,
      required this.onStop,
      required this.onHome});

  @override
  State<RunView> createState() => _RunViewState();
}

class _RunViewState extends State<RunView> {
  Map<String, dynamic>? _snap;
  String _fp = '';
  Timer? _poll;
  final _logsCtrl = ScrollController();
  final _hitsCtrl = ScrollController();
  bool _stickLogs = true;
  bool _stickHits = true;
  // Locally accumulated log/hit lines. The server sends only the lines we
  // have not seen yet (log_from/hit_from), so each list is appended to
  // instead of being rebuilt from the full payload on every poll.
  final List<String> _logs = [];
  final List<String> _hits = [];
  int _logSeq = 0;
  int _hitSeq = 0;

  @override
  void initState() {
    super.initState();
    _tick();
    _poll = Timer.periodic(const Duration(milliseconds: 1200), (_) => _tick());
    _logsCtrl.addListener(() => _follow(_logsCtrl, (v) => _stickLogs = v));
    _hitsCtrl.addListener(() => _follow(_hitsCtrl, (v) => _stickHits = v));
  }

  void _follow(ScrollController c, void Function(bool) setStick) {
    if (!c.hasClients) return;
    final dist = c.position.maxScrollExtent - c.offset;
    setStick(dist < 28);
  }

  void _autoscroll(ScrollController c, bool stick) {
    if (!c.hasClients || !stick) return;
    SchedulerBinding.instance.addPostFrameCallback((_) {
      if (!c.hasClients) return;
      c.jumpTo(c.position.maxScrollExtent);
    });
  }

  @override
  void dispose() {
    _poll?.cancel();
    _logsCtrl.dispose();
    _hitsCtrl.dispose();
    super.dispose();
  }

  Future<void> _tick() async {
    final r =
        await Api.get('/api/state?log_from=$_logSeq&hit_from=$_hitSeq');
    if (!mounted || r.data is! Map) return;
    final data = Map<String, dynamic>.from(r.data);
    // Rebuild only when the snapshot actually changed: this poll used to
    // setState every 1.2s, re-rendering the whole log/hit panes (and
    // burning CPU) even when the engine sat idle.
    String fp;
    try {
      fp = jsonEncode(data);
    } catch (_) {
      fp = data.toString();
    }
    if (fp == _fp) return;
    _fp = fp;
    _ingest(data);
    final prev = _logsCtrl.hasClients ? _logsCtrl.offset : 0.0;
    setState(() => _snap = data);
    if (_stickLogs) {
      _autoscroll(_logsCtrl, true);
    } else if (_logsCtrl.hasClients && prev > 0) {
      // keep the user's position across the rebuild
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_logsCtrl.hasClients) _logsCtrl.jumpTo(prev);
      });
    }
    if (_stickHits) _autoscroll(_hitsCtrl, true);
  }

  // _ingest folds the server's delta (or a legacy full payload) into the
  // local lists and advances the sequence we will ask for next.
  void _ingest(Map<String, dynamic> data) {
    final lt = data['log_total'];
    if (lt is int) {
      if (data['log_reset'] == true) _logs.clear();
      final nl = data['new_logs'];
      if (nl is List) _logs.addAll(nl.map((e) => '$e'));
      _logSeq = lt;
    } else if (data['logs'] is List) {
      _logs
        ..clear()
        ..addAll((data['logs'] as List).map((e) => '$e'));
      _logSeq = _logs.length;
    }
    if (_logs.length > 200) _logs.removeRange(0, _logs.length - 200);

    final ht = data['hit_total'];
    if (ht is int) {
      if (data['hit_reset'] == true) _hits.clear();
      final nh = data['new_hits'];
      if (nh is List) _hits.addAll(nh.map((e) => '$e'));
      _hitSeq = ht;
    } else if (data['hits'] is List) {
      _hits
        ..clear()
        ..addAll((data['hits'] as List).map((e) => '$e'));
      _hitSeq = _hits.length;
    }
    if (_hits.length > 200) _hits.removeRange(0, _hits.length - 200);
  }

  @override
  Widget build(BuildContext context) {
    final s = _snap ?? const <String, dynamic>{};
    final cpm = (s['rate_cpm'] is num) ? (s['rate_cpm'] as num).toDouble() : 0.0;
    // Show the last 80 log lines (same window the server keeps), appended
    // from the local list instead of re-slicing a fresh payload each tick.
    final logs = _logs.length > 80 ? _logs.sublist(_logs.length - 80) : List<String>.from(_logs);
    final hits = List<String>.from(_hits);

    final tiles = <(String, String)>[
      ('Checked', '${s['checked'] ?? 0} / ${s['total'] ?? 0}'),
      ('Running', '${s['elapsed'] ?? '0s'}'),
      ('ETA', '${s['eta'] ?? '—'}'),
      ('Speed', cpm > 0
          ? (cpm >= 10
              ? '${cpm.round()}/min'
              : '${cpm.toStringAsFixed(1)}/min')
          : '—'),
      ('Daily', '${s['daily'] ?? 0} / ${s['limit'] ?? 0}'),
    ];

    final paused = s['paused'] == true;
    final running = s['running'] == true;
    // A run that is no longer active has finished (Done/Stopped status from
    // the engine): the Pause/Stop controls are meaningless then, so only the
    // status pill and the tiles remain.
    final active = running || paused;
    final statusText = '${s['status'] ?? 'Ready'}';
    final finished = !active &&
        (statusText.startsWith('Done') || statusText.startsWith('Stopped'));

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            TagW(
              kind: paused
                  ? 'warn'
                  : running
                      ? 'yellow'
                      : finished
                          ? 'green'
                          : '',
              child: Text(statusText,
                  style: const TextStyle(
                      fontSize: 11, fontWeight: FontWeight.w600)),
            ),
            const Spacer(),
            if (active) ...[
              TextBtn(
                label: paused ? 'Resume checking' : 'Pause',
                kind: BtnKind.primary,
                onPressed: widget.onPause,
              ),
              const SizedBox(width: 8),
              TextBtn(label: 'Stop', onPressed: () => widget.onStop()),
              const SizedBox(width: 8),
            ],
            TextBtn(
                label: 'Home',
                icon: const IconBack(size: 14),
                onPressed: widget.onHome),
          ],
        ),
        const SizedBox(height: 16),
        LayoutBuilder(builder: (context, box) {
          final cols = box.maxWidth < 900
              ? 2
              : box.maxWidth < 1300
                  ? 3
                  : 5;
          return GridView.count(
            crossAxisCount: cols,
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            crossAxisSpacing: 10,
            mainAxisSpacing: 10,
            childAspectRatio: 3.0,
            children: tiles
                .map((t) => Container(
                      padding: const EdgeInsets.fromLTRB(13, 11, 13, 11),
                      decoration: BoxDecoration(
                        color: T.card2,
                        border: Border.all(color: T.line),
                        borderRadius: BorderRadius.circular(12),
                      ),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          Text(t.$1.toUpperCase(),
                              style: const TextStyle(
                                  fontSize: 10.5,
                                  fontWeight: FontWeight.w700,
                                  letterSpacing: 1.05,
                                  color: T.mut)),
                          const SizedBox(height: 4),
                          Text(t.$2,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(
                                  fontSize: 16, fontWeight: FontWeight.w700)),
                        ],
                      ),
                    ))
                .toList(),
          );
        }),
        const SizedBox(height: 12),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            TagW(
                kind: 'green',
                child: _TagCount(label: 'Valid', n: s['valid'] ?? 0)),
            TagW(
                kind: 'green',
                child: _TagCount(label: 'Free', n: s['free'] ?? 0)),
            TagW(
                kind: 'blue',
                child: _TagCount(label: 'Private', n: s['private'] ?? 0)),
            TagW(
                kind: 'warn',
                child: _TagCount(label: 'Invalid', n: s['invalid'] ?? 0)),
            TagW(
                kind: 'red',
                child: _TagCount(label: 'Retries', n: s['retries'] ?? 0)),
          ],
        ),
        const SizedBox(height: 16),
        LayoutBuilder(builder: (context, box) {
          final split = box.maxWidth >= 1100;
          final activity = _LogPane(
            eyebrow: 'Activity',
            hint: '${logs.length} lines',
            controller: _logsCtrl,
            lines: logs,
            colorOf: (l) {
              // Green = hit, amber = retry/skip notes (not results),
              // red = failures. A wall of red retry lines would look the
              // same as real failures and hides what is actually going on.
              if (RegExp(r'\[HIT\]|\[Free Hit\]|\[Private Hit\]',
                      caseSensitive: false)
                  .hasMatch(l)) {
                return const Color(0xFF9AF0C8);
              }
              if (RegExp(r'^\[RETRY\]|^\[SKIP\]|^\[INFO\]',
                      caseSensitive: false)
                  .hasMatch(l)) {
                return const Color(0xFFFFD48A);
              }
              return const Color(0xFFF5A8A8);
            },
          );
          final hitsPane = _LogPane(
            eyebrow: 'Hits',
            hint: '${hits.length} lines',
            controller: _hitsCtrl,
            lines: hits,
            colorOf: (_) => const Color(0xFFA7F3D0),
            joinWith: '\n',
          );
          if (!split) {
            return Column(children: [
              activity,
              const SizedBox(height: 14),
              hitsPane,
            ]);
          }
          return Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(child: activity),
              const SizedBox(width: 14),
              Expanded(child: hitsPane),
            ],
          );
        }),
      ],
    );
  }
}

class _TagCount extends StatelessWidget {
  final String label;
  final dynamic n;
  const _TagCount({required this.label, required this.n});
  @override
  Widget build(BuildContext context) => Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text('$label ',
              style: const TextStyle(
                  fontSize: 11, fontWeight: FontWeight.w600)),
          Text('$n',
              style: const TextStyle(
                  fontSize: 11, fontWeight: FontWeight.w700)),
        ],
      );
}

class _LogPane extends StatelessWidget {
  final String eyebrow;
  final String hint;
  final ScrollController controller;
  final List<String> lines;
  final Color Function(String) colorOf;
  final String joinWith;
  const _LogPane({
    required this.eyebrow,
    required this.hint,
    required this.controller,
    required this.lines,
    required this.colorOf,
    this.joinWith = '\n',
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      constraints: const BoxConstraints(minHeight: 170),
      decoration: BoxDecoration(
        color: T.card2,
        border: Border.all(color: T.line),
        borderRadius: BorderRadius.circular(12),
        boxShadow: const [
          BoxShadow(
              color: Color(0x38000000), blurRadius: 16, offset: Offset(0, 4)),
        ],
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Container(
            padding: const EdgeInsets.fromLTRB(13, 9, 13, 9),
            decoration: const BoxDecoration(
              border: Border(bottom: BorderSide(color: T.lineSoft)),
              color: Color(0x06FFFFFF),
            ),
            child: Row(
              children: [
                Eyebrow(eyebrow),
                const Spacer(),
                Muted(hint),
              ],
            ),
          ),
          Container(
            height: 300,
            color: T.bgSoft,
            padding: const EdgeInsets.fromLTRB(13, 11, 6, 11),
            child: lines.isEmpty
                ? const Muted('—')
                : ListView.builder(
                    controller: controller,
                    itemCount: lines.length,
                    itemBuilder: (context, i) => Text(
                      lines[i],
                      style: TextStyle(
                        fontSize: 12.5,
                        height: 1.5,
                        color: colorOf(lines[i]),
                        fontFamily: 'Consolas',
                      ),
                    ),
                  ),
          ),
        ],
      ),
    );
  }
}
