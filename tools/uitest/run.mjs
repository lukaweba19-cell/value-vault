// jsdom smoke + visual regression harness for the vendored Preact UI.
// Mounts the real dashboard and viewer apps against mocked /api routes and
// asserts DOM output — including SVG icon geometry and gradient defs, so the
// regressions seen in 1.0.9 screenshots (giant icons, missing Telegram blue
// circles) fail loudly here.
//
// Usage:
//   JSDOM_DIR=/path/to/node_modules node tools/uitest/run.mjs
// (JSDOM_DIR points at a directory containing the jsdom package; omit it if
// jsdom is resolvable from the repo.)
import { createRequire } from "module";
import { pathToFileURL, fileURLToPath } from "url";
import path from "path";

const require = createRequire(import.meta.url);
const { JSDOM } = require(process.env.JSDOM_DIR
  ? path.join(process.env.JSDOM_DIR, "jsdom")
  : "jsdom");

const assets = process.env.ASSETS_DIR
  ? path.resolve(process.env.ASSETS_DIR)
  : path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "internal", "ui", "assets");

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const errors = [];
process.on("unhandledRejection", (e) => errors.push("unhandledRejection: " + (e && e.stack || e)));

function makeEnv(routes) {
  const dom = new JSDOM("<!DOCTYPE html><html><body><div id=\"app\"></div></body></html>", {
    url: "http://127.0.0.1:2718/",
    pretendToBeVisual: true,
  });
  global.window = dom.window;
  global.document = dom.window.document;
  Object.defineProperty(global, "navigator", { value: dom.window.navigator, configurable: true });
  global.EventSource = class { constructor() {} close() {} };
  global.requestAnimationFrame = (cb) => setTimeout(cb, 0);
  global.cancelAnimationFrame = (id) => clearTimeout(id);
  global.getComputedStyle = dom.window.getComputedStyle.bind(dom.window);
  const calls = [];
  global.fetch = async (url, opts) => {
    calls.push({ url: String(url), method: (opts && opts.method) || "GET", body: opts && opts.body });
    const u = String(url).split("?")[0];
    const h = routes[u] || routes[String(url)];
    if (!h) return { ok: false, status: 404, json: async () => ({}) };
    const data = typeof h === "function" ? h({ url: String(url), method: (opts && opts.method) || "GET", body: opts && opts.body ? JSON.parse(opts.body) : null }) : h;
    return { ok: true, status: 200, json: async () => data };
  };
  return { dom, calls };
}

const assert = (cond, msg) => { if (!cond) { errors.push("ASSERT FAIL: " + msg); } else { console.log("  ✓", msg); } };

/* ------- SVG visual assertions (regression guards for the 1.0.9 bugs) ------- */

// Every rendered svg must carry numeric width/height attributes; a missing or
// garbage value (e.g. the literal text "{props.size") makes icons blow up to
// full container size.
function assertSvgSized(scope, label) {
  const svgs = [...scope.querySelectorAll("svg")];
  assert(svgs.length > 0, label + ": has svg elements");
  for (const svg of svgs) {
    const w = svg.getAttribute("width"), h = svg.getAttribute("height");
    if (!w || !h || !/^\d+(\.\d+)?$/.test(w) || !/^\d+(\.\d+)?$/.test(h)) {
      errors.push(`ASSERT FAIL: ${label}: svg missing numeric width/height (got ${w}x${h}); outer=${svg.outerHTML.slice(0, 120)}`);
    } else {
      console.log(`  ✓ ${label}: svg ${w}x${h}`);
    }
  }
}

// No htm template garbage attributes anywhere (width={props.size leaks).
function assertNoTemplateGarbage(scope, label) {
  const bad = [...scope.querySelectorAll("*")].filter((el) =>
    [...el.attributes].some((a) => (a.value || "").includes("{props") || a.name === "{props"));
  assert(bad.length === 0, label + ": no literal htm attribute garbage");
}

// Telegram icons must paint their circle with a gradient that actually exists
// in the DOM — otherwise the blue circle disappears and only the white plane
// path is visible.
function assertTelegramGradient(doc, gid, label) {
  const grad = doc.querySelector(`linearGradient#${gid}`);
  assert(!!grad, `${label}: linearGradient#${gid} defs present`);
  if (grad) {
    const stops = [...grad.querySelectorAll("stop")].map((s) => s.getAttribute("stop-color"));
    assert(stops.includes("#37BBFE") && stops.includes("#007DBB"), `${label}: gradient stops #37BBFE/#007DBB`);
  }
  const circle = doc.querySelector(`circle[fill="url(#${gid})"]`);
  assert(!!circle, `${label}: circle painted with url(#${gid})`);
}

/* ---------------- dashboard ---------------- */
console.log("== dashboard ==");
{
  let running = false;
  const { dom, calls } = makeEnv({
    "/api/state": () => ({ running, paused: false, status: running ? "Checking" : "Ready", valid: 1, invalid: 2, retries: 3, checked: 4, total: 10, daily: 5, limit: 10000, uid: "MACHINE-TEST-123", logs: ["boot ok", "[HIT] valid one"], hits: ["a@b.com:pw"], free: 1, private: 2, elapsed: "12s", eta: "3m", rate_cpm: 42.5 }),
    "/api/sessions": { sessions: [{ file: "s1.json", combo: "combos.txt", proxy: "", keywords: "vbucks", workers: 20, retries: 2, updated: "u1", started: "st1" }] },
    "/api/proxy-lists": [{ name: "saved1", type: "", path: "C:\\px.txt", count: 10 }, { name: "srv1", type: "server", urls: ["http://x/p.txt"], count: 5 }],
    "/api/editor/banners": { banners: [{ name: "sig1", text: "# tag" }] },
    "/api/update/check": { ok: false },
    "/api/open-viewer": {},
    "/api/start": (req) => { running = true; return { ok: true }; },
    "/api/pause": { ok: true },
    "/api/stop": (req) => { running = false; return { ok: true }; },
    "/api/open-link": {},
  });
  await import(pathToFileURL(path.join(assets, "dash", "main.mjs")));
  await sleep(80);
  const d = dom.window.document;
  assert(d.querySelector(".wrap"), "dashboard .wrap mounted");
  assert(d.body.textContent.includes("Xbox Full Capture + Keyword Inboxer + Promos / Codes"), "title line 1");
  assert(d.body.textContent.includes("Epic Games / Fortnite Full Capture"), "title line 2");
  assert(d.body.textContent.includes("@solodolosouls"), "credit handle");
  assert(d.body.textContent.includes("ID MACHINE-TEST-123"), "machine id line");
  assert(d.body.textContent.includes("Start new") && d.body.textContent.includes("Resume session") && d.body.textContent.includes("Editor"), "home choices");
  assert(d.body.textContent.includes("Tools & Updates") && d.body.textContent.includes("Chat"), "telegram cards");
  assert(d.querySelector(".brand svg circle"), "xbox sphere svg rendered");

  /* --- visual regression guards --- */
  assertNoTemplateGarbage(d, "home");
  assertTelegramGradient(d, "tg0", "credit telegram");
  assertTelegramGradient(d, "tgA", "tools telegram");
  assertTelegramGradient(d, "tgB", "chat telegram");
  const tgCard = [...d.querySelectorAll(".ico.tg svg")];
  assert(tgCard.length === 2 && tgCard.every((s) => s.getAttribute("width") === "42"), "telegram card svgs 42px");
  const creditSvg = d.querySelector(".credit svg");
  assert(creditSvg && creditSvg.getAttribute("width") === "18", "credit telegram svg 18px");
  const brandSvg = d.querySelector(".brand svg");
  assert(brandSvg && brandSvg.getAttribute("viewBox") === "0 0 32 32", "brand svg viewBox (css sizes it 72px)");
  const viewValidBtn = [...d.querySelectorAll("button")].find((b) => b.textContent.includes("View valid"));
  assert(!!viewValidBtn, "View valid button present");
  const vvSvg = viewValidBtn && viewValidBtn.querySelector("svg");
  assert(vvSvg && vvSvg.getAttribute("width") === "14" && vvSvg.getAttribute("height") === "14", "View valid icon svg 14px (compact button)");

  // go to setup
  [...d.querySelectorAll("button")].find((b) => b.textContent.includes("Start new")).click();
  await sleep(40);
  assert(d.body.textContent.includes("Combos") && d.body.textContent.includes("Proxies") && d.body.textContent.includes("Workers"), "setup fields render");
  assert(d.body.textContent.includes("Captures") && d.body.textContent.includes("Fortnite"), "capture toggles");
  const backBtn = [...d.querySelectorAll("button")].find((b) => b.textContent.trim().startsWith("Back"));
  assert(!!backBtn, "setup Back button present");
  const backSvg = backBtn && backBtn.querySelector("svg");
  assert(backSvg && backSvg.getAttribute("width") === "14" && backSvg.getAttribute("height") === "14", "setup Back chevron svg 14px (not a giant card)");
  assertNoTemplateGarbage(d, "setup");

  // start with a combo set
  const inputs = [...d.querySelectorAll("input")];
  const comboInput = inputs.find((i) => i.placeholder === "combo file");
  comboInput.value = "C:/lists/combos.txt";
  comboInput.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
  await sleep(20);
  [...d.querySelectorAll("button")].find((b) => b.textContent.trim() === "Start new").click();
  await sleep(80);
  const startCall = calls.find((c) => c.url === "/api/start");
  assert(!!startCall, "POST /api/start sent");
  const sent = startCall && JSON.parse(startCall.body);
  assert(sent && sent.combo === "C:/lists/combos.txt" && sent.workers === "20" && sent.keywords_on === true && sent.fortnite_on === true && sent.resume === false, "start payload fields");
  assert(d.body.textContent.includes("Checked") && d.body.textContent.includes("4 / 10"), "run view stats (checked 4/10)");
  assert(d.body.textContent.includes("Daily") && d.body.textContent.includes("5 / 10000"), "daily quota tag");
  assert(d.body.textContent.includes("[HIT] valid one"), "activity log line");
  assert(d.body.textContent.includes("a@b.com:pw"), "hits log content");
  assert(d.body.textContent.includes("43/min"), "speed tag");
  assertNoTemplateGarbage(d, "run");

  // stop returns home
  [...d.querySelectorAll("button")].find((b) => b.textContent.trim() === "Stop").click();
  await sleep(40);
  assert(d.body.textContent.includes("Start new"), "back on home after stop");
}

/* ---------------- viewer ---------------- */
console.log("== viewer ==");
{
  const { dom, calls } = makeEnv({
    "/api/stats": { total: 2, with_pass: 2, file_size: 2048, file_path: "C:/valid.txt", modified: "m1", loaded_at: "l1", tag_counts: { "Has Payment": 1, "Fortnite": 1 }, countries: { "US": 2 } },
    "/api/accounts": (req) => ({ accounts: [{ id: 1, line: 2, email: "a@b.com", password: "pw1", tags: ["Has Payment"], fields: { Country: "US", "V-Bucks": "500" }, raw: "a@b.com:pw1" }], count: 1, page: 1, pages: 1 }),
    "/api/reload": { ok: true },
    "/api/mail/probe": (() => { let n = 0; return () => { n++; return n === 1
      ? { ok: true, total: 1, messages: [{ id: "m1", subject: "Test mail", from: "Sender <sender@example.com>", date: "2026-09-29T14:30:00.000Z", preview: "hello world" }] }
      : { ok: false, need_login: true }; }; })(),
    "/api/mail/list": { ok: true, total: 1, messages: [{ id: "m1", subject: "Test mail", from: "Sender <sender@example.com>", date: "2026-09-29T14:30:00.000Z", preview: "hello world" }] },
    "/api/mail/body": { ok: true, html: "<b>full body</b>" },
    "/api/proxy-lists": [{ name: "srv1", type: "server", urls: ["http://x/p.txt"], count: 5 }],
  });
  await import(pathToFileURL(path.join(assets, "viewer", "main.mjs")));
  await sleep(80);
  const d = dom.window.document;
  assert(d.querySelector("h1") && d.querySelector("h1").textContent === "Valid Viewer", "viewer h1");
  assert(d.body.textContent.includes("C:/valid.txt"), "file meta line");
  assert(d.body.textContent.includes("2.0 KB"), "file size stat");
  assert(d.querySelector(".card .email") && d.querySelector(".card .email").textContent === "a@b.com", "account card email");
  assert(d.body.textContent.includes("V-Bucks") && d.body.textContent.includes("500"), "field rows on card");
  assert([...d.querySelectorAll(".chip")].some((c) => c.textContent.includes("Has Payment")), "quick tag chips");
  assertNoTemplateGarbage(d, "viewer");

  // open mail viewer
  [...d.querySelectorAll("button")].find((b) => b.textContent === "Email Viewer").click();
  await sleep(80);
  assert(d.body.textContent.includes("EMAIL VIEWER"), "mail modal open");
  assert(d.body.textContent.includes("Test mail"), "mail subject in list");
  assert([...d.querySelectorAll(".mail-item b")].some((b) => b.textContent === "sender@example.com"), "sender grouped list");
  // select message
  [...d.querySelectorAll(".mail-item")].find((el) => el.textContent.includes("Test mail")).click();
  await sleep(60);
  assert(d.querySelector(".mail-frame"), "message iframe rendered");
  assert([...d.querySelectorAll("button")].some((b) => b.textContent.trim() === "Delete"), "delete button present");

  // close the modal fully (original behavior: onClose unmounts the viewer) and
  // reopen with a FRESH component instance — state must not leak between opens.
  const closeBtns = [...d.querySelectorAll("button")].filter((b) => b.textContent.trim() === "Close");
  closeBtns[closeBtns.length - 1].click();
  await sleep(40);
  assert(!d.body.textContent.includes("EMAIL VIEWER"), "mail modal fully closed on Close");

  // Regression guard: mail list requests must carry the proxy choice the user
  // just made. The original read window._mailProxy fresh at call time; a stale
  // React state closure dropped it from the FIRST list call after Continue.
  calls.length = 0;
  [...d.querySelectorAll("button")].find((b) => b.textContent === "Email Viewer").click();
  await sleep(60);
  assert(d.body.textContent.includes("Use proxies for this mailbox login?"), "mail ask-proxy prompt on fresh open");
  const askScope = [...d.querySelectorAll("button")].filter((b) => b.textContent.trim() === "No" || b.textContent.trim() === "Yes");
  askScope.find((b) => b.textContent.trim() === "Yes").click();
  await sleep(40);
  assert(d.body.textContent.includes("PROXY SOURCE"), "proxy source step shown");
  // pick a saved server list: switch to server mode, open its saved menu
  const serverModeBtn = [...d.querySelectorAll("button")].find((b) => b.textContent.trim() === "Server");
  assert(!!serverModeBtn, "server mode toggle present");
  serverModeBtn.click();
  await sleep(20);
  const savedSrvBtn = [...d.querySelectorAll("button")].find((b) => b.textContent.trim() === "Saved servers");
  assert(!!savedSrvBtn, "saved servers button present");
  savedSrvBtn.click();
  await sleep(20);
  const srvRow = [...d.querySelectorAll(".menu div")].find((el) => el.textContent.includes("srv1"));
  assert(!!srvRow, "saved server list row rendered");
  srvRow.click();
  await sleep(20);
  [...d.querySelectorAll("button")].find((b) => b.textContent.trim() === "Continue").click();
  await sleep(60);
  const listCall = calls.filter((c) => c.url === "/api/mail/list").pop();
  assert(!!listCall, "mail list call issued after proxy continue");
  const listBody = listCall && JSON.parse(listCall.body);
  assert(listBody && listBody.proxy_mode === "server" && Array.isArray(listBody.proxy_urls) && listBody.proxy_urls[0] === "http://x/p.txt", "mail list carries chosen proxy (mode+urls) on first load");
  assert(d.body.textContent.includes("Test mail"), "mail list rendered after proxied load");
}

if (errors.length) {
  console.log("\nFAILURES:");
  errors.forEach((e) => console.log(" -", e));
  process.exit(1);
}
console.log("\nALL SMOKE CHECKS PASSED");

process.exit(0);
