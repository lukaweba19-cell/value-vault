// Dashboard: home menu and session resume views.
import { html, useState, useEffect, api, IconNew, IconResume, IconEditor, IconList, IconBack, IconNext, TelegramTgA, TelegramTgB, Tag } from "../app.mjs";

export const Choice = ({ icon: Icon, tg: Tg, title, desc, onClick }) => html`
  <button class="choice" type="button" onClick=${onClick}>
    ${Tg
      ? html`<div class="ico tg"><${Tg} size=${42} /></div>`
      : html`<div class="ico"><${Icon} size=${18} /></div>`}
    <h3>${title}</h3>
    <p>${desc}</p>
    <span class="choice-go"><${IconNext} size=${14} /></span>
  </button>`;

export function HomeView({ onNew, onResume, onEditor, onOpenViewer, onLive, live, uid }) {
  return html`
    <div class="panel">
      <div class="section">
        <div class="section-head">
          <div class="head-copy">
            <div class="eyebrow">Workspace</div>
            <h2>Start a session</h2>
            <div class="head-sub">Pick a list, tune the run, watch it work.</div>
          </div>
        </div>
        <div class="grid-3">
          <${Choice} icon=${IconNew} title="Start new"
            desc="New combo list, proxies, keywords and workers." onClick=${onNew} />
          <${Choice} icon=${IconResume} title="Resume session"
            desc="Pick a saved session and continue unfinished lines + retries." onClick=${onResume} />
          <${Choice} icon=${IconEditor} title="Editor"
            desc="Build, clean, extract and export combo lists." onClick=${onEditor} />
        </div>
        <div class="bar" style=${{ marginTop: "16px" }}>
          <div class="btnbar">
            <button class="primary" type="button" onClick=${onOpenViewer}>
              <${IconList} size=${14} />View valid
            </button>
            ${live && html`
              <button class="primary" type="button" style=${{ boxShadow: "0 0 0 3px rgba(124,92,255,.25)" }} onClick=${onLive}>
                Live run
              </button>`}
          </div>
          <div class="muted">Results land in valid.txt — open the viewer to browse them.</div>
        </div>
      </div>

      <div class="section">
        <div class="section-head">
          <div class="head-copy">
            <div class="eyebrow">Community</div>
            <h2>Stay in the loop</h2>
          </div>
        </div>
        <div class="grid-2">
          <${Choice} tg=${TelegramTgA} title="Tools & Updates"
            desc="Go here for tools, updates, and new releases."
            onClick=${() => openLink("https://t.me/+naqymNMpt75jYjdh")} />
          <${Choice} tg=${TelegramTgB} title="Chat"
            desc="Go here to chat with everyone in the community."
            onClick=${() => openLink("https://t.me/+cueTzd36nb9jNWQx")} />
        </div>
      </div>
    </div>`;
}

function openLink(u) {
  fetch("/api/open-link?u=" + encodeURIComponent(u));
}

export function ResumeView({ onBack, onPick }) {
  const [sessions, setSessions] = useState(null);
  const [page, setPage] = useState(0);
  const [picked, setPicked] = useState("");
  const perPage = 5;

  useEffect(() => {
    let alive = true;
    api("/api/sessions").then(({ data }) => {
      if (alive) setSessions((data && data.sessions) || []);
    }).catch(() => { if (alive) setSessions([]); });
    return () => { alive = false; };
  }, []);

  const list = sessions || [];
  const pages = Math.max(1, Math.ceil(list.length / perPage));
  const clamped = Math.min(Math.max(page, 0), pages - 1);
  const start = clamped * perPage;
  const slice = list.slice(start, start + perPage);

  // auto-select the first session of the current page (as the old UI did)
  useEffect(() => {
    if (list[start]) setPicked(list[start].file || "");
  }, [clamped, sessions]);

  const selected = list.find((x) => x.file === picked) || list[0] || null;

  return html`
    <div class="panel">
      <div class="section-head">
        <div class="head-copy">
          <div class="eyebrow">Saved sessions</div>
          <h2>Resume where you left off</h2>
          <div class="head-sub">Unfinished lines and retries are kept per session.</div>
        </div>
      </div>
      <div>
        ${sessions === null && html`<div class="muted">Loading sessions...</div>`}
        ${sessions !== null && !list.length && html`<div class="muted">No saved sessions</div>`}
        ${slice.map((x) => html`
            <div class=${"sess" + (picked === x.file ? " on" : "")} onClick=${() => setPicked(x.file || "")} key=${x.file}>
              <div class="bar">
                <b>${x.combo || ""}</b>
                <div class="muted">started ${x.started || "-"} · updated ${x.updated || "-"}</div>
              </div>
              <div style=${{ display: "flex", flexWrap: "wrap", gap: "6px", marginTop: "9px" }}>
                <${Tag} kind="green">workers ${x.workers || 0}<//>
                <${Tag} kind="warn">retries ${x.retries || 0}<//>
                ${x.proxy && html`<${Tag}>${x.proxy}<//>`}
                ${x.keywords && html`<${Tag}>keywords<//>`}
              </div>
            </div>`)}
      </div>
      <div class="pages">
        <button class="ghost" onClick=${() => setPage(clamped - 1)}>Prev</button>
        <div class="muted">${list.length ? "Page " + (clamped + 1) + " / " + pages : ""}</div>
        <button class="ghost" onClick=${() => setPage(clamped + 1)}>Next</button>
      </div>
      <div class="bar" style=${{ marginTop: "14px" }}>
        <button class="ghost" onClick=${onBack}>
          <${IconBack} size=${14} />Back
        </button>
        <button class="primary" onClick=${() => onPick(selected)}>
          <${IconNext} size=${14} />Resume selected
        </button>
      </div>
    </div>`;
}
