// App shell: topbar, sidebar navigation, statusbar, view state machine and
// the update prompt — the Flutter port of dash/main.mjs.
import 'dart:async';
import 'dart:io' show exit;

import 'package:flutter/material.dart';

import 'api.dart';
import 'theme.dart';
import 'views/editor.dart';
import 'views/home.dart';
import 'views/run.dart';
import 'views/setup.dart';
import 'views/viewer.dart';
import 'widgets.dart';

/// Shared run-configuration form (mirrors emptyForm() in main.mjs).
class RunForm {
  String combo = '';
  String proxy = '';
  List<String> proxyUrls = [''];
  String kw = '';
  String workers = '20';
  bool rememberProxy = false;
  String? proxyFileCount;
  String? proxyCount;
  String err = '';
  bool capKw = true;
  bool capPay = true;
  bool capPromo = true;
  bool capFn = true;

  void reset() {
    combo = '';
    proxy = '';
    proxyUrls = [''];
    kw = '';
    workers = '20';
    rememberProxy = false;
    proxyFileCount = null;
    proxyCount = null;
    err = '';
    capKw = true;
    capPay = true;
    capPromo = true;
    capFn = true;
  }
}

typedef ViewSet = void Function(String view);

class AppShell extends StatefulWidget {
  const AppShell({super.key});

  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> {
  String _view = 'menu'; // menu | resume | setup | run | editor
  final RunForm form = RunForm();
  String proxyKind = 'file';
  bool live = false;
  String uid = '';
  String sessionFile = '';
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _poll = Timer.periodic(const Duration(milliseconds: 1200), (_) => _tick());
    _tick();
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _tick() async {
    // lite=1: the shell only needs running/uid, and must not force the
    // backend to copy the log/hit windows on every poll — the run view asks
    // for its own delta of those (one payload producer, no double copy).
    final r = await Api.get('/api/state?lite=1');
    if (!mounted || r.data is! Map) return;
    final data = r.data as Map;
    final nowLive = data['running'] == true;
    var nowUid = uid;
    final u = data['uid'];
    if (u is String && u.isNotEmpty) nowUid = u;
    // Only rebuild when something actually changed. This poll used to call
    // setState every 1.2s, which rebuilt every view under the cursor and
    // reset focused text fields mid-typing.
    if (nowLive != live || nowUid != uid) {
      setState(() {
        live = nowLive;
        uid = nowUid;
      });
    }
  }

  void setView(String v) {
    setState(() => _view = v);
  }

  void _navTo(String v) {
    if (v == 'setup') {
      newSession();
    } else {
      setView(v);
    }
  }

  void _openViewer() {
    Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => const ViewerScreen()),
    );
  }

  void newSession() {
    form.reset();
    proxyKind = 'file';
    setView('setup');
  }

  Future<void> startRun({bool resume = false, Map<String, dynamic>? session}) async {
    final combo = form.combo;
    if (!resume && combo.isEmpty) {
      form.err = 'Pick a combo file first (email:pass list), not the proxy list.';
      setView('setup');
      return;
    }
    form.err = '';
    final body = <String, dynamic>{
      'combo': combo,
      'proxy': form.proxy,
      'proxy_mode': proxyKind,
      'proxy_urls': form.proxyUrls.map((s) => s.trim()).where((s) => s.isNotEmpty).toList(),
      'keywords': form.kw,
      'workers': form.workers.isEmpty ? '20' : form.workers,
      'resume': resume,
      'session_file': (session != null ? (session['file'] ?? '') : sessionFile) as String,
      'keywords_on': form.capKw,
      'payments_on': form.capPay,
      'promos_on': form.capPromo,
      'fortnite_on': form.capFn,
    };
    final res = await Api.post('/api/start', body);
    if (!mounted) return;
    if (res.ok) {
      setView('run');
    } else {
      // Stay on the form and show the engine's actual reason instead of
      // flashing the run view and bouncing back.
      form.err = res.text.isEmpty ? 'Start failed' : res.text;
      setView('setup');
    }
  }

  Future<void> resumePicked(Map<String, dynamic>? session) async {
    if (session == null) return;
    setState(() {
      form.combo = (session['combo'] ?? '') as String;
      form.proxy = (session['proxy'] ?? '') as String;
      form.kw = (session['keywords'] ?? '') as String;
      form.workers = '${session['workers'] ?? 20}';
      form.err = '';
      sessionFile = (session['file'] ?? '') as String;
    });
    await startRun(resume: true, session: session);
  }

  String get _viewLabel {
    switch (_view) {
      case 'menu':
        return 'Home';
      case 'resume':
        return 'Resume session';
      case 'setup':
        return 'New session';
      case 'run':
        return 'Run monitor';
      default:
        return 'Editor';
    }
  }

  @override
  Widget build(BuildContext context) {
    final wide = _view == 'setup' || _view == 'run' || _view == 'editor';
    // Phone / portrait-tablet layout: a fixed 252px sidebar plus the desktop
    // content measure squeezed the form into a ~70px clipped column, and the
    // topbar sat under the iOS status bar. Below this breakpoint navigation
    // moves into a drawer and the content column runs full width — desktop
    // (>= 900px, the Windows window opens at 1280) is untouched.
    final size = MediaQuery.sizeOf(context);
    final compact = size.width < 900;
    return Scaffold(
      drawer: compact
          ? Drawer(
              backgroundColor: Colors.transparent,
              child: SafeArea(
                child: SingleChildScrollView(
                  child: _SideBar(
                    view: _view,
                    uid: uid,
                    fill: true,
                    onNav: (v) {
                      Navigator.of(context).pop();
                      _navTo(v);
                    },
                    onOpenViewer: () {
                      Navigator.of(context).pop();
                      _openViewer();
                    },
                  ),
                ),
              ),
            )
          : null,
      body: appBackdrop(
        Column(
          children: [
            _TopBar(live: live, viewLabel: _viewLabel, showMenu: compact),
            Expanded(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (!compact)
                    _SideBar(
                      view: _view,
                      uid: uid,
                      onNav: _navTo,
                      onOpenViewer: _openViewer,
                    ),
                  Expanded(
                    child: SingleChildScrollView(
                      padding: EdgeInsets.fromLTRB(
                          2, compact ? 14 : 18, 2, compact ? 32 : 40),
                      child: Center(
                        child: ConstrainedBox(
                          constraints: BoxConstraints(
                            maxWidth: compact
                                ? size.width
                                : wide
                                    ? 1480
                                    : size.width < 1160
                                        ? size.width - 320
                                        : 1040,
                          ),
                          child: Padding(
                            padding: EdgeInsets.symmetric(
                                horizontal: compact ? 14 : 22),
                            child: _buildView(),
                          ),
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
            _StatusBar(viewLabel: _viewLabel, uid: uid),
            // Update prompt, mounted once like <UpdateCheck /> in the web shell.
            const UpdateCheck(),
          ],
        ),
      ),
    );
  }

  Widget _buildView() {
    switch (_view) {
      case 'resume':
        return ResumeView(
          onBack: () => setView('menu'),
          onPick: resumePicked,
        );
      case 'setup':
        return SetupView(
          form: form,
          proxyKind: proxyKind,
          setProxyKind: (k) => setState(() => proxyKind = k),
          onBack: () => setView('menu'),
          onStart: () => startRun(),
        );
      case 'run':
        return RunView(
          onPause: () => Api.postEmpty('/api/pause'),
          onStop: () async {
            await Api.postEmpty('/api/stop');
            setView('menu');
          },
          onHome: () => setView('menu'),
        );
      case 'editor':
        return EditorView(onHome: () => setView('menu'));
      default:
        return HomeView(
          live: live,
          uid: uid,
          onNew: newSession,
          onResume: () => setView('resume'),
          onEditor: () => setView('editor'),
          onLive: () => setView('run'),
          onOpenViewer: () {
            Navigator.of(context)
                .push(MaterialPageRoute(builder: (_) => const ViewerScreen()));
          },
        );
    }
  }
}

/* ---------------- topbar --------------------------------------------------- */

class _TopBar extends StatelessWidget {
  final bool live;
  final String viewLabel;
  final bool showMenu;
  const _TopBar(
      {required this.live, required this.viewLabel, this.showMenu = false});

  @override
  Widget build(BuildContext context) {
    // Grow behind the iOS status bar so the clock never overlaps the title
    // (the inset is 0 on Windows, so the desktop bar stays 46px tall).
    final topInset = MediaQuery.paddingOf(context).top;
    return Container(
      height: 46 + topInset,
      padding:
          EdgeInsets.only(top: topInset, left: showMenu ? 6 : 16, right: 16),
      decoration: const BoxDecoration(
        color: Color(0xEB090C13),
        border: Border(bottom: BorderSide(color: T.line)),
      ),
      child: Row(
        children: [
          if (showMenu) ...[
            IconButton(
              onPressed: () => Scaffold.maybeOf(context)?.openDrawer(),
              icon: const Icon(Icons.menu, size: 20, color: T.txt),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 36, minHeight: 36),
              tooltip: 'Menu',
            ),
            const SizedBox(width: 2),
          ],
          Container(
            width: 22,
            height: 22,
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(7),
            ),
            clipBehavior: Clip.antiAlias,
            child: Image.asset(
              'assets/solo_icon.png',
              width: 22,
              height: 22,
              fit: BoxFit.cover,
              errorBuilder: (_, _, _) => const XboxMark(size: 15),
            ),
          ),
          // Compact (phone): skip the brand lockup so the page title and
          // status pill always fit — the brand lives in the drawer there.
          if (!showMenu) ...[
            const SizedBox(width: 9),
            Text('SOLO Checker',
                style: TextStyle(
                    fontSize: 13, fontWeight: FontWeight.w600, color: T.mut)),
            const SizedBox(width: 9),
            const Text('/', style: TextStyle(fontSize: 13, color: Color(0xFF46536E))),
          ],
          const SizedBox(width: 9),
          Expanded(
            child: Text(
              viewLabel,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                  fontSize: 13, fontWeight: FontWeight.w700, color: T.txt),
            ),
          ),
          StatusPill(on: true, label: live ? 'Checking' : 'Connected'),
        ],
      ),
    );
  }
}

/* ---------------- sidebar -------------------------------------------------- */

class _SideBar extends StatelessWidget {
  final String view;
  final String uid;
  final ValueChanged<String> onNav;
  final VoidCallback onOpenViewer;
  // fill = rendered inside the navigation drawer on compact (phone) layouts:
  // full width, full labels, no desktop card margin.
  final bool fill;
  const _SideBar({
    required this.view,
    required this.uid,
    required this.onNav,
    required this.onOpenViewer,
    this.fill = false,
  });

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(builder: (context, box) {
      final narrow = !fill && box.maxWidth < 760;
      return Container(
        width: fill ? double.infinity : (narrow ? 74 : 252),
        margin: fill
            ? const EdgeInsets.all(14)
            : const EdgeInsets.fromLTRB(16, 18, 0, 0),
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: const Color(0xEB10141F),
          border: Border.all(color: T.line),
          borderRadius: BorderRadius.circular(16),
          boxShadow: const [
            BoxShadow(
                color: Color(0x38000000), blurRadius: 16, offset: Offset(0, 4)),
          ],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // brand lockup
            Container(
              padding: const EdgeInsets.only(bottom: 12),
              decoration: const BoxDecoration(
                  border: Border(
                      bottom: BorderSide(color: T.lineSoft, width: 1))),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.center,
                children: [
                  Row(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      _LogoTile(
                        child: ClipRRect(
                          borderRadius: BorderRadius.circular(8),
                          child: Image.asset(
                            'assets/solo_icon.png',
                            width: 26,
                            height: 26,
                            fit: BoxFit.cover,
                            errorBuilder: (_, _, _) =>
                                const XboxMark(size: 26),
                          ),
                        ),
                      ),
                      const SizedBox(width: 8),
                      _LogoTile(child: const XboxMark(size: 26)),
                      const SizedBox(width: 8),
                      _LogoTile(
                          epic: true, child: const EpicMark(size: 22)),
                    ],
                  ),
                  if (!narrow) ...[
                    const SizedBox(height: 9),
                    const Text(
                      'Xbox Full Capture + Keyword Inboxer + Promos / Codes',
                      textAlign: TextAlign.center,
                      style: TextStyle(
                        fontSize: 12.5,
                        fontWeight: FontWeight.w800,
                        color: T.grn,
                        height: 1.3,
                      ),
                    ),
                    const SizedBox(height: 2),
                    const Text(
                      'Epic Games / Fortnite Full Capture',
                      textAlign: TextAlign.center,
                      style: TextStyle(
                        fontSize: 11.5,
                        fontWeight: FontWeight.w700,
                        color: T.pur,
                        letterSpacing: 0.23,
                      ),
                    ),
                  ],
                ],
              ),
            ),
            const SizedBox(height: 12),
            if (!narrow)
              Padding(
                padding: const EdgeInsets.only(left: 8, bottom: 7),
                child: Text('WORKSPACE', style: eyebrowStyle()),
              ),
            _NavItem(
              label: 'Home',
              icon: const IconHome(size: 15),
              on: view == 'menu',
              narrow: narrow,
              onTap: () => onNav('menu'),
            ),
            _NavItem(
              label: 'New session',
              icon: const IconNew(size: 15),
              on: view == 'setup' || view == 'run',
              narrow: narrow,
              onTap: () => onNav('setup'),
            ),
            _NavItem(
              label: 'Resume session',
              icon: const IconResume(size: 15),
              on: view == 'resume',
              narrow: narrow,
              onTap: () => onNav('resume'),
            ),
            _NavItem(
              label: 'Editor',
              icon: const IconEditor(size: 15),
              on: view == 'editor',
              narrow: narrow,
              onTap: () => onNav('editor'),
            ),
            _NavItem(
              label: 'View valid',
              icon: const IconList(size: 15),
              on: false,
              narrow: narrow,
              onTap: onOpenViewer,
            ),
            const SizedBox(height: 12),
            Container(
              padding: const EdgeInsets.only(top: 12),
              decoration: const BoxDecoration(
                  border: Border(
                      top: BorderSide(color: T.lineSoft, width: 1))),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  InkWell(
                    borderRadius: BorderRadius.circular(999),
                    onTap: () =>
                        openLink('https://t.me/+naqymNMpt75jYjdh'),
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 12, vertical: 5),
                      decoration: BoxDecoration(
                        color: const Color(0x1438BDF8),
                        border: Border.all(
                            color: const Color(0x4038BDF8)),
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const TgMark(size: 18, gid: 'tgSide'),
                          const SizedBox(width: 8),
                          if (!narrow)
                            // Flexible: long handles (or wide test fonts)
                            // ellipsize instead of overflowing the card.
                            Flexible(
                              child: Text('@solodolosouls',
                                  overflow: TextOverflow.ellipsis,
                                  maxLines: 1,
                                  style: const TextStyle(
                                      fontSize: 13,
                                      fontWeight: FontWeight.w700,
                                      color: T.blu)),
                            ),
                        ],
                      ),
                    ),
                  ),
                  if (uid.isNotEmpty) ...[
                    const SizedBox(height: 8),
                    _UidTag(uid: uid),
                  ],
                ],
              ),
            ),
          ],
        ),
      );
    });
  }
}

class _LogoTile extends StatelessWidget {
  final Widget child;
  final bool epic;
  const _LogoTile({required this.child, this.epic = false});

  @override
  Widget build(BuildContext context) => Container(
        width: 42,
        height: 42,
        decoration: BoxDecoration(
          gradient: RadialGradient(
            center: const Alignment(-0.4, -1),
            colors: epic
                ? [const Color(0x12FFFFFF), const Color(0x00FFFFFF)]
                : [const Color(0x1AFFFFFF), const Color(0x05FFFFFF)],
          ),
          border: Border.all(color: T.line),
          borderRadius: BorderRadius.circular(12),
        ),
        alignment: Alignment.center,
        child: child,
      );
}

class _UidTag extends StatefulWidget {
  final String uid;
  const _UidTag({required this.uid});

  @override
  State<_UidTag> createState() => _UidTagState();
}

class _UidTagState extends State<_UidTag> {
  bool _copied = false;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(999),
      onTap: () async {
        final ok = await copyText('ID ${widget.uid}');
        if (!ok || !mounted) return;
        setState(() => _copied = true);
        Timer(const Duration(milliseconds: 900), () {
          if (mounted) setState(() => _copied = false);
        });
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
        decoration: BoxDecoration(
          color: T.tagPurpleBg,
          border: Border.all(color: T.tagPurpleBd),
          borderRadius: BorderRadius.circular(999),
        ),
        child: Text(
          _copied ? 'ID ${widget.uid} copied' : 'ID ${widget.uid}',
          style: const TextStyle(
              fontSize: 11, fontWeight: FontWeight.w600, color: T.tagPurpleFg),
        ),
      ),
    );
  }
}

class _NavItem extends StatefulWidget {
  final String label;
  final Widget icon;
  final bool on;
  final bool narrow;
  final VoidCallback onTap;
  const _NavItem({
    required this.label,
    required this.icon,
    required this.on,
    required this.narrow,
    required this.onTap,
  });

  @override
  State<_NavItem> createState() => _NavItemState();
}

class _NavItemState extends State<_NavItem> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final on = widget.on;
    return Padding(
      padding: const EdgeInsets.only(bottom: 3),
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          borderRadius: BorderRadius.circular(9),
          onHover: (h) => setState(() => _hover = h),
          onTap: widget.onTap,
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 130),
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
            decoration: BoxDecoration(
              color: on
                  ? const Color(0x297C5CFF)
                  : _hover
                      ? const Color(0x0DFFFFFF)
                      : Colors.transparent,
              border: Border.all(
                  color: on ? const Color(0x617C5CFF) : Colors.transparent),
              borderRadius: BorderRadius.circular(9),
            ),
            child: Row(
              children: [
                IconTheme(
                    data: IconThemeData(
                        color: on ? Colors.white : T.mut, size: 15),
                    child: widget.icon),
                if (!widget.narrow) ...[
                  const SizedBox(width: 10),
                  Expanded(
                    child: Text(
                      widget.label,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 13,
                        fontWeight: FontWeight.w600,
                        color: on ? Colors.white : T.mut,
                      ),
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/* ---------------- statusbar ------------------------------------------------ */

class _StatusBar extends StatelessWidget {
  final String viewLabel;
  final String uid;
  const _StatusBar({required this.viewLabel, required this.uid});

  @override
  Widget build(BuildContext context) {
    final mq = MediaQuery.of(context);
    final small = mq.size.width < 720;
    // Keep the row above the iPhone home indicator (inset is 0 on Windows).
    final bottomInset = mq.padding.bottom;
    return Container(
      height: 30 + bottomInset,
      padding: EdgeInsets.only(left: 16, right: 16, bottom: bottomInset),
      decoration: const BoxDecoration(
        color: Color(0xF0090C13),
        border: Border(top: BorderSide(color: T.line)),
      ),
      child: Row(
        children: [
          const StatusDot(on: true),
          const SizedBox(width: 10),
          Text('connected', style: mutedStyle(11.5)),
          if (uid.isNotEmpty && !small) ...[
            const SizedBox(width: 10),
            _Sep(),
            const SizedBox(width: 10),
            Text(uid,
                style: const TextStyle(
                    fontSize: 11.5,
                    fontWeight: FontWeight.w600,
                    color: Color(0xFFB6C2D8))),
          ],
          const Spacer(),
          if (!small) ...[
            Text(viewLabel,
                style: const TextStyle(
                    fontSize: 11.5,
                    fontWeight: FontWeight.w600,
                    color: Color(0xFFB6C2D8))),
            const SizedBox(width: 10),
            _Sep(),
            const SizedBox(width: 10),
            Text('SOLO Checker', style: mutedStyle(11.5)),
          ],
        ],
      ),
    );
  }
}

class _Sep extends StatelessWidget {
  @override
  Widget build(BuildContext context) => Container(
        width: 1,
        height: 12,
        color: T.line,
      );
}

/* ---------------- update prompt -------------------------------------------- */

class UpdateCheck extends StatefulWidget {
  const UpdateCheck({super.key});

  @override
  State<UpdateCheck> createState() => _UpdateCheckState();
}

class _UpdateCheckState extends State<UpdateCheck> {
  bool _busy = false;
  String _err = '';

  @override
  void initState() {
    super.initState();
    _check();
  }

  Future<void> _check() async {
    final r = await Api.get('/api/update/check');
    if (!mounted) return;
    if (r.data is Map && r.data['ok'] == true && r.data['update'] == true) {
      final upd = Map<String, dynamic>.from(r.data);
      _showPrompt(upd);
    }
  }

  Future<void> _apply(VoidCallback refresh) async {
    setState(() {
      _busy = true;
      _err = '';
    });
    refresh();
    final r = await Api.post('/api/update/apply', const {});
    if (!mounted) return;
    if (!r.ok || (r.data is Map && r.data['ok'] != true)) {
      setState(() {
        _busy = false;
        _err = (r.data is Map && r.data['error'] != null)
            ? r.data['error'].toString()
            : (r.text.isEmpty ? 'update failed' : r.text);
      });
    } else {
      final restarting = r.data is Map && r.data['restarting'] != false;
      setState(() => _err =
          restarting ? 'Restarting…' : 'Installed — reopen the app once.');
      refresh();
      if (restarting) {
        // The swap is already done; the restart script takes over the
        // moment this window exits.
        Future.delayed(const Duration(milliseconds: 500), () => exit(0));
      }
    }
    refresh();
  }

  void _showPrompt(Map<String, dynamic> upd) {
    if (!mounted) return;
    showAppModal<void>(
      context,
      StatefulBuilder(builder: (context, setModal) {
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            // Explicit color + decoration on both lines: this prompt must
            // never fall back to MaterialApp's red/underlined debug style
            // (the "consider putting your text in a Material" fallback),
            // even if it is ever rendered without a Material ancestor.
            Text('UPDATE',
                style:
                    eyebrowStyle().copyWith(decoration: TextDecoration.none)),
            const SizedBox(height: 4),
            Text('Version ${upd['remote']} is available',
                style: const TextStyle(
                    fontSize: 15,
                    fontWeight: FontWeight.w700,
                    color: T.txt,
                    decoration: TextDecoration.none)),
            const SizedBox(height: 14),
            Row(
              children: [
                TextBtn(
                  label: _busy ? 'Updating…' : 'Update now',
                  kind: BtnKind.primary,
                  disabled: _busy,
                  onPressed: () => _apply(() => setModal(() {})),
                ),
                const SizedBox(width: 10),
                TextBtn(
                  label: 'Later',
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
            if (_err.isNotEmpty) ...[
              const SizedBox(height: 10),
              Muted(_err, color: T.red),
            ],
          ],
        );
      }),
    );
  }

  @override
  Widget build(BuildContext context) => const SizedBox.shrink();
}
