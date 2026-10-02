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

  return html`
    <div class="panel">
      <div class="run-split">
        <div>
          <div style="display:flex;flex-wrap:wrap;gap:8px;align-items:center;margin-bottom:12px">
            <${Tag} kind=${s.paused ? "warn" : (s.running ? "yellow" : "")}>${s.status || "Ready"}<//>
            <${Tag} kind="yellow">Checked <b>${(s.checked || 0) + " / " + (s.total || 0)}</b><//>
            <${Tag} kind="yellow">Running <b>${s.elapsed || "0s"}</b><//>
            <${Tag} kind="yellow">ETA <b>${s.eta || "—"}</b><//>
            <${Tag} kind="red">Speed <b>${cpm > 0 ? (cpm >= 10 ? Math.round(cpm) + "/min" : cpm.toFixed(1) + "/min") : "—"}</b><//>
          </div>
          <div style="display:flex;flex-wrap:wrap;gap:8px;margin:8px 0 14px">
            <${Tag} kind="green">Valid <b>${s.valid || 0}</b><//>
            <${Tag} kind="green">Free <b>${s.free || 0}</b><//>
            <${Tag} kind="blue">Private <b>${s.private || 0}</b><//>
            <${Tag} kind="warn">Invalid <b>${s.invalid || 0}</b><//>
            <${Tag} kind="red">Retries <b>${s.retries || 0}</b><//>
            <${Tag} kind="orange">Daily <b>${(s.daily || 0) + " / " + (s.limit || 0)}</b><//>
          </div>
          <label>Activity</label>
          <div class="log" ref=${logsRef} onScroll=${onLogScroll(stickLogs)} style="color:#fb7185">
            ${logs.map((line, i) => html`<div key=${i} style=${{ color: /\[HIT\]|\[Free Hit\]|\[Private Hit\]/i.test(line) ? "#9af0c8" : "#f5a8a8" }}>${line}</div>`)}
          </div>
        </div>
        <div>
          <label>Hits</label>
          <div class="log" ref=${hitsRef} onScroll=${onLogScroll(stickHits)} style="color:#a7f3d0">${hitsText}</div>
        </div>
      </div>
      <div class="row">
        <button class="primary" onClick=${onPause}>${s.paused ? "Resume checking" : "Pause"}</button>
        <button class="ghost" type="button" onClick=${onStop}>Stop</button>
        <button class="ghost" onClick=${onHome}><${IconBack} size=${14} />Home</button>
      </div>
    </div>`;
}
