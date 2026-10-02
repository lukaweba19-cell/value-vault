// Dashboard: live run view (polls /api/state, shows stats, logs, hits).
import { html, useState, useEffect, useRef, api, IconBack, Tag } from "../app.mjs";

export function RunView({ status, onPause, onStop, onHome }) {
  const [snap, setSnap] = useState(null);
  const logsRef = useRef(null);
  const hitsRef = useRef(null);
  // Stick-to-bottom for both logs: keep following the newest lines while the
  // user is already at (or near) the bottom, but once they scroll up to read
  // history, stay where they are until they scroll back down themselves.
  const stickLogs = useRef(true);
  const stickHits = useRef(true);
  const onLogScroll = (ref) => (e) => {
    const el = e.currentTarget;
    ref.current = el.scrollHeight - el.scrollTop - el.clientHeight < 28;
  };

  useEffect(() => {
    let alive = true;
    const tick = async () => {
      try {
        const { data } = await api("/api/state");
        if (alive) setSnap(data);
      } catch (e) { /* keep last snapshot */ }
    };
    tick();
    const t = setInterval(tick, 1200);
    return () => { alive = false; clearInterval(t); };
  }, []);

  // Runs after every render (i.e. after each snapshot repaint) so new lines
  // are pulled into view exactly when the user hasn't scrolled away.
  useEffect(() => {
    const el = logsRef.current;
    if (el && stickLogs.current) el.scrollTop = el.scrollHeight;
  });
  useEffect(() => {
    const el = hitsRef.current;
    if (el && stickHits.current) el.scrollTop = el.scrollHeight;
  });

  const s = snap || {};
  const cpm = Number(s.rate_cpm || 0);
  const logs = (s.logs || []).slice(-80);
  const hitsText = (s.hits || []).join("\n");

  const tiles = [
    ["Checked", (s.checked || 0) + " / " + (s.total || 0), ""],
    ["Running", s.elapsed || "0s", ""],
    ["ETA", s.eta || "—", ""],
    ["Speed", cpm > 0 ? (cpm >= 10 ? Math.round(cpm) + "/min" : cpm.toFixed(1) + "/min") : "—", ""],
    ["Daily", (s.daily || 0) + " / " + (s.limit || 0), ""],
  ];

  return html`
    <div class="panel">
      <div class="bar">
        <div class="btnbar">
          <${Tag} kind=${s.paused ? "warn" : (s.running ? "yellow" : "")}>${s.status || "Ready"}<//>
        </div>
        <div class="btnbar">
          <button class="primary" onClick=${onPause}>${s.paused ? "Resume checking" : "Pause"}</button>
          <button class="ghost" type="button" onClick=${onStop}>Stop</button>
          <button class="ghost" onClick=${onHome}><${IconBack} size=${14} />Home</button>
        </div>
      </div>

      <div class="statgrid" style=${{ marginTop: "16px" }}>
        ${tiles.map(([k, v]) => html`
          <div class="stat-tile" key=${k}>
            <div class="k">${k}</div>
            <div class="v">${v}</div>
          </div>`)}
      </div>

      <div class="toolbar">
        <${Tag} kind="green">Valid <b>${s.valid || 0}</b><//>
        <${Tag} kind="green">Free <b>${s.free || 0}</b><//>
        <${Tag} kind="blue">Private <b>${s.private || 0}</b><//>
        <${Tag} kind="warn">Invalid <b>${s.invalid || 0}</b><//>
        <${Tag} kind="red">Retries <b>${s.retries || 0}</b><//>
      </div>

      <div class="run-split" style=${{ marginTop: "16px" }}>
        <div class="logpane">
          <div class="log-head">
            <span class="eyebrow">Activity</span>
            <span class="muted">live</span>
          </div>
          <div class="log" ref=${logsRef} onScroll=${onLogScroll(stickLogs)}>
            ${logs.map((line, i) => html`<div key=${i} style=${{ color: /\[HIT\]|\[Free Hit\]|\[Private Hit\]/i.test(line) ? "#9af0c8" : "#f5a8a8" }}>${line}</div>`)}
          </div>
        </div>
        <div class="logpane">
          <div class="log-head">
            <span class="eyebrow">Hits</span>
            <span class="muted">${(s.hits || []).length} lines</span>
          </div>
          <div class="log" ref=${hitsRef} onScroll=${onLogScroll(stickHits)} style="color:#a7f3d0">${hitsText}</div>
        </div>
      </div>
    </div>`;
}
