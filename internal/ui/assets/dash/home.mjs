// Dashboard: home menu and session resume views.
import { html, useState, useEffect, api, IconNew, IconResume, IconEditor, IconList, IconBack, IconNext, TelegramTgA, TelegramTgB, Tag } from "../app.mjs";

export const Choice = ({ icon: Icon, tg: Tg, title, desc, onClick }) => html`
  <button class="choice" type="button" onClick=${onClick}>
    ${Tg
      ? html`<div class="ico tg"><${Tg} size=${42} /></div>`
      : html`<div class="ico"><${Icon} size=${18} /></div>`}
    <h3>${title}</h3>
    <p>${desc}</p>
  </button>`;

export function HomeView({ onNew, onResume, onEditor, onOpenViewer, onLive, live, uid }) {
  return html`
    <div class="panel">
      <div class="grid2">
        <${Choice} icon=${IconNew} title="Start new"
          desc="New combo list, proxies, keywords and workers." onClick=${onNew} />
        <${Choice} icon=${IconResume} title="Resume session"
          desc="Pick a saved session and continue unfinished lines + retries." onClick=${onResume} />
        <${Choice} icon=${IconEditor} title="Editor"
          desc="Build, clean, extract and export combo lists." onClick=${onEditor} />
      </div>
      <div class="row" style="margin-top:14px">
        <button class="primary" type="button" onClick=${onOpenViewer}>
          <${IconList} size=${14} />View valid
        </button>
        ${live && html`
          <button class="primary" type="button" style="box-shadow:0 0 0 3px rgba(124,92,255,.25)" onClick=${onLive}>
            Live run
          </button>`}
      </div>
      <div class="grid2" style="margin-top:14px">
        <${Choice} tg=${TelegramTgA} title="Tools & Updates"
          desc="Go here for tools, updates, and new releases."
          onClick=${() => openLink("https://t.me/+naqymNMpt75jYjdh")} />
        <${Choice} tg=${TelegramTgB} title="Chat"
          desc="Go here to chat with everyone in the community."
          onClick=${() => openLink("https://t.me/+cueTzd36nb9jNWQx")} />
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
      <div class="muted">SAVED SESSIONS</div>
      <div>
        ${sessions === null && html`<div class="muted">Loading sessions...</div>`}
        ${sessions !== null && !list.length && html`<div class="muted">No saved sessions</div>`}
        ${slice.map((x) => {
          const n = start + list.indexOf(x);
          return html`
            <div class=${"sess" + (picked === x.file ? " on" : "")} onClick=${() => setPicked(x.file || "")} key=${x.file}>
              <b>${x.combo || ""}</b>
              <div style="display:flex;flex-wrap:wrap;gap:6px;margin:8px 0">
                <${Tag} kind="green">workers ${x.workers || 0}<//>
                <${Tag} kind="warn">retries ${x.retries || 0}<//>
                ${x.proxy && html`<${Tag}>${x.proxy}<//>`}
                ${x.keywords && html`<${Tag}>keywords<//>`}
              </div>
              <div class="muted">started ${x.started || "-"} · updated ${x.updated || "-"}</div>
            </div>`;
        })}
      </div>
      <div class="pages">
        <button class="ghost" onClick=${() => setPage(clamped - 1)}>Prev</button>
        <div class="muted">${list.length ? "Page " + (clamped + 1) + " / " + pages : ""}</div>
        <button class="ghost" onClick=${() => setPage(clamped + 1)}>Next</button>
      </div>
      <div class="row">
        <button class="ghost" onClick=${onBack}>
          <${IconBack} size=${14} />Back
        </button>
        <button class="primary" onClick=${() => onPick(selected)}>
          <${IconNext} size=${14} />Resume selected
        </button>
      </div>
    </div>`;
}
