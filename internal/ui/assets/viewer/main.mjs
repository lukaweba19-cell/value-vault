// Valid Viewer entry: accounts browser + mailbox viewer, Preact port.
// Same endpoints and behaviors as the original HTML UI.
import { html, useState, useEffect, useRef, api, copyText } from "../app.mjs";
import { MailViewer, formatMailDate } from "./mail.mjs";

const fmtBytes = (n) => {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(2) + " MB";
};

const tagClass = (t) => {
  const x = String(t || "").toLowerCase();
  if (x.includes("ultimate") || x.includes("game pass") || x.includes("promo") || x.includes("discord")) return "green";
  if (x.includes("no payment") || x.includes("2fa")) return "warn";
  return "";
};

const PREFERRED = ["Active", "Country", "Services", "Active Cards", "Removed Cards", "Expires", "Fortnite", "Specials", "Promos", "Claimed Promos", "Inbox", "Keywords", "Level", "Account Level", "V-Bucks", "Display", "Notes"];

function fieldRows(fields) {
  const keys = Object.keys(fields || {});
  keys.sort((a, b) => {
    const ia = PREFERRED.findIndex((p) => p.toLowerCase() === a.toLowerCase());
    const ib = PREFERRED.findIndex((p) => p.toLowerCase() === b.toLowerCase());
    if (ia === -1 && ib === -1) return a.localeCompare(b);
    if (ia === -1) return 1;
    if (ib === -1) return -1;
    return ia - ib;
  });
  return keys
    .filter((k) => fields[k])
    .map((k) => html`
      <div class="kv-row" key=${k}>
        <div class="k">${k}</div>
        <div class="v">${String(fields[k])}</div>
      </div>`);
}

function useToast() {
  const [msg, setMsg] = useState("");
  const timer = useRef(null);
  const toast = (m) => {
    setMsg(m);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setMsg(""), 1800);
  };
  return [msg, toast];
}

function AccountCard({ a, onMail, toast }) {
  const doCopy = (kind) => {
    let text = "";
    if (kind === "email") text = a.email || "";
    else if (kind === "combo") text = (a.email || "") + ":" + (a.password || "");
    else text = a.raw || "";
    copyText(text).then((ok) => toast(ok ? "Copied" : "Copy failed"));
  };
  return html`
    <article class="card">
      <div class="card-top">
        <div class="email">${a.email || "(no email)"}</div>
        <div class="line-no">#${a.id} · L${a.line}</div>
      </div>
      <div class="pass"><code>${a.password || "—"}</code></div>
      <div class="tag-row">
        ${(a.tags || []).map((t) => html`<span class=${"tag " + tagClass(t)} key=${t}>${t}</span>`)}
      </div>
      <div class="kv">${fieldRows(a.fields)}</div>
      <div class="actions">
        <button type="button" onClick=${() => doCopy("email")}>Copy email</button>
        <button type="button" onClick=${() => doCopy("combo")}>Copy combo</button>
        <button type="button" onClick=${() => doCopy("raw")}>Copy line</button>
        <button type="button" onClick=${() => onMail(a)}>Email Viewer</button>
      </div>
    </article>`;
}

export function App() {
  const [stats, setStats] = useState(null);
  const [filters, setFilters] = useState({ q: "", tag: "", country: "", sort: "id" });
  const [accounts, setAccounts] = useState([]);
  const [count, setCount] = useState(0);
  const [page, setPage] = useState(1);
  const [pages, setPages] = useState(1);
  const [mailAcc, setMailAcc] = useState(null);
  const [toastMsg, toast] = useToast();

  const loadStats = async () => {
    try {
      const { data } = await api("/api/stats");
      setStats(data || {});
    } catch (e) {}
  };

  const loadAccounts = async (p) => {
    const f = p || filters;
    const params = new URLSearchParams({
      q: f.q, tag: f.tag, country: f.country, sort: f.sort,
      page: String(p ? page : 1), limit: "24",
    });
    try {
      const { data } = await api("/api/accounts?" + params.toString());
      const list = (data && data.accounts) || [];
      setAccounts(list);
      setCount((data && data.count) != null ? data.count : list.length);
      const pg = (data && data.page) || 1;
      setPage(pg);
      setPages((data && data.pages) || Math.max(1, Math.ceil(((data && data.count) || 0) / 24)));
    } catch (e) {}
  };

  useEffect(() => { loadStats(); loadAccounts(); }, []);

  // live reload (same as the original EventSource hook)
  useEffect(() => {
    let t = null;
    let es;
    try {
      es = new EventSource("/api/events");
      es.onmessage = () => {
        if (mailAcc) return;
        if (t) clearTimeout(t);
        t = setTimeout(() => { loadStats().then(loadAccounts); }, 2500);
      };
    } catch (e) {}
    return () => { try { es && es.close(); } catch (e) {} if (t) clearTimeout(t); };
  }, [mailAcc, filters]);

  const setF = (patch) => setFilters((f) => ({ ...f, ...patch }));
  const search = () => { setPage(1); loadAccounts({ ...filters }); };
  const clear = () => {
    const f = { q: "", tag: "", country: "", sort: "id" };
    setFilters(f);
    setPage(1);
    loadAccounts(f);
    loadStats();
  };
  const gotoPage = (p) => {
    const np = Math.min(Math.max(1, p), pages);
    setPage(np);
    loadAccounts({ ...filters });
  };

  const s = stats || {};
  const tagKeys = Object.keys(s.tag_counts || {}).sort((a, b) => s.tag_counts[b] - s.tag_counts[a]);
  const countries = Object.keys(s.countries || {}).sort();
  const pinned = ["Has Payment", "No Payment", "PayPal", "Fortnite", "Promos", "Private", "Free"];
  const shown = [];
  pinned.forEach((t) => { if (s.tag_counts && s.tag_counts[t]) shown.push(t); });
  tagKeys.forEach((t) => { if (shown.indexOf(t) < 0) shown.push(t); });

  const reloadFile = async () => {
    const { ok } = await api("/api/reload", { method: "POST" });
    if (!ok) { toast("Reload failed"); return; }
    toast("File reloaded");
    await loadStats();
    await loadAccounts({ ...filters });
  };

  const copyAll = async () => {
    const params = new URLSearchParams({ q: filters.q, tag: filters.tag, country: filters.country, sort: filters.sort });
    const { data } = await api("/api/accounts?" + params.toString());
    const emails = ((data && data.accounts) || []).map((a) => a.email).filter(Boolean).join("\n");
    copyText(emails).then((ok) => toast(ok ? "Copied " + ((data && data.count) || 0) + " emails" : "Copy failed"));
  };

  return html`
    <header>
      <div class="wrap">
        <div class="title-row">
          <div>
            <h1>Valid Viewer</h1>
            <div class="sub">${(s.file_path || "valid.txt") + " · modified " + (s.modified || "—") + " · loaded " + (s.loaded_at || "—")}</div>
          </div>
          <div style=${{ display: "flex", gap: "8px", flexWrap: "wrap" }}>
            <button class="secondary" type="button" onClick=${reloadFile}>Reload file</button>
            <button class="secondary" type="button" onClick=${copyAll}>Copy filtered emails</button>
          </div>
        </div>
        <div class="controls">
          <input type="search" placeholder="Search email, password, country, services, promos…"
            value=${filters.q}
            onInput=${(e) => setF({ q: e.target.value })}
            onKeyDown=${(e) => { if (e.key === "Enter") { e.preventDefault(); search(); } }} />
          <select value=${filters.tag} onChange=${(e) => { setF({ tag: e.target.value }); setPage(1); loadAccounts({ ...filters, tag: e.target.value }); }}>
            <option value="">All tags</option>
            ${tagKeys.map((t) => html`<option key=${t} value=${t}>${t + " (" + s.tag_counts[t] + ")"}</option>`)}
          </select>
          <select value=${filters.country} onChange=${(e) => { setF({ country: e.target.value }); setPage(1); loadAccounts({ ...filters, country: e.target.value }); }}>
            <option value="">All countries</option>
            ${countries.map((c) => html`<option key=${c} value=${c}>${c + " (" + s.countries[c] + ")"}</option>`)}
          </select>
          <select value=${filters.sort} onChange=${(e) => { setF({ sort: e.target.value }); setPage(1); loadAccounts({ ...filters, sort: e.target.value }); }}>
            <option value="id">File order</option>
            <option value="newest">Newest</option>
            <option value="oldest">Oldest</option>
            <option value="email">Email A–Z</option>
            <option value="email-desc">Email Z–A</option>
            <option value="country">Country</option>
            <option value="line">Line number</option>
          </select>
          <button type="button" onClick=${search}>Filter</button>
          <button class="secondary" type="button" onClick=${clear}>Clear</button>
        </div>
        <div class="chips">
          ${shown.slice(0, 16).map((t) => html`
            <span class=${"chip" + (filters.tag === t ? " active" : "")} key=${t}
              onClick=${() => {
                const tag = filters.tag === t ? "" : t;
                setF({ tag });
                setPage(1);
                loadAccounts({ ...filters, tag });
                loadStats();
              }}>${t + " · " + s.tag_counts[t]}</span>`)}
        </div>
      </div>
    </header>

    <main class="wrap">
      <div class="stats">
        <div class="stat"><div class="label">Total valids</div><div class="value">${s.total != null ? s.total : "—"}</div></div>
        <div class="stat"><div class="label">File size</div><div class="value">${s.file_size != null ? fmtBytes(s.file_size) : "—"}</div></div>
      </div>
      <div class="meta">${count + " account(s) matched"}</div>
      ${pages > 1 && html`
        <div class="pager">
          <button class="secondary" type="button" disabled=${page <= 1} onClick=${() => gotoPage(page - 1)}>Back</button>
          <span class="muted">Page ${page + " / " + pages}</span>
          <button class="secondary" type="button" disabled=${page >= pages} onClick=${() => gotoPage(page + 1)}>Next</button>
        </div>`}
      <div class="grid">
        ${accounts.map((a) => html`<${AccountCard} key=${(a.id || "") + ":" + (a.line || "")} a=${a} onMail=${setMailAcc} toast=${toast} />`)}
      </div>
      ${!accounts.length && html`<div class="empty">No accounts match your filters.</div>`}
    </main>

    ${toastMsg && html`<div class="toast show">${toastMsg}</div>`}

    <${MailViewer} account=${mailAcc} onClose=${() => setMailAcc(null)} />
  `;
}

import { render } from "../app.mjs";
render(html`<${App} />`, document.getElementById("app"));
