// Dashboard entry: app shell, view state machine, shared /api/state polling.
import { html, render, useState, useEffect, api, apiJSON, XboxLogo, TelegramTg0, Tag, UidLine, Modal, IconHome, IconNew, IconResume, IconEditor, IconList } from "../app.mjs";
import { HomeView, ResumeView } from "./home.mjs";
import { SetupView } from "./setup.mjs";
import { RunView } from "./run.mjs";
import { EditorView } from "./editor.mjs";

const emptyForm = () => ({
  combo: "", proxy: "", proxy_urls: [""], kw: "", workers: "20",
  rememberProxy: false, proxyFileCount: null, proxyCount: null, err: "",
  capKw: true, capPay: true, capPromo: true, capFn: true,
});

export function App() {
  const [view, setView] = useState("menu"); // menu | resume | setup | run | editor
  const [form, setFormState] = useState(emptyForm());
  const [proxyKind, setProxyKind] = useState("file");
  const [live, setLive] = useState(false);
  const [uid, setUid] = useState("");
  const [sessionFile, setSessionFile] = useState("");

  const set = (patch) => setFormState((f) => ({ ...f, ...patch }));

  // single shared /api/state poll for uid + live-run button (menu views only)
  useEffect(() => {
    let alive = true;
    const tick = async () => {
      try {
        const { data } = await api("/api/state");
        if (!alive || !data) return;
        setLive(!!data.running);
        if (data.uid) setUid(data.uid);
      } catch (e) {}
    };
    tick();
    const t = setInterval(tick, 1200);
    return () => { alive = false; clearInterval(t); };
  }, []);

  const start = async (resume, session) => {
    const combo = form.combo || "";
    if (!resume && !combo) {
      set({ err: "Pick a combo file first (email:pass list), not the proxy list." });
      setView("setup");
      return;
    }
    if (/proxy/i.test(combo)) {
      set({ err: "Combos is a proxy file. Browse the email:pass combo list in Combos." });
    } else {
      set({ err: "" });
    }
    setView("run");
    const body = {
      combo,
      proxy: form.proxy || "",
      proxy_mode: proxyKind,
      proxy_urls: (form.proxy_urls || []).map((s) => s.trim()).filter(Boolean),
      keywords: form.kw || "",
      workers: form.workers || "20",
      resume: !!resume,
      session_file: (session && session.file) || sessionFile || "",
      keywords_on: !!form.capKw,
      payments_on: !!form.capPay,
      promos_on: !!form.capPromo,
      fortnite_on: !!form.capFn,
    };
    try {
      const res = await fetch("/api/start", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      if (!res.ok) {
        set({ err: await res.text() });
        setView("setup");
        return;
      }
      setView("run");
    } catch (e) {
      set({ err: String(e) });
      setView("setup");
    }
  };

  const pause = async () => { await fetch("/api/pause", { method: "POST" }); };
  const stop = async () => {
    await fetch("/api/stop", { method: "POST" });
    setView("menu");
  };

  const resumePicked = (session) => {
    if (!session) return;
    setFormState((f) => ({
      ...f,
      combo: session.combo || "",
      proxy: session.proxy || "",
      kw: session.keywords || "",
      workers: String(session.workers || 20),
      err: "",
    }));
    setSessionFile(session.file || "");
    start(true, session);
  };

  const viewLabel = view === "menu" ? "Home"
    : view === "resume" ? "Resume session"
    : view === "setup" ? "New session"
    : view === "run" ? "Run monitor"
    : "Editor";

  return html`
    <div class="app">
      <div class="topbar">
        <div class="crumbs">
          <span class="crumb-mark"><${XboxLogo} /></span>
          <span class="crumb">SOLO Checker</span>
          <span class="crumb-sep">/</span>
          <span class="crumb-now">${viewLabel}</span>
        </div>
        <span class="status-pill"><span class=${"dot" + (live ? " on" : "")}></span>${live ? "Checking" : "Connected"}</span>
      </div>

      <div class="shell">
        <aside class="side">
          <div class="brand">
            <span class="logo-tile"><${XboxLogo} /></span>
            <span class="logo-tile epic"><svg fill="#c8c8c8" role="img" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M3.537 0C2.165 0 1.66.506 1.66 1.879V18.44a4.262 4.262 0 00.02.433c.031.3.037.59.316.92.027.033.311.245.311.245.153.075.258.13.43.2l8.335 3.491c.433.199.614.276.928.27h.002c.314.006.495-.071.928-.27l8.335-3.492c.172-.07.277-.124.43-.2 0 0 .284-.211.311-.243.28-.33.285-.621.316-.92a4.261 4.261 0 00.02-.434V1.879c0-1.373-.506-1.88-1.878-1.88zm13.366 3.11h.68c1.138 0 1.688.553 1.688 1.696v1.88h-1.374v-1.8c0-.369-.17-.54-.523-.54h-.235c-.367 0-.537.17-.537.539v5.81c0 .369.17.54.537.54h.262c.353 0 .523-.171.523-.54V8.619h1.373v2.143c0 1.144-.562 1.71-1.7 1.71h-.694c-1.138 0-1.7-.566-1.7-1.71V4.82c0-1.144.562-1.709 1.7-1.709zm-12.186.08h3.114v1.274H6.117v2.603h1.648v1.275H6.117v2.774h1.74v1.275h-3.14zm3.816 0h2.198c1.138 0 1.7.564 1.7 1.708v2.445c0 1.144-.562 1.71-1.7 1.71h-.799v3.338h-1.4zm4.53 0h1.4v9.201h-1.4zm-3.13 1.235v3.392h.575c.354 0 .523-.171.523-.54V4.965c0-.368-.17-.54-.523-.54zm-3.74 10.147a1.708 1.708 0 01.591.108 1.745 1.745 0 01.49.299l-.452.546a1.247 1.247 0 00-.308-.195.91.91 0 00-.363-.068.658.658 0 00-.28.06.703.703 0 00-.224.163.783.783 0 00-.151.243.799.799 0 00-.056.299v.008a.852.852 0 00.056.31.7.7 0 00.157.245.736.736 0 00.238.16.774.774 0 00.303.058.79.79 0 00.445-.116v-.339h-.548v-.565H7.37v1.255a2.019 2.019 0 01-.524.307 1.789 1.789 0 01-.683.123 1.642 1.642 0 01-.602-.107 1.46 1.46 0 01-.478-.3 1.371 1.371 0 01-.318-.455 1.438 1.438 0 01-.115-.58v-.008a1.426 1.426 0 01.113-.57 1.449 1.449 0 01.312-.46 1.418 1.418 0 01.474-.309 1.58 1.58 0 01.598-.111 1.708 1.708 0 01.045 0zm11.963.008a2.006 2.006 0 01.612.094 1.61 1.61 0 01.507.277l-.386.546a1.562 1.562 0 00-.39-.205 1.178 1.178 0 00-.388-.07.347.347 0 00-.208.052.154.154 0 00-.07.127v.008a.158.158 0 00.022.084.198.198 0 00.076.066.831.831 0 00.147.06c.062.02.14.04.236.061a3.389 3.389 0 01.43.122 1.292 1.292 0 01.328.17.678.678 0 01.207.24.739.739 0 01.071.337v.008a.865.865 0 01-.081.382.82.82 0 01-.229.285 1.032 1.032 0 01-.353.18 1.606 1.606 0 01-.46.061 2.16 2.16 0 01-.71-.116 1.718 1.718 0 01-.593-.346l.43-.514c.277.223.578.335.9.335a.457.457 0 00.236-.05.157.157 0 00.082-.142v-.008a.15.15 0 00-.02-.077.204.204 0 00-.073-.066.753.753 0 00-.143-.062 2.45 2.45 0 00-.233-.062 5.036 5.036 0 01-.413-.113 1.26 1.26 0 01-.331-.16.72.72 0 01-.222-.243.73.73 0 01-.082-.36v-.008a.863.863 0 01.074-.359.794.794 0 01.214-.283 1.007 1.007 0 01.34-.185 1.423 1.423 0 01.448-.066 2.006 2.006 0 01.025 0zm-9.358.025h.742l1.183 2.81h-.825l-.203-.499H8.623l-.198.498h-.81zm2.197.02h.814l.663 1.08.663-1.08h.814v2.79h-.766v-1.602l-.711 1.091h-.016l-.707-1.083v1.593h-.754zm3.469 0h2.235v.658h-1.473v.422h1.334v.61h-1.334v.442h1.493v.658h-2.255zm-5.3.897l-.315.793h.624zm-1.145 5.19h8.014l-4.09 1.348z"/></svg></span>
            <div class="titles">
              <div class="t1">Xbox Full Capture + Keyword Inboxer + Promos / Codes</div>
              <div class="t2">Epic Games / Fortnite Full Capture</div>
            </div>
          </div>

          <div class="side-section">
            <div class="side-label">Workspace</div>
            <div class="side-nav">
              <button class=${"nav-item" + (view === "menu" ? " on" : "")} type="button" onClick=${() => setView("menu")}>
                <${IconHome} size=${14} />Home
              </button>
              <button class=${"nav-item" + (view === "setup" || view === "run" ? " on" : "")} type="button" onClick=${() => { setFormState(emptyForm()); setProxyKind("file"); setView("setup"); }}>
                <${IconNew} size=${14} />New session
              </button>
              <button class=${"nav-item" + (view === "resume" ? " on" : "")} type="button" onClick=${() => setView("resume")}>
                <${IconResume} size=${14} />Resume session
              </button>
              <button class=${"nav-item" + (view === "editor" ? " on" : "")} type="button" onClick=${() => setView("editor")}>
                <${IconEditor} size=${14} />Editor
              </button>
              <button class="nav-item" type="button" onClick=${() => { fetch("/api/open-viewer", { method: "POST" }); }}>
                <${IconList} size=${14} />View valid
              </button>
            </div>
          </div>

          <div class="side-foot">
            <div class="credit">
              <${TelegramTg0} size=${18} />
              @solodolosouls
            </div>
            <${UidLine} uid=${uid} />
          </div>
        </aside>

        <main class="main">
          <div class="wrap ${view === "setup" || view === "run" || view === "editor" ? "wide" : ""}">

      ${view === "menu" && html`
        <${HomeView}
          onNew=${() => { setFormState(emptyForm()); setProxyKind("file"); setView("setup"); }}
          onResume=${() => setView("resume")}
          onEditor=${() => setView("editor")}
          onOpenViewer=${async () => { await fetch("/api/open-viewer", { method: "POST" }); }}
          onLive=${() => setView("run")}
          live=${live}
          uid=${uid} />`}

      ${view === "resume" && html`
        <${ResumeView} onBack=${() => setView("menu")} onPick=${resumePicked} />`}

      ${view === "setup" && html`
        <${SetupView}
          form=${form} set=${set}
          proxyKind=${proxyKind} setProxyKind=${setProxyKind}
          onBack=${() => setView("menu")}
          onStart=${() => start(false)} />`}

      ${view === "run" && html`
        <${RunView}
          onPause=${pause}
          onStop=${stop}
          onHome=${() => setView("menu")} />`}

      ${view === "editor" && html`
        <${EditorView} onHome=${() => setView("menu")} />`}
          </div>
        </main>
      </div>

      <div class="statusbar">
        <div class="sb-group">
          <span class="dot on"></span>
          <span>connected</span>
          ${uid && html`<span class="sb-sep"></span><span class="sb-strong">${uid}</span>`}
        </div>
        <div class="sb-group">
          <span class="sb-strong">${viewLabel}</span>
          <span class="sb-sep"></span>
          <span>SOLO Checker</span>
        </div>
      </div>
    </div>

    <${UpdateCheck} />`;
}

/* Update prompt — same endpoints and behavior as the old UI. */
function UpdateCheck() {
  const [upd, setUpd] = useState(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    (async () => {
      try {
        const { data } = await api("/api/update/check");
        if (data && data.ok && data.update) setUpd(data);
      } catch (e) {}
    })();
  }, []);

  const apply = async () => {
    setBusy(true);
    setErr("");
    try {
      const { ok, data } = await apiJSON("/api/update/apply", {});
      if (!ok) { setErr((data && data.error) || "update failed"); setBusy(false); return; }
      setErr("Restarting…");
    } catch (e) {
      setErr(String(e));
      setBusy(false);
    }
  };

  return html`
    <${Modal} show=${!!upd} width=${520}>
      <div class="muted">UPDATE</div>
      <b>Version ${upd && upd.remote} is available</b>
      <div class="row" style="margin-top:14px">
        <button class="primary" type="button" disabled=${busy} onClick=${apply}>${busy ? "Updating…" : "Update now"}</button>
        <button class="ghost" type="button" onClick=${() => setUpd(null)}>Later</button>
      </div>
      ${err && html`<div class="muted" style=${{ color: "var(--red)", marginTop: "10px" }}>${err}</div>`}
    <//>`;
}

render(html`<${App} />`, document.getElementById("app"));
