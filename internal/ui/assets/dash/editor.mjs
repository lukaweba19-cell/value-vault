// Dashboard: combo editor (load/clean, generate, signatures) as Preact components.
import { html, useState, useEffect, useRef, api, apiJSON, IconSave, IconDownload, IconNew, IconNext, Tag, Modal } from "../app.mjs";

const fmtSize = (n) => {
  n = Number(n) || 0;
  if (n < 1024) return n + " B";
  if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
  if (n < 1073741824) return (n / 1048576).toFixed(1) + " MB";
  return (n / 1073741824).toFixed(2) + " GB";
};

function StatRow({ x, kind, onSave }) {
  return html`
    <div class="ed-item">
      <b>${x.name}</b>
      <span style=${{ color: "var(--mut)" }}>${String(x.count)}</span>
      <button class="ico-btn" type="button" title="Export" onClick=${() => onSave(kind, x.name)}>
        <${IconSave} size=${16} />
      </button>
    </div>`;
}

export function EditorView({ onHome }) {
  const [view, setView] = useState("home"); // home | work
  const [mode, setMode] = useState("load"); // load | new (which entry button opened work)
  const [tab, setTab] = useState("load"); // load | new | sign

  const [path, setPath] = useState("");
  const [lines, setLines] = useState(0);
  const [size, setSize] = useState(0);
  const [msg, setMsg] = useState("");

  const [domains, setDomains] = useState([]);
  const [urls, setUrls] = useState([]);
  const [filterAll, setFilterAll] = useState([]);
  const [modalKind, setModalKind] = useState(null); // email | host | filter

  const [mailTypes, setMailTypes] = useState("");
  const [urlFilter, setUrlFilter] = useState("");
  const [filterState, setFilterState] = useState("idle"); // idle | running | done
  const [filterStatus, setFilterStatus] = useState("");
  const [exportOpen, setExportOpen] = useState(false);
  const [genExportOpen, setGenExportOpen] = useState(false);

  const [banners, setBanners] = useState([]);
  const [bannerSel, setBannerSel] = useState("");
  const [bannerText, setBannerText] = useState("");
  const [bannerName, setBannerName] = useState("");

  const [nameMode, setNameMode] = useState("paste");
  const [users, setUsers] = useState("");
  const [namePath, setNamePath] = useState("");
  const [doms, setDoms] = useState("outlook.com, hotmail.com, gmail.com");
  const [passMode, setPassMode] = useState("paste");
  const [passes, setPasses] = useState("");
  const [passPath, setPassPath] = useState("");
  const [count, setCount] = useState("500");
  const [genOut, setGenOut] = useState("");

  const [sigHead, setSigHead] = useState("");
  const [sigTail, setSigTail] = useState("");
  const [sigHeadGen, setSigHeadGen] = useState("");
  const [sigTailGen, setSigTailGen] = useState("");
  const [savePathGen, setSavePathGen] = useState("");

  const anES = useRef(null);
  const fES = useRef(null);
  useEffect(() => () => {
    try { anES.current && anES.current.close(); } catch (e) {}
    try { fES.current && fES.current.close(); } catch (e) {}
  }, []);

  // message display follows the original: shown under the file input, or under
  // the signature selector while the Signatures tab is open.
  const Msg = () => (msg ? html`<div class="muted" style=${{ color: "#9af0c8", margin: "6px 0" }}>${msg}</div>` : null);

  const loadBanners = async () => {
    try {
      const { data } = await api("/api/editor/banners");
      setBanners((data && data.banners) || []);
    } catch (e) { setBanners([]); }
  };
  useEffect(() => { loadBanners(); }, []);

  const sigTextOf = (idx) => {
    const b = banners[Number(idx)];
    return b ? (b.text || "") : "";
  };

  const openWork = (m) => { setMode(m); setTab(m); setView("work"); };
  const paint = (d) => {
    if (d && d.error) { setMsg(d.error); return; }
    setMsg(d.done ? "Analyzed " + (d.lines || 0) + " lines" : "Analyzing… " + (d.lines || 0) + " lines");
    if (d.path) setPath(d.path);
    setLines(d.lines || 0);
    setSize(d.size || 0);
    setDomains(d.domains || []);
    setUrls(d.urls || []);
  };

  // p0 lets Browse pass the freshly picked path directly: setState is async,
  // so reading `path` here would still see the previous (empty) value on the
  // first pick and wrongly answer "Choose a file first".
  const loadFile = (p0) => {
    const p = String(p0 != null ? p0 : path || "").trim();
    if (!p) { setMsg("Choose a file first"); return; }
    setMsg("Analyzing…");
    try { anES.current && anES.current.close(); } catch (e) {}
    const es = new EventSource("/api/editor/analyze-stream?path=" + encodeURIComponent(p));
    anES.current = es;
    es.onmessage = (ev) => {
      try {
        const d = JSON.parse(ev.data);
        paint(d);
        if (d.done) { es.close(); setMsg("Analyzed " + (d.lines || 0) + " lines"); }
      } catch (e) {}
    };
    es.onerror = () => { es.close(); };
  };

  const browse = async (kind) => {
    const { data } = await api("/api/pick?kind=" + encodeURIComponent(kind));
    if (!data || !data.path) return;
    if (kind === "editor") { setPath(data.path); loadFile(data.path); }
    else if (kind === "editor-names") {
      setNamePath(data.path);
      const r = await apiJSON("/api/editor/analyze", { path: data.path });
      if (r.data) setUsers(r.data.large ? (r.data.preview || "") : (r.data.text || r.data.preview || ""));
    } else if (kind === "editor-pass") {
      setPassPath(data.path);
      const r = await apiJSON("/api/editor/analyze", { path: data.path });
      if (r.data) setPasses(r.data.large ? (r.data.preview || "") : (r.data.text || r.data.preview || ""));
    }
  };

  const saveOne = async (kind, name) => {
    const def = String(name).replace(/[^\w.\-]+/g, "_") + ".txt";
    const r = await api("/api/pick-save?name=" + encodeURIComponent(def));
    if (!r.data || !r.data.path) return;
    setMsg("Exporting " + name + "…");
    const body = {
      path: path,
      dest: r.data.path,
      banner: sigTextOf(sigHead),
      banner_end: sigTextOf(sigTail),
    };
    if (kind === "email") body.email_types = "@" + name;
    else body.url = name;
    const res = await apiJSON("/api/editor/filter", body);
    if (!res.ok) { setMsg((res.data && res.data.error) || "Save failed"); return; }
    setMsg("Saved " + ((res.data && res.data.extracted) || 0) + " to " + ((res.data && res.data.dest) || r.data.path));
  };

  const runFilter = () => {
    setFilterState("running");
    setFilterStatus("Filtering…");
    try { fES.current && fES.current.close(); } catch (e) {}
    const q = "path=" + encodeURIComponent(path) +
      "&email_types=" + encodeURIComponent(mailTypes) +
      "&url=" + encodeURIComponent(urlFilter);
    const es = new EventSource("/api/editor/filter-stream?" + q);
    fES.current = es;
    es.onmessage = (ev) => {
      try {
        const d = JSON.parse(ev.data);
        setFilterAll(d.domains || filterAll);
        if (d.done) {
          es.close();
          setFilterState("done");
          setFilterStatus("Filtered " + ((d.extracted || 0)) + " combos");
        } else {
          setFilterStatus("Filtering… " + ((d.extracted || 0)) + " hits / " + ((d.scanned || 0)) + " scanned");
        }
      } catch (e) {}
    };
    es.onerror = () => { es.close(); setFilterState("idle"); setFilterStatus("Filter stopped"); };
  };

  const exportFiltered = async () => {
    const url = (urlFilter || "filtered").replace(/[^\w.\-]+/g, "_");
    const r = await api("/api/pick-save?name=" + encodeURIComponent(url + ".txt"));
    if (!r.data || !r.data.path) return;
    setFilterStatus("Saving…");
    const res = await apiJSON("/api/editor/filter", {
      path: path,
      dest: r.data.path,
      email_types: mailTypes,
      url: urlFilter,
      banner: sigTextOf(sigHead),
      banner_end: sigTextOf(sigTail),
    });
    if (!res.ok) { setFilterStatus((res.data && res.data.error) || "Save failed"); return; }
    setExportOpen(false);
    setFilterStatus("Saved " + ((res.data && res.data.extracted) || 0) + " combos to " + ((res.data && res.data.dest) || r.data.path));
  };

  const exportGenerated = async () => {
    const r = await api("/api/pick-save?name=" + encodeURIComponent("combos-export.txt"));
    if (!r.data || !r.data.path) return;
    let text = genOut || "";
    const head = sigTextOf(sigHeadGen);
    const tail = sigTextOf(sigTailGen);
    if (head) text = head.replace(/\s+$/, "") + "\n" + text;
    if (tail) text = text.replace(/\s+$/, "") + "\n" + tail;
    await apiJSON("/api/editor/save", { path: r.data.path, text });
    setGenExportOpen(false);
  };

  const saveGenerated = async () => {
    let p = (savePathGen || "").trim();
    if (!p) {
      const r = await api("/api/pick-save");
      p = (r.data && r.data.path) || "combos-export.txt";
      setSavePathGen(p);
    }
    const r2 = await apiJSON("/api/editor/save", {
      path: p,
      text: genOut || "",
      banner: sigTextOf(sigHeadGen) || sigTextOf(sigHead),
      banner_end: sigTextOf(sigTailGen) || sigTextOf(sigTail),
    });
    if (r2.ok) setMsg("Exported to " + p);
    else setMsg((r2.data && (r2.data.error || r2.data)) ? String(r2.data.error || "") : "Save failed");
  };

  const generate = async () => {
    const r = await apiJSON("/api/editor/generate", {
      users, domains: doms, passwords: passes, count: parseInt(count || "500", 10),
    });
    if (r.data) {
      setGenOut(r.data.text || "");
      paint(r.data);
    }
  };

  const loadSig = () => {
    const b = banners[Number(bannerSel)];
    if (b) { setBannerText(b.text || ""); setBannerName(b.name || ""); }
  };
  const deleteSig = async () => {
    const b = banners[Number(bannerSel)];
    if (!b || !b.name) return;
    await fetch("/api/editor/banners?name=" + encodeURIComponent(b.name), { method: "DELETE" });
    setBannerText("");
    setBannerName("");
    loadBanners();
  };
  const saveSig = async () => {
    if (!bannerName.trim() || !bannerText) { setMsg("Name and signature text required"); return; }
    const r = await apiJSON("/api/editor/banners", { name: bannerName.trim(), text: bannerText });
    if (r.ok) { setMsg("Signature saved"); loadBanners(); }
    else setMsg("Save failed");
  };

  const resetFilterBtn = () => { setFilterState("idle"); };

  const modalList = modalKind === "host" ? urls : modalKind === "filter" ? filterAll : domains;
  const modalSaveKind = modalKind === "host" ? "host" : "email";

  const SigSelect = ({ value, onChange }) => html`
    <select value=${value} onChange=${(e) => onChange(e.target.value)}>
      <option value="">(none)</option>
      ${banners.map((b, i) => html`<option key=${i} value=${String(i)}>${b.name}</option>`)}
    </select>`;

  if (view === "home") {
    return html`
      <div class="panel">
        <div class="section-head">
          <div class="head-copy">
            <div class="eyebrow">Combo editor</div>
            <h2>What do you want to do?</h2>
            <div class="head-sub">Open an existing list or generate a fresh one.</div>
          </div>
        </div>
        <div class="grid-2">
          <button class="choice" type="button" onClick=${() => openWork("load")}>
            <div class="ico"><${IconDownload} size=${18} /></div>
            <h3>Load file</h3>
            <p>Open a combo or ULP list. Large files are scanned, not loaded into RAM.</p>
            <span class="choice-go"><${IconNext} size=${14} /></span>
          </button>
          <button class="choice" type="button" onClick=${() => openWork("new")}>
            <div class="ico"><${IconNew} size=${18} /></div>
            <h3>Create new</h3>
            <p>Generate combos from names, domains and a password list.</p>
            <span class="choice-go"><${IconNext} size=${14} /></span>
          </button>
        </div>
        <div class="bar" style="margin-top:16px">
          <button class="ghost" onClick=${onHome}>Back</button>
        </div>
      </div>`;
  }

  return html`
    <div class="panel">
      <div class="section-head">
        <div class="head-copy">
          <div class="eyebrow">${tab === "new" ? "GENERATE" : tab === "sign" ? "SIGNATURES" : "LOAD / CLEAN"}</div>
          <h2>Combo editor</h2>
        </div>
        <div class="btnbar">
          <${Tag}>${lines + " lines"}<//>
          <${Tag} kind="green">${fmtSize(size)}<//>
        </div>
      </div>
      <div class="tabs">
        ${mode === "load" && html`<button type="button" class=${tab === "load" ? "on" : ""} onClick=${() => setTab("load")}>File</button>`}
        ${mode === "new" && html`<button type="button" class=${tab === "new" ? "on" : ""} onClick=${() => setTab("new")}>Generate</button>`}
        <button type="button" class=${tab === "sign" ? "on" : ""} onClick=${() => setTab("sign")}>Signatures</button>
      </div>

      ${tab === "load" && html`
        <div>
          <label>File</label>
          <div class="row tight">
            <input value=${path} placeholder="combo / ULP path" onInput=${(e) => { setPath(e.target.value); }} />
            <button class="ghost" type="button" onClick=${() => browse("editor")}>Browse</button>
          </div>
          <${Msg} />
          <div class="desk-grid" style="margin-top:8px">
            <div>
              <div class="bar" style=${{ marginBottom: "8px" }}>
                <div class="eyebrow">TOP EMAIL TYPES</div>
                <button class="ghost" type="button" onClick=${() => setModalKind("email")}>View all</button>
              </div>
              ${(domains || []).slice(0, 10).map((x, i) => html`<${StatRow} key=${i} x=${x} kind="email" onSave=${saveOne} />`)}
            </div>
            <div>
              <div class="bar" style=${{ marginBottom: "8px" }}>
                <div class="eyebrow">TOP URL / DOMAINS</div>
                <button class="ghost" type="button" onClick=${() => setModalKind("host")}>View all</button>
              </div>
              ${(urls || []).slice(0, 10).map((x, i) => html`<${StatRow} key=${i} x=${x} kind="host" onSave=${saveOne} />`)}
            </div>
          </div>
          <div class="desk-grid">
            <div>
              <label>Email types</label>
              <input value=${mailTypes} placeholder="@outlook @hotmail @msn" onInput=${(e) => { setMailTypes(e.target.value); resetFilterBtn(); }} />
            </div>
            <div>
              <label>URL / domain</label>
              <input value=${urlFilter} placeholder="epicgames.com" onInput=${(e) => { setUrlFilter(e.target.value); resetFilterBtn(); }} />
            </div>
          </div>
          <div class="row">
            <button class="ghost" type="button" onClick=${runFilter}>${filterState === "idle" ? "Filter" : filterState === "running" ? "Filtering" : "Filtered"}</button>
            ${filterState === "done" && html`<button class="primary" type="button" onClick=${() => setExportOpen(true)}>Export</button>`}
          </div>
          <div class="muted" style=${{ color: "#9af0c8", margin: "8px 0" }}>${filterStatus}</div>
          ${filterState === "done" && html`
            <div class="bar" style=${{ marginTop: "14px", marginBottom: "4px" }}>
              <div class="eyebrow">FILTERED EMAIL TYPES</div>
              <button class="ghost" type="button" onClick=${() => setModalKind("filter")}>View all</button>
            </div>`}
          ${(filterState === "done" ? filterAll : []).slice(0, 10).map((x, i) => html`<${StatRow} key=${i} x=${x} kind="email" onSave=${saveOne} />`)}
        </div>`}

      ${tab === "new" && html`
        <div>
          <div class="row tight">
            <button class=${"ghost" + (nameMode === "paste" ? " on" : "")} type="button" onClick=${() => setNameMode("paste")}>Paste names</button>
            <button class=${"ghost" + (nameMode === "file" ? " on" : "")} type="button" onClick=${() => setNameMode("file")}>Load names file</button>
          </div>
          ${nameMode === "paste"
            ? html`<textarea rows="4" value=${users} placeholder="one name per line" onInput=${(e) => setUsers(e.target.value)} style="margin-top:10px" />`
            : html`<div class="row">
                <input value=${namePath} placeholder="names list file" onInput=${(e) => setNamePath(e.target.value)} />
                <button class="ghost" type="button" onClick=${() => browse("editor-names")}>Browse</button>
              </div>`}
          <label>Email domains</label>
          <input value=${doms} onInput=${(e) => setDoms(e.target.value)} />
          <div class="row tight">
            <button class=${"ghost" + (passMode === "paste" ? " on" : "")} type="button" onClick=${() => setPassMode("paste")}>Paste passwords</button>
            <button class=${"ghost" + (passMode === "file" ? " on" : "")} type="button" onClick=${() => setPassMode("file")}>Load password file</button>
          </div>
          ${passMode === "paste"
            ? html`<textarea rows="3" value=${passes} placeholder="one per line" onInput=${(e) => setPasses(e.target.value)} style="margin-top:10px" />`
            : html`<div class="row">
                <input value=${passPath} placeholder="password list file" onInput=${(e) => setPassPath(e.target.value)} />
                <button class="ghost" type="button" onClick=${() => browse("editor-pass")}>Browse</button>
              </div>`}
          <div class="row">
            <input value=${count} style=${{ maxWidth: "120px" }} onInput=${(e) => setCount(e.target.value)} />
            <button class="primary" type="button" onClick=${generate}>Generate</button>
          </div>
          <label>Generated preview</label>
          <textarea class="mono" rows="7" value=${genOut} onInput=${(e) => setGenOut(e.target.value)} />
          <div class="row">
            <button class="primary" type="button" onClick=${() => setGenExportOpen(true)}>Export generated</button>
            <button class="ghost" type="button" onClick=${saveGenerated}>Save</button>
          </div>
          <${Msg} />
        </div>`}

      ${tab === "sign" && html`
        <div>
          <${Msg} />
          <label>Saved signatures</label>
          <div class="row tight">
            <select style=${{ flex: 1 }} value=${bannerSel} onChange=${(e) => setBannerSel(e.target.value)}>
              <option value="">(none)</option>
              ${banners.map((b, i) => html`<option key=${i} value=${String(i)}>${b.name}</option>`)}
            </select>
            <button class="ghost" type="button" onClick=${loadSig}>Load signature</button>
            <button class="ghost" type="button" onClick=${deleteSig}>Delete</button>
          </div>
          <label>Signature text</label>
          <textarea rows="4" value=${bannerText} placeholder="# captured by @solodolosouls" onInput=${(e) => setBannerText(e.target.value)} />
          <div class="row">
            <input value=${bannerName} placeholder="Name this signature" onInput=${(e) => setBannerName(e.target.value)} />
            <button class="primary" type="button" onClick=${saveSig}>Save signature</button>
          </div>
        </div>`}

      <div class="bar" style="margin-top:20px">
        <div class="btnbar">
          <button class="ghost" type="button" onClick=${() => setView("home")}>Back</button>
          <button class="ghost" onClick=${onHome}>Home</button>
        </div>
      </div>

      <${Modal} show=${modalKind !== null} onClose=${() => setModalKind(null)}>
        <div class="row" style=${{ justifyContent: "space-between" }}>
          <div class="muted">${modalKind === "host" ? "URL / DOMAINS" : modalKind === "filter" ? "FILTERED EMAIL TYPES" : "EMAIL TYPES"}</div>
          <button class="ghost" type="button" onClick=${() => setModalKind(null)}>Close</button>
        </div>
        ${(modalList || []).map((x, i) => html`<${StatRow} key=${i} x=${x} kind=${modalSaveKind} onSave=${saveOne} />`)}
      <//>

      <${Modal} show=${exportOpen} onClose=${() => setExportOpen(false)}>
        <div class="muted">EXPORT FILTERED</div>
        <label>Start signature</label>
        <${SigSelect} value=${sigHead} onChange=${setSigHead} />
        <label>End signature</label>
        <${SigSelect} value=${sigTail} onChange=${setSigTail} />
        <div class="row">
          <button class="primary" type="button" onClick=${exportFiltered}>Save</button>
          <button class="ghost" type="button" onClick=${() => setExportOpen(false)}>Cancel</button>
        </div>
      <//>

      <${Modal} show=${genExportOpen} onClose=${() => setGenExportOpen(false)}>
        <div class="muted">EXPORT GENERATED</div>
        <label>File name</label>
        <div class="row tight">
          <input value=${savePathGen} placeholder="combos-export.txt" onInput=${(e) => setSavePathGen(e.target.value)} />
          <button class="ghost" type="button" onClick=${async () => {
            const r = await api("/api/pick-save");
            if (r.data && r.data.path) setSavePathGen(r.data.path);
          }}>Browse</button>
        </div>
        <label>Start signature</label>
        <${SigSelect} value=${sigHeadGen} onChange=${setSigHeadGen} />
        <label>End signature</label>
        <${SigSelect} value=${sigTailGen} onChange=${setSigTailGen} />
        <div class="row">
          <button class="primary" type="button" onClick=${exportGenerated}>Save</button>
          <button class="ghost" type="button" onClick=${() => setGenExportOpen(false)}>Cancel</button>
        </div>
      <//>
    </div>`;
}
