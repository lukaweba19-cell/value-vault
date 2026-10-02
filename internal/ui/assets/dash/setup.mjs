// Dashboard: setup (run configuration) view.
import { html, useState, useEffect, api, apiJSON, IconBack, IconPlay, Tag, Modal } from "../app.mjs";

const fileBase = (p) => String(p || "").split(/[\\/]/).pop();

export function SetupView({ form, set, proxyKind, setProxyKind, onBack, onStart }) {
  const [proxyMenu, setProxyMenu] = useState(false);
  const [saved, setSaved] = useState([]);
  const [saveOpen, setSaveOpen] = useState(false);
  const [saveName, setSaveName] = useState("");

  const loadSaved = async () => {
    try {
      const { data } = await api("/api/proxy-lists");
      setSaved((data || []).slice());
    } catch (e) { setSaved([]); }
  };
  useEffect(() => { loadSaved(); }, []);

  // close the saved-lists dropdown on outside clicks
  useEffect(() => {
    if (!proxyMenu) return;
    const close = (e) => {
      if (!e.target.closest || !e.target.closest(".drop")) setProxyMenu(false);
    };
    document.addEventListener("click", close);
    return () => document.removeEventListener("click", close);
  }, [proxyMenu]);

  const rowsFor = (kind) => saved.filter((x) => (kind === "server" ? x.type === "server" : x.type !== "server"));

  const useSaved = (x) => {
    if (proxyKind === "server") {
      set({ proxy_urls: (x.urls && x.urls.length) ? x.urls.slice() : [""] });
    } else {
      set({ proxy: x.path || "", proxyFileCount: "0 proxies" });
    }
    setProxyMenu(false);
  };

  const delSaved = async (x) => {
    await fetch("/api/proxy-lists?name=" + encodeURIComponent(x.name || "") + "&path=" + encodeURIComponent(x.path || ""), { method: "DELETE" });
    loadSaved();
  };

  const browse = async (kind) => {
    const { data } = await api("/api/pick?kind=" + encodeURIComponent(kind));
    if (data && data.path) set(kind === "proxy" ? { proxy: data.path } : { combo: data.path });
  };

  const setUrl = (i, v) => {
    const urls = (form.proxy_urls || []).slice();
    urls[i] = v;
    set({ proxy_urls: urls });
  };
  const addUrl = () => set({ proxy_urls: (form.proxy_urls || []).concat([""]) });
  const delUrl = () => set({ proxy_urls: (form.proxy_urls || []).slice(0, -1) });

  const testFile = async () => {
    set({ proxyFileCount: "checking…" });
    const { ok, data } = await apiJSON("/api/proxy-fetch", { path: form.proxy || "" });
    set({ proxyFileCount: ok ? ((data && data.count) || 0) + " proxies" : "failed" });
  };

  const testServer = async () => {
    set({ proxyCount: "checking…" });
    const urls = (form.proxy_urls || []).map((s) => s.trim()).filter(Boolean);
    const { ok, data } = await apiJSON("/api/proxy-fetch", { urls });
    set({ proxyCount: ok ? ((data && data.count) || 0) + " proxies" : "failed" });
  };

  const remember = async (checked) => {
    if (!checked) return;
    if (!(form.proxy || "").trim()) { set({ rememberProxy: false }); return; }
    await apiJSON("/api/proxy-lists", { path: form.proxy, name: fileBase(form.proxy) });
    loadSaved();
  };

  const saveServer = async () => {
    const urls = (form.proxy_urls || []).map((s) => s.trim()).filter(Boolean);
    const name = saveName.trim();
    if (!urls.length || !name) return;
    await apiJSON("/api/proxy-lists", { type: "server", name, urls });
    setSaveOpen(false);
    setSaveName("");
    loadSaved();
  };

  const caps = [["capKw", "Keywords"], ["capPay", "Payments"], ["capPromo", "Promos"], ["capFn", "Fortnite"]];
  const menuStyle = { padding: "8px 10px", display: "block", whiteSpace: "normal", cursor: "default" };
  const spread = { flex: 1, display: "flex", justifyContent: "space-between", gap: "10px", alignItems: "center" };

  return html`
    <div class="panel">
      <div class="section-head">
        <div class="head-copy">
          <div class="eyebrow">New session</div>
          <h2>Run configuration</h2>
          <div class="head-sub">Combo list, proxies, keywords and workers.</div>
        </div>
      </div>

      <div class="section">
        <div class="field">
          <label>Combos</label>
          <div class="input-row">
            <input value=${form.combo} placeholder="combo file" onInput=${(e) => set({ combo: e.target.value })} />
            <button class="ghost" type="button" onClick=${() => browse("combo")}>Browse</button>
          </div>
        </div>
      </div>

      <div class="section">
        <div class="eyebrow" style=${{ marginBottom: "10px" }}>Proxies</div>
        <div class="seg">
          <button class=${proxyKind === "file" ? "on" : ""} type="button" onClick=${() => setProxyKind("file")}>File</button>
          <button class=${proxyKind === "server" ? "on" : ""} type="button" onClick=${() => setProxyKind("server")}>Server</button>
        </div>
        ${proxyKind === "file" ? html`
          <div>
            <div class="input-row">
              <input value=${form.proxy} placeholder="proxy file" onInput=${(e) => set({ proxy: e.target.value })} />
              <button class="ghost" type="button" onClick=${() => browse("proxy")}>Browse</button>
            </div>
            <div class="toolbar">
              <label class="check">
                <input type="checkbox" checked=${form.rememberProxy} onClick=${(e) => remember(e.target.checked)} /> Remember
              </label>
              <button class="ghost" type="button" onClick=${testFile}>Check</button>
              <${Tag}>${form.proxyFileCount || "0 proxies"}<//>
              <div class="drop">
                <button class="ghost" type="button" onClick=${() => { setProxyMenu(!proxyMenu); loadSaved(); }}>Saved lists</button>
                ${proxyMenu && html`
                  <div class="menu on">
                    ${!rowsFor("file").length && html`<div class="muted" style=${menuStyle}>Nothing saved</div>`}
                    ${rowsFor("file").map((x) => html`
                      <div key=${(x.name || "") + (x.path || "")}>
                        <span style=${spread} onClick=${() => useSaved(x)}>
                          <b>${x.name || "list"}</b>
                          <${Tag}>${(x.count || 0) + " proxies · " + (x.path || "")}<//>
                        </span>
                        <button class="ghost" type="button" onClick=${() => delSaved(x)}>✕</button>
                      </div>`)}
                  </div>`}
              </div>
            </div>
          </div>` : html`
          <div>
            ${(form.proxy_urls || []).map((u, i) => html`
              <div class="field" style=${{ marginBottom: "8px" }} key=${i}>
                <input value=${u} placeholder="https://example.com/proxies.txt" onInput=${(e) => setUrl(i, e.target.value)} />
              </div>`)}
            <div class="toolbar">
              <button class="ghost" type="button" onClick=${addUrl}>+</button>
              ${(form.proxy_urls || []).length > 1 && html`<button class="ghost" type="button" onClick=${delUrl}>–</button>`}
              <button class="ghost" type="button" onClick=${() => setSaveOpen(true)}>Save list</button>
              <button class="ghost" type="button" onClick=${testServer}>Check</button>
              <${Tag}>${form.proxyCount || "0 proxies"}<//>
              <div class="drop">
                <button class="ghost" type="button" onClick=${() => { setProxyMenu(!proxyMenu); loadSaved(); }}>Saved servers</button>
                ${proxyMenu && html`
                  <div class="menu on">
                    ${!rowsFor("server").length && html`<div class="muted" style=${menuStyle}>Nothing saved</div>`}
                    ${rowsFor("server").map((x) => html`
                      <div key=${x.name || ""}>
                        <span style=${spread} onClick=${() => useSaved(x)}>
                          <b>${x.name || "list"}</b>
                          <${Tag}>${((x.urls && x.urls.length) || 0) + " urls · " + (x.count || 0) + " proxies"}<//>
                        </span>
                        <button class="ghost" type="button" onClick=${() => delSaved(x)}>✕</button>
                      </div>`)}
                  </div>`}
              </div>
            </div>
          </div>`}
      </div>

      <div class="section">
        <div class="grid-2">
          <div class="field">
            <label>Keywords</label>
            <input value=${form.kw} onInput=${(e) => set({ kw: e.target.value })} />
          </div>
          <div class="field">
            <label>Workers</label>
            <input value=${form.workers} onInput=${(e) => set({ workers: e.target.value })} />
          </div>
        </div>
      </div>

      <div class="section">
        <div class="eyebrow" style=${{ marginBottom: "10px" }}>Captures</div>
        <div class="row tight">
          ${caps.map(([key, label]) => html`
            <label class="check-chip" key=${key}>
              <input type="checkbox" checked=${form[key]} onClick=${(e) => set({ [key]: e.target.checked })} /> ${label}
            </label>`)}
        </div>
      </div>

      ${form.err && html`<div class="muted" style=${{ color: "var(--red)", marginTop: "12px" }}>${form.err}</div>`}

      <div class="bar" style=${{ marginTop: "20px" }}>
        <button class="ghost" onClick=${onBack}><${IconBack} size=${14} />Back</button>
        <button class="primary" type="button" onClick=${onStart}><${IconPlay} size=${14} />Start new</button>
      </div>

      <${Modal} show=${saveOpen} onClose=${() => setSaveOpen(false)} width=${520}>
        <div class="eyebrow">Save server list</div>
        <label style=${{ marginTop: "12px" }}>Name</label>
        <input value=${saveName} placeholder="proxy-labs https" onInput=${(e) => setSaveName(e.target.value)} />
        <div class="row">
          <button class="primary" type="button" onClick=${saveServer}>Save</button>
          <button class="ghost" type="button" onClick=${() => setSaveOpen(false)}>Cancel</button>
        </div>
      <//>
    </div>`;
}
