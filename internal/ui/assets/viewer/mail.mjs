// Valid Viewer: mailbox client (folders, senders, list, reader, proxies, deletes).
// Faithful port of the original viewer mail logic; same endpoints and behaviors.
import { html, useState, useEffect, useRef, api, apiJSON, Tag } from "../app.mjs";

export function formatMailDate(raw) {
  if (!raw) return "";
  const s = String(raw).replace(/(\.\d{3})\d+Z$/, "$1Z");
  const d = new Date(s);
  if (isNaN(d.getTime())) return String(raw);
  const months = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
  const h = d.getHours(), m = d.getMinutes();
  const am = h < 12 ? "AM" : "PM";
  let h12 = h % 12; if (!h12) h12 = 12;
  const mm = m < 10 ? "0" + m : String(m);
  return months[d.getMonth()] + " " + d.getDate() + ", at " + h12 + ":" + mm + " " + am;
}

export function displayFromEmail(raw) {
  raw = String(raw || "").trim();
  const m = raw.match(/[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}/i);
  return m ? m[0] : raw;
}

export function senderEmailOf(from) {
  const raw = String(from || "");
  const em = raw.match(/[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}/i);
  return em ? em[0].toLowerCase() : raw.toLowerCase();
}

export function senderKey(m) {
  const raw = String((m && m.from) || "").trim();
  const em = raw.match(/[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}/i);
  if (em) return em[0].toLowerCase();
  return raw.toLowerCase() || "unknown";
}

export function mailKey(m) {
  if (!m) return "";
  if (m.id) return "id:" + m.id;
  return String(m.subject || "") + "|" + String(m.from || "") + "|" + String(m.date || "");
}

export function wrapMailHTML(htmlBody, preview) {
  let raw = htmlBody || "";
  if (raw && /<html/i.test(raw)) {
    if (!/<meta[^>]+charset/i.test(raw)) raw = raw.replace(/<head[^>]*>/i, (m) => m + '<meta charset="utf-8">');
    if (!/<base /i.test(raw)) raw = raw.replace(/<head[^>]*>/i, (m) => m + '<base target="_blank">');
    return raw;
  }
  const body = raw && raw.indexOf("<") >= 0
    ? raw
    : '<pre style="white-space:pre-wrap;font:14px/1.55 Segoe UI,Arial,sans-serif;margin:0">' +
      String(preview || "").replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c])) + "</pre>";
  return '<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><base target="_blank"><style>img,video,iframe{max-width:100%;height:auto}table{max-width:100%}</style></head><body style="margin:0;padding:16px;background:#fff">' + body + "</body></html>";
}

const AV_COLORS = ["#3b82f6", "#22c55e", "#a855f7", "#f59e0b", "#ef4444", "#06b6d4", "#84cc16", "#f97316"];

export function Avatar({ from, big }) {
  const key = senderEmailOf(from) || "?";
  const letter = ((key.replace(/^[^a-z0-9]+/i, "")[0]) || "?").toUpperCase();
  const domain = key.indexOf("@") >= 0 ? key.split("@")[1] : "";
  let n = 0;
  for (let i = 0; i < key.length; i++) n = (n + key.charCodeAt(i)) % AV_COLORS.length;
  const src = domain ? "https://www.google.com/s2/favicons?sz=128&domain_url=" + encodeURIComponent("https://" + domain) : "";
  return html`
    <span class=${"mail-av" + (big ? " mail-av-lg" : "")} style=${{ background: AV_COLORS[n], color: "#fff", fontWeight: 700, fontSize: big ? "16px" : "13px" }}>
      ${src && html`<img src=${src} alt="" onLoad=${(e) => {
        if (e.currentTarget.naturalWidth <= 16) e.currentTarget.style.display = "none";
        else if (e.currentTarget.nextSibling) e.currentTarget.nextSibling.style.display = "none";
      }} onError=${(e) => { e.currentTarget.style.display = "none"; }} />`}
      <span class="mail-av-letter">${letter}</span>
    </span>`;
}

const FOLDERS = [["inbox", "Inbox"], ["sent", "Sent"], ["spam", "Spam"], ["deleted", "Deleted"]];

export function MailViewer({ account, onClose }) {
  const [stage, setStage] = useState("ask"); // ask | px | main
  // proxy choice ({proxy_mode, proxy, proxy_urls} or null) lives on R.current
  // (below), NOT in state: the original read window._mailProxy fresh at call
  // time, so the load issued right after picking proxies must see it too.

  const [mails, setMails] = useState([]);
  const [packs, setPacks] = useState({});
  const [fromFilter, setFromFilter] = useState("");
  const [folder, setFolder] = useState("inbox");
  const [query, setQuery] = useState("");
  const [appliedQuery, setAppliedQuery] = useState("kind:email");
  const [total, setTotal] = useState(0);
  const [more, setMore] = useState(false);
  const [sel, setSel] = useState(-1);
  const [picked, setPicked] = useState({});
  const [selAll, setSelAll] = useState(false);

  const [pxMode, setPxMode] = useState("file");
  const [pxPath, setPxPath] = useState("");
  const [pxUrls, setPxUrls] = useState([""]);
  const [pxFileCount, setPxFileCount] = useState("0 proxies");
  const [pxCount, setPxCount] = useState("0 proxies");
  const [pxLists, setPxLists] = useState([]);
  const [pxMenu, setPxMenu] = useState(""); // file | server | ""

  const [listErr, setListErr] = useState("");
  const [delAsk, setDelAsk] = useState(null); // {ids, purge, text, err, busy}
  const [readHtml, setReadHtml] = useState(null);
  const frameRef = useRef(null);

  // refs mirror the state the async mail flows depend on (the original code
  // read these straight off window.* globals, always fresh).
  const R = useRef({ proxy: null });
  const put = (patch) => {
    R.current = { ...R.current, ...patch };
    if ("mails" in patch) setMails(patch.mails);
    if ("packs" in patch) setPacks(patch.packs);
    if ("fromFilter" in patch) setFromFilter(patch.fromFilter);
    if ("folder" in patch) setFolder(patch.folder);
    if ("appliedQuery" in patch) setAppliedQuery(patch.appliedQuery);
    if ("total" in patch) setTotal(patch.total);
    if ("more" in patch) setMore(patch.more);
    if ("picked" in patch) setPicked(patch.picked);
    if ("sel" in patch) setSel(patch.sel);
    if ("listErr" in patch) setListErr(patch.listErr);
  };

  const pack = fromFilter ? (packs[fromFilter] || { mails: [], total: 0, more: true, ready: false }) : null;
  const activeList = fromFilter ? (pack ? pack.mails : []) : mails;

  const reset = () => {
    R.current.proxy = null;
    put({
      mails: [], packs: {}, fromFilter: "", folder: "inbox",
      appliedQuery: "kind:email", total: 0, more: false, picked: {},
      sel: -1, listErr: "",
    });
    setQuery(""); setSelAll(false);
    setPxPath(""); setPxUrls([""]); setPxFileCount("0 proxies"); setPxCount("0 proxies");
    setPxMode("file"); setPxMenu(""); setReadHtml(null);
  };

  /* ---- opening flow: probe first, else ask about proxies ---- */
  useEffect(() => {
    if (!account) return;
    reset();
    setStage("ask");
    (async () => {
      try {
        const { data } = await apiJSON("/api/mail/probe", { email: account.email });
        if (data && data.ok) {
          put({ mails: data.messages || [], total: data.total || 0 });
          setMore(!!data.more || ((data.total || 0) > (data.messages || []).length));
          setStage("main");
          return;
        }
      } catch (e) {}
      setStage("ask");
    })();
  }, [account && account.email]);

  const activeListOf = () => {
    const f = R.current.fromFilter || "";
    if (!f) return R.current.mails || [];
    const p = (R.current.packs || {})[f];
    return (p && p.mails) ? p.mails : [];
  };

  const loadPage = async (moreReq, opts) => {
    if (!account) return;
    const f = (opts && opts.fromFilter !== undefined) ? opts.fromFilter : (R.current.fromFilter || "");
    if (f) return loadSenderPage(f, moreReq);
    const q = R.current.appliedQuery || "kind:email";
    const from = moreReq ? (R.current.mails || []).length : 0;
    if (!moreReq) put({ mails: [], sel: -1, listErr: "" });
    const body = Object.assign(
      { email: account.email, password: account.password, from, size: 100, query: q, folder: R.current.folder || "inbox" },
      R.current.proxy || {}
    );
    try {
      const { ok, data } = await apiJSON("/api/mail/list", body);
      if (!ok) { setListErr((data && data.error) || "Could not load mail"); return; }
      const seen = {};
      (R.current.mails || []).forEach((m) => { seen[mailKey(m)] = true; });
      const added = [];
      (data.messages || []).forEach((m) => {
        const k = mailKey(m);
        if (seen[k]) return;
        seen[k] = true;
        added.push(m);
      });
      const base = moreReq ? (R.current.mails || []) : [];
      const next = base.concat(added);
      put({ mails: next });
      if (data.total) put({ total: data.total });
      let skipExtra = 0;
      if (moreReq && added.length === 0) skipExtra = 100;
      const loaded = (moreReq ? base.length : 0) + added.length + skipExtra;
      setMore(added.length > 0 && (!!data.more || (data.total && loaded < data.total) || added.length >= 10));
      if (!moreReq && added.length) put({ sel: 0 });
    } catch (e) { setListErr(String(e && e.message ? e.message : e)); }
  };

  const loadSenderPage = async (from, moreReq) => {
    if (!account || !from) return;
    const cur = R.current.packs || {};
    let p = cur[from] || { mails: [], total: 0, more: true, ready: false };
    if (!moreReq) {
      const seed = (R.current.mails || []).filter((m) => senderKey(m) === from);
      p = { mails: seed.slice(), total: 0, more: true, ready: false };
    }
    const off = moreReq ? (p.mails.length + (p._skip || 0)) : 0;
    const body = Object.assign(
      { email: account.email, password: account.password, from: off, size: 100, query: "from:" + from + " kind:email", folder: R.current.folder || "inbox" },
      R.current.proxy || {}
    );
    try {
      const { ok, data } = await apiJSON("/api/mail/list", body);
      if (!ok) {
        put({ packs: { ...cur, [from]: { ...p, ready: true, more: false } } });
        return;
      }
      const seen = {};
      p.mails.forEach((m) => { seen[mailKey(m)] = true; });
      let addedN = 0;
      const newMails = p.mails.slice();
      (data.messages || []).forEach((m) => {
        const k = mailKey(m);
        if (seen[k]) return;
        seen[k] = true;
        newMails.push(m);
        addedN++;
      });
      put({
        packs: {
          ...cur,
          [from]: {
            mails: newMails,
            total: data.total || p.total,
            ready: true,
            _skip: (moreReq && addedN === 0) ? ((p._skip || 0) + 100) : (p._skip || 0),
            more: (data.total && newMails.length < data.total) || (!data.total && (data.more || addedN >= 10)),
          },
        },
      });
      if (!moreReq && R.current.fromFilter === from && newMails.length) showMail(0);
    } catch (e) {
      put({ packs: { ...cur, [from]: { ...p, ready: true, more: false } } });
      setListErr(String(e && e.message ? e.message : e));
    }
  };

  /* ---- visible rows (client-side search over loaded mail) ---- */
  const q = query.trim().toLowerCase();
  const rows = activeList
    .map((m, i) => ({ m, i }))
    .filter((x) => {
      if (!q || q === "*") return true;
      const t = ((x.m.subject || "") + " " + (x.m.from || "") + " " + (x.m.preview || "")).toLowerCase();
      return t.indexOf(q) >= 0;
    });

  const senderCounts = {};
  mails.forEach((m) => {
    const k = senderKey(m);
    senderCounts[k] = (senderCounts[k] || 0) + 1;
  });
  const senderKeys = Object.keys(senderCounts).sort((a, b) => senderCounts[b] - senderCounts[a]);

  /* ---- reading ---- */
  const showMail = (i) => {
    const m = activeList[i];
    if (!m) return;
    put({ sel: i, listErr: "" });
    setReadHtml(wrapMailHTML(m.html || "", m.preview || ""));
    if (m.id && !m._full) {
      apiJSON("/api/mail/body", { email: (account && account.email) || "", id: m.id }).then(({ data }) => {
        if (!data || !data.ok || !data.html) return;
        if (R.current.sel !== i) return; // user moved on — don't overwrite the pane
        m.html = data.html;
        m._full = true;
        setReadHtml(wrapMailHTML(m.html, m.preview || ""));
      }).catch(() => {});
    }
  };

  const onFrameLoad = () => {
    try {
      const d = frameRef.current && frameRef.current.contentDocument;
      if (d && d.body && frameRef.current) {
        frameRef.current.style.height = Math.max(520, d.body.scrollHeight + 32) + "px";
      }
    } catch (e) {}
  };

  /* ---- deletes ---- */
  const removeEverywhere = (m) => {
    const k = mailKey(m);
    put({ mails: (R.current.mails || []).filter((x) => mailKey(x) !== k) });
    const cur = R.current.packs || {};
    const out = {};
    Object.keys(cur).forEach((s) => {
      const pk = cur[s];
      if (!pk || !pk.mails) return;
      out[s] = { ...pk, mails: pk.mails.filter((x) => mailKey(x) !== k), total: pk.total ? Math.max(0, pk.total - 1) : pk.total };
    });
    put({ packs: out });
  };

  const askDelete = (ids, text, err) => {
    setDelAsk({ ids, purge: (R.current.folder || "inbox") === "deleted", text, err: err || "", busy: false });
  };

  const deleteMailAt = (i) => {
    const m = activeList[i];
    if (!m) return;
    if (!m.id) {
      askDelete([], "Are you sure you want to delete this email?", "This message has no Outlook id, so it cannot be deleted.");
      return;
    }
    askDelete([m.id], "Are you sure you want to delete this email?", "");
  };

  const deletePicked = () => {
    const ids = Object.keys(picked || {}).filter(Boolean);
    if (!ids.length) return;
    askDelete(ids, "Are you sure you want to delete these emails?", "");
  };

  const confirmDelete = async () => {
    const d = delAsk;
    if (!d || !d.ids || !d.ids.length) return;
    setDelAsk({ ...d, busy: true, err: "" });
    try {
      const { ok, data } = await apiJSON("/api/mail/delete", {
        email: (account && account.email) || "", ids: d.ids, purge: !!d.purge,
      });
      if (!ok) {
        setDelAsk({ ...d, busy: false, err: (data && data.error) || "Could not delete" });
        return;
      }
      const set = {};
      d.ids.forEach((id) => { set[id] = true; });
      activeListOf().slice().forEach((m) => { if (m && set[m.id]) removeEverywhere(m); });
      if (R.current.total) put({ total: Math.max(0, R.current.total - d.ids.length) });
      put({ picked: {}, sel: -1 });
      setSelAll(false);
      setDelAsk(null);
      setReadHtml(null);
    } catch (e) {
      setDelAsk({ ...d, busy: false, err: String(e) });
    }
  };

  const emptyDeleted = async () => {
    put({ folder: "deleted" });
    for (let n = 0; n < 30; n++) {
      const ids = activeListOf().map((m) => m && m.id).filter(Boolean);
      if (!ids.length) {
        if (!R.current.more) break;
        await loadPage(true);
        continue;
      }
      const { ok } = await apiJSON("/api/mail/delete", { email: (account && account.email) || "", ids, purge: true });
      if (!ok) break;
      const set = {};
      ids.forEach((id) => { set[id] = true; });
      activeListOf().slice().forEach((m) => { if (m && set[m.id]) removeEverywhere(m); });
      put({ picked: {} });
      if (R.current.more) await loadPage(true);
    }
    setReadHtml(null);
  };

  /* ---- proxy source modal helpers ---- */
  const loadPxLists = async () => {
    try {
      const { data } = await api("/api/proxy-lists");
      setPxLists(data || []);
    } catch (e) { setPxLists([]); }
  };
  const pxRows = (kind) => pxLists.filter((x) => (kind === "server" ? x.type === "server" : x.type !== "server"));
  const pxCheckFile = async () => {
    setPxFileCount("checking…");
    const { ok, data } = await apiJSON("/api/proxy-fetch", { path: pxPath || "" });
    setPxFileCount(ok ? ((data && data.count) || 0) + " proxies" : "failed");
  };
  const pxCheckServer = async () => {
    setPxCount("checking…");
    const urls = pxUrls.map((s) => s.trim()).filter(Boolean);
    const { ok, data } = await apiJSON("/api/proxy-fetch", { urls });
    setPxCount(ok ? ((data && data.count) || 0) + " proxies" : "failed");
  };
  const pxUseSaved = (x) => {
    if (pxMode === "server") {
      setPxUrls((x.urls && x.urls.length) ? x.urls.slice() : [""]);
      setPxCount("0 proxies");
    } else {
      setPxPath(x.path || "");
      setPxFileCount("0 proxies");
    }
    setPxMenu("");
  };
  const pxBrowse = async () => {
    const { data } = await api("/api/pick?kind=proxy");
    if (data && data.path) setPxPath(data.path);
  };
  const startMail = (usePx) => {
    if (usePx) { setStage("px"); loadPxLists(); return; }
    R.current.proxy = null;
    setStage("main");
    loadPage(false);
  };
  const pxGo = () => {
    // Write the ref BEFORE loading: loadPage below runs in this same tick and
    // must see the chosen proxies (the original relied on the window.* global).
    R.current.proxy = {
      proxy_mode: pxMode || "file",
      proxy: pxPath || "",
      proxy_urls: pxUrls.map((s) => s.trim()).filter(Boolean),
    };
    setStage("main");
    loadPage(false);
  };

  if (!account) return null;
  const email = account.email || "";

  return html`
    <div class="modal" style=${{ zIndex: 50 }}>
      ${stage === "main" && html`
        <div class="mail-wrap">
          <div class="mail-head">
            <div>
              <div class="muted">EMAIL VIEWER</div>
              <b>${email}</b>
            </div>
            <button class="secondary" type="button" onClick=${onClose}>Close</button>
          </div>
          <div class="mail-search">
            <input placeholder="Search mailbox" value=${query} onInput=${(e) => setQuery(e.target.value)}
              onKeyDown=${(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  const nq = (e.target.value || "").trim() || "kind:email";
                  put({ appliedQuery: nq });
                  loadPage(false);
                }
              }} />
            <button class="secondary" type="button" onClick=${() => {
              const nq = q || "kind:email";
              put({ appliedQuery: nq });
              loadPage(false);
            }}>Search</button>
          </div>
          <div class="mail-folders">
            ${FOLDERS.map(([id, label]) => html`
              <button type="button" class=${"mail-folder" + (folder === id ? " on" : "")}
                onClick=${() => {
                  put({
                    folder: id, fromFilter: "", appliedQuery: "kind:email",
                    packs: {}, picked: {}, mails: [], total: 0,
                  });
                  setQuery(""); setMore(false); put({ sel: -1 }); setSelAll(false);
                  setTimeout(() => loadPage(false, { fromFilter: "" }), 0);
                }}>${label}</button>`)}
          </div>
          <div class="mail-toolbar">
            <label class="mail-check">
              <input type="checkbox" checked=${selAll} onClick=${(e) => {
                const on = e.target.checked;
                setSelAll(on);
                const p = {};
                if (on) activeList.forEach((m) => { if (m && m.id) p[m.id] = true; });
                put({ picked: p });
              }} /> Select all
            </label>
            <button class="secondary" type="button" onClick=${deletePicked}>Delete selected</button>
            ${folder === "deleted" && html`<button class="secondary danger" type="button" onClick=${emptyDeleted}>Empty deleted</button>`}
            <span class="muted">${Object.keys(picked || {}).length ? Object.keys(picked).length + " selected" : ""}</span>
          </div>
          <div class="mail-body">
            <div class="mail-list">
              <div class=${"mail-item" + (!fromFilter ? " on" : "")} onClick=${() => { put({ fromFilter: "" }); put({ sel: -1 }); }}>
                <div class="mail-item-body">
                  <b>All senders</b>
                  <div class="mail-from">
                    <span class="mail-chip tag-green">Total messages: ${total || mails.length}</span>
                    <span class="mail-chip tag-green">${senderKeys.length} senders</span>
                  </div>
                </div>
              </div>
              ${senderKeys.map((k) => {
                const cnt = (packs[k] && packs[k].total) ? packs[k].total : senderCounts[k];
                return html`
                  <div class=${"mail-item" + (fromFilter === k ? " on" : "")} key=${k} onClick=${() => { put({ fromFilter: k }); put({ sel: -1 }); if (!packs[k]) loadSenderPage(k, false); }}>
                    <${Avatar} from=${k} />
                    <div class="mail-item-body">
                      <b>${k}</b>
                      <div class="mail-from"><span class="mail-chip tag-green">${cnt}</span></div>
                    </div>
                  </div>`;
              })}
            </div>
            <div class="mail-list">
              ${listErr && rows.length > 0 && html`
                <div class="muted" style=${{ padding: "10px 14px", color: "var(--red)" }}>${listErr}</div>`}
              ${!rows.length && html`
                ${listErr
                  ? html`<div class="muted" style=${{ padding: "14px", color: "var(--red)" }}>${listErr}</div>`
                  : html`<div class="muted" style="padding:14px">No messages</div>`}
                ${((fromFilter && pack && pack.more) || (!fromFilter && more)) && html`
                  <div style="padding:8px 0 4px"><button class="secondary" type="button" onClick=${() => loadPage(true)}>Load more</button></div>`}
              `}
              ${rows.map((x) => html`
                <div class=${"mail-item" + (x.i === sel ? " on" : "")} key=${x.m.id || x.i} onClick=${() => showMail(x.i)}>
                  <input class="mail-pick" type="checkbox" checked=${!!(picked && x.m.id && picked[x.m.id])}
                    onClick=${(e) => e.stopPropagation()}
                    onChange=${(e) => {
                      const id = x.m.id || "";
                      const p = { ...(picked || {}) };
                      if (e.target.checked) p[id] = true; else delete p[id];
                      put({ picked: p });
                    }} />
                  <${Avatar} from=${x.m.from} />
                  <div class="mail-item-body">
                    <div style=${{ display: "flex", justifyContent: "space-between", gap: "8px", alignItems: "flex-start" }}>
                      <b>${x.m.subject || "(no subject)"}</b>
                      <button type="button" class="mail-del-mini" title="Delete"
                        onClick=${(e) => { e.stopPropagation(); deleteMailAt(x.i); }}>✕</button>
                    </div>
                    <div class="mail-from">
                      ${x.m.from && html`<span class="mail-chip tag-yellow">${displayFromEmail(x.m.from)}</span>`}
                      ${x.m.date && html`<span class="mail-chip tag-blue">${formatMailDate(x.m.date)}</span>`}
                    </div>
                    <div class="muted" style=${{ marginTop: "6px" }}>${String(x.m.preview || "").slice(0, 110)}</div>
                  </div>
                </div>`)}
              ${rows.length > 0 && ((fromFilter && pack && pack.more) || (!fromFilter && more)) && html`
                <div style="padding:8px 0 4px">
                  <button class="secondary" type="button" onClick=${() => loadPage(true)}>
                    Load more${(fromFilter && pack && pack.total) ? (" · " + pack.mails.length + " / " + pack.total) : (!fromFilter && total) ? (" · " + mails.length + " / " + total) : ""}
                  </button>
                </div>`}
            </div>
            <div class="mail-read">
              ${sel >= 0 && activeList[sel] ? html`
                <div class="mail-read-card">
                  <div style=${{ display: "flex", justifyContent: "space-between", gap: "10px", alignItems: "flex-start", flexWrap: "wrap" }}>
                    <div style=${{ display: "flex", gap: "12px", alignItems: "flex-start", flex: 1, minWidth: 0 }}>
                      <${Avatar} from=${activeList[sel].from} big=${true} />
                      <div style=${{ minWidth: 0 }}>
                        <h2 style=${{ margin: "0" }}>${activeList[sel].subject || "(no subject)"}</h2>
                        <div class="mail-from" style=${{ marginTop: "8px" }}>
                          ${activeList[sel].from && html`<span class="mail-chip tag-yellow">${displayFromEmail(activeList[sel].from)}</span>`}
                          ${activeList[sel].date && html`<span class="mail-chip tag-blue">${formatMailDate(activeList[sel].date)}</span>`}
                        </div>
                      </div>
                    </div>
                    <button type="button" class="secondary danger" onClick=${() => deleteMailAt(sel)}>Delete</button>
                  </div>
                  <iframe class="mail-frame" ref=${frameRef} srcDoc=${readHtml || ""} onLoad=${onFrameLoad}
                    sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-presentation allow-forms"></iframe>
                </div>`
              : html`<div class="muted">Select a message</div>`}
            </div>
          </div>
        </div>`}

      ${stage === "ask" && html`
        <div class="mail-wrap" style=${{ height: "auto", width: "min(420px, 100%)" }}>
          <div class="mail-head">
            <div><div class="muted">EMAIL VIEWER</div><b>Use proxies?</b></div>
            <button class="secondary" type="button" onClick=${onClose}>Close</button>
          </div>
          <div style="padding:18px">
            <p class="muted" style="margin:0">Use proxies for this mailbox login?</p>
            <div style=${{ display: "flex", gap: "10px", marginTop: "14px" }}>
              <button type="button" onClick=${() => startMail(false)}>No</button>
              <button class="secondary" type="button" onClick=${() => startMail(true)}>Yes</button>
            </div>
          </div>
        </div>`}

      ${stage === "px" && html`
        <div class="mail-wrap" style=${{ height: "auto", width: "min(520px, 100%)" }}>
          <div class="mail-head">
            <div><div class="muted">PROXY SOURCE</div><b>Choose list</b></div>
            <button class="secondary" type="button" onClick=${onClose}>Close</button>
          </div>
          <div style="padding:16px">
            <div style=${{ display: "flex", gap: "8px", marginBottom: "12px" }}>
              <button class=${"secondary" + (pxMode === "file" ? " on" : "")} type="button" onClick=${() => setPxMode("file")}>File</button>
              <button class=${"secondary" + (pxMode === "server" ? " on" : "")} type="button" onClick=${() => setPxMode("server")}>Server</button>
            </div>
            ${pxMode === "file" ? html`
              <div>
                <div style=${{ display: "flex", gap: "8px", flexWrap: "wrap", alignItems: "center" }}>
                  <input placeholder="proxy file" style=${{ minWidth: "180px", flex: 1 }} value=${pxPath} onInput=${(e) => setPxPath(e.target.value)} />
                  <button class="secondary" type="button" onClick=${pxBrowse}>Browse</button>
                  <button class="secondary" type="button" onClick=${pxCheckFile}>Check</button>
                  <${Tag}>${pxFileCount}<//>
                </div>
                <div style=${{ marginTop: "10px" }}>
                  <button class="secondary" type="button" onClick=${() => { setPxMenu(pxMenu === "file" ? "" : "file"); loadPxLists(); }}>Saved lists</button>
                  ${pxMenu === "file" && html`
                    <div class="menu on" style=${{ position: "static", marginTop: "8px", maxHeight: "180px", width: "100%", left: "auto", top: "auto", display: "block" }}>
                      ${!pxRows("file").length && html`<div class="muted" style="padding:8px 10px">Nothing saved</div>`}
                      ${pxRows("file").map((x) => html`
                        <div key=${(x.name || "") + (x.path || "")} onClick=${() => pxUseSaved(x)}>
                          <b>${x.name || "list"}</b>
                          <${Tag}>${(x.count || 0) + " proxies · " + (x.path || "")}<//>
                        </div>`)}
                    </div>`}
                </div>
              </div>`
            : html`
              <div>
                ${pxUrls.map((u, i) => html`
                  <div class="row tight" key=${i}>
                    <input placeholder="https://list/proxies.txt" value=${u} onInput=${(e) => setPxUrls(pxUrls.map((v, j) => (j === i ? e.target.value : v)))} />
                  </div>`)}
                <div style=${{ display: "flex", gap: "8px", marginTop: "8px", alignItems: "center", flexWrap: "wrap" }}>
                  <button class="secondary" type="button" onClick=${() => setPxUrls(pxUrls.concat([""]))}>+</button>
                  ${pxUrls.length > 1 && html`<button class="secondary" type="button" onClick=${() => setPxUrls(pxUrls.slice(0, -1))}>–</button>`}
                  <button class="secondary" type="button" onClick=${pxCheckServer}>Check</button>
                  <${Tag}>${pxCount}<//>
                </div>
                <div style=${{ width: "100%", marginTop: "10px" }}>
                  <button class="secondary" type="button" onClick=${() => { setPxMenu(pxMenu === "server" ? "" : "server"); loadPxLists(); }}>Saved servers</button>
                  ${pxMenu === "server" && html`
                    <div class="menu on" style=${{ position: "static", marginTop: "8px", maxHeight: "180px", width: "100%", left: "auto", top: "auto", display: "block" }}>
                      ${!pxRows("server").length && html`<div class="muted" style="padding:8px 10px">Nothing saved</div>`}
                      ${pxRows("server").map((x) => html`
                        <div key=${x.name || ""} onClick=${() => pxUseSaved(x)}>
                          <b>${x.name || "list"}</b>
                          <${Tag}>${((x.urls && x.urls.length) || 0) + " urls · " + (x.count || 0) + " proxies"}<//>
                        </div>`)}
                    </div>`}
                </div>
              </div>`}
            <div style=${{ display: "flex", gap: "8px", marginTop: "14px" }}>
              <button type="button" onClick=${pxGo}>Continue</button>
              <button class="secondary" type="button" onClick=${() => setStage("ask")}>Cancel</button>
            </div>
          </div>
        </div>`}

      ${delAsk && html`
        <div class="modal" style=${{ zIndex: 80, padding: "18px" }}>
          <div class="modal-card" style=${{ width: "min(460px, 100%)" }}>
            <div class="row tight" style=${{ justifyContent: "space-between" }}>
              <div><div class="muted">DELETE</div><b>Are you sure?</b></div>
              <button class="secondary" type="button" onClick=${() => setDelAsk(null)}>Close</button>
            </div>
            <div style="padding:18px 0 0">
              <p class="muted" style="margin:0">${delAsk.text}</p>
              <div style=${{ display: "flex", gap: "10px", marginTop: "16px" }}>
                <button type="button" class="danger" disabled=${delAsk.busy || !delAsk.ids.length} onClick=${confirmDelete}>
                  ${delAsk.busy ? "Deleting…" : "Yes"}
                </button>
                <button class="secondary" type="button" onClick=${() => setDelAsk(null)}>No</button>
              </div>
              ${delAsk.err && html`<div class="muted" style=${{ marginTop: "10px", color: "var(--red)" }}>${delAsk.err}</div>`}
            </div>
          </div>
        </div>`}
    </div>
`;
}
