// Shared Preact app library: htm runtime, API helper, clipboard, icons, widgets.
import { h, render } from "./preact.module.mjs";
import { useState, useEffect, useRef, useMemo, useCallback } from "./hooks.module.mjs";
import htm from "./htm.module.mjs";

const html = htm.bind(h);
export { h, render, html, useState, useEffect, useRef, useMemo, useCallback };

/* ---------------- API helper ---------------- */
export async function api(url, opts) {
  const res = await fetch(url, opts);
  let data = null;
  try { data = await res.json(); } catch (e) { /* non-JSON */ }
  return { ok: res.ok, status: res.status, data };
}

export async function apiJSON(url, body, method) {
  return api(url, {
    method: method || "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

/* ---------------- clipboard (modern API + textarea fallback, as before) ---------------- */
function fallbackCopy(text) {
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.top = "-9999px";
    ta.style.left = "0";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    if (navigator.userAgent.match(/ipad|iphone|ipod/i)) {
      const range = document.createRange();
      range.selectNodeContents(ta);
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
      ta.setSelectionRange(0, text.length);
    } else {
      ta.select();
    }
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch (e) {
    return false;
  }
}

export function copyText(text) {
  text = String(text == null ? "" : text);
  if (!text) return Promise.resolve(false);
  if (navigator.clipboard && navigator.clipboard.writeText) {
    return navigator.clipboard.writeText(text).then(() => true).catch(() => fallbackCopy(text));
  }
  return Promise.resolve(fallbackCopy(text));
}

/* ---------------- icons (SVGs byte-exact from the original UI) ----------------
   NOTE: htm interpolates with ${...} only. `width={...}` is parsed as a literal
   string attribute, which blows the SVG up to 100% size. Always use ${...}. */
export const IconNew = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 18} height=${props.size || 18}>
    <path d="M12 5v14M5 12h14" stroke="#22c55e" stroke-width="2" stroke-linecap="round"/>
  </svg>`;
export const IconResume = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 18} height=${props.size || 18}>
    <path d="M4 12a8 8 0 101.7-4.9" stroke="#c084fc" stroke-width="2" stroke-linecap="round"/><path d="M4 5v5h5" stroke="#c084fc" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
  </svg>`;
export const IconEditor = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 18} height=${props.size || 18}>
    <path d="M4 20h4l10-10-4-4L4 16v4z" stroke="#38bdf8" stroke-width="2" stroke-linejoin="round"/><path d="M13 7l4 4" stroke="#38bdf8" stroke-width="2"/>
  </svg>`;
export const IconList = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 14} height=${props.size || 14}>
    <path d="M4 6h16M4 12h16M4 18h10" stroke="#fff" stroke-width="2" stroke-linecap="round"/>
  </svg>`;
export const IconBack = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 14} height=${props.size || 14}>
    <path d="M15 18l-6-6 6-6" stroke="#e8edf7" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
  </svg>`;
export const IconNext = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 14} height=${props.size || 14}>
    <path d="M5 12h14M13 6l6 6-6 6" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
  </svg>`;
export const IconSave = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 16} height=${props.size || 16}>
    <path d="M12 3v12M8 11l4 4 4-4M4 21h16" stroke="#e8edf7" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
  </svg>`;
export const IconDownload = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 18} height=${props.size || 18}>
    <path d="M4 20h16M12 4v12M8 12l4 4 4-4" stroke="#38bdf8" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
  </svg>`;
export const IconPlay = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 14} height=${props.size || 14}>
    <path d="M8 5v14l11-7z" fill="#fff"/>
  </svg>`;
export const IconHome = (props) => html`
  <svg viewBox="0 0 24 24" fill="none" width=${props.size || 14} height=${props.size || 14}>
    <path d="M4 11l8-7 8 7v8a1.5 1.5 0 01-1.5 1.5h-13A1.5 1.5 0 014 19v-8z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/>
    <path d="M10 20.5v-6h4v6" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/>
  </svg>`;

/* Telegram mark: circle painted with the per-instance gradient (ids tg0/tgA/tgB
   match the original UI). The gradient id must resolve or the blue circle
   disappears, so each component carries its original default id. */
const tgPlanePath = "M22.9866 10.2088C23.1112 9.40332 22.3454 8.76755 21.6292 9.082L7.36482 15.3448C6.85123 15.5703 6.8888 16.3483 7.42147 16.5179L10.3631 17.4547C10.9246 17.6335 11.5325 17.541 12.0228 17.2023L18.655 12.6203C18.855 12.4821 19.073 12.7665 18.9021 12.9426L14.1281 17.8646C13.665 18.3421 13.7569 19.1512 14.314 19.5005L19.659 22.8523C20.2585 23.2282 21.0297 22.8506 21.1418 22.1261L22.9866 10.2088Z";

const telegramSVG = (gid, size) => html`
  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg" width=${size || 42} height=${size || 42}>
    <circle cx="16" cy="16" r="14" fill="url(#${gid})"/><path d=${tgPlanePath} fill="white"/><defs><linearGradient id=${gid} x1="16" y1="2" x2="16" y2="30" gradientUnits="userSpaceOnUse"><stop stop-color="#37BBFE"/><stop offset="1" stop-color="#007DBB"/></linearGradient></defs>
  </svg>`;

export const TelegramTg0 = (props) => telegramSVG(props.gid || "tg0", props.size);
export const TelegramTgA = (props) => telegramSVG(props.gid || "tgA", props.size);
export const TelegramTgB = (props) => telegramSVG(props.gid || "tgB", props.size);
export const XboxLogo = () => html`
  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg"><circle opacity="0.852" cx="16" cy="16" r="14" fill="url(#xb0)"/><path d="M6.75137 26.5333C6.73227 26.5193 6.7127 26.5016 6.69312 26.4801C6.33073 26.1504 5.74045 25.6304 5.12362 24.8444C4.5909 23.6889 4.89287 21.748 6.20386 19.0075C8.05357 15.1409 11.0131 11.2151 12.2265 9.8225C11.8813 9.43732 10.8119 8.38253 9.29659 7.24477C7.7813 6.10701 6.67738 5.99047 6.29264 6.05961C6.64779 5.67443 7.47646 4.94852 8.17196 4.45964C10.3265 3.39299 14.3081 5.19543 16.0148 6.22257V13.2891C11.6642 16.6372 7.63924 21.8075 6.82536 23.8222C6.20418 25.36 6.44975 26.2128 6.69312 26.4801C6.71325 26.4984 6.73269 26.5161 6.75137 26.5333Z" fill="url(#xb1)"/><path d="M25.2779 26.5333C25.297 26.5193 25.3166 26.5016 25.3362 26.4801C25.6986 26.1504 26.2888 25.6304 26.9057 24.8444C27.4384 23.6889 27.1364 21.748 25.8254 19.0075C23.9757 15.1409 21.0162 11.2151 19.8028 9.8225C20.148 9.43732 21.2174 8.38253 22.7327 7.24477C24.248 6.10701 25.3519 5.99047 25.7367 6.05961C25.3815 5.67443 24.5528 4.94852 23.8573 4.45964C21.7028 3.39299 17.7212 5.19543 16.0145 6.22257V13.2891C20.3651 16.6372 24.3901 21.8075 25.2039 23.8222C25.8251 25.36 25.5796 26.2128 25.3362 26.4801C25.316 26.4984 25.2966 26.5161 25.2779 26.5333Z" fill="url(#xb3)"/><defs><radialGradient id="xb0" cx="0" cy="0" r="1" gradientUnits="userSpaceOnUse" gradientTransform="translate(16 14.5) rotate(90.0548) scale(15.5)"><stop stop-color="#FAFAFA"/><stop offset="1" stop-color="#879288"/></radialGradient><linearGradient id="xb1" x1="12.99" y1="4.72" x2="13.22" y2="12.78" gradientUnits="userSpaceOnUse"><stop stop-color="#008B00"/><stop offset="1" stop-color="#6CC329"/></linearGradient><linearGradient id="xb3" x1="19.03" y1="4.72" x2="18.79" y2="12.78" gradientUnits="userSpaceOnUse"><stop stop-color="#008B00"/><stop offset="1" stop-color="#6CC329"/></linearGradient></defs></svg>`;

/* ---------------- small widgets ---------------- */
export const Tag = ({ kind, children, ...rest }) =>
  html`<span class=${"tag" + (kind ? " " + kind : "")} ...${rest}>${children}</span>`;

export const UidLine = ({ uid }) => {
  const [copied, setCopied] = useState(false);
  if (!uid) return null;
  const copy = () => {
    copyText(uid).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 900);
    });
  };
  return html`
    <div class="tag" style=${{ cursor: "pointer" }} title="Click to copy" onClick=${copy}>
      ${copied ? "ID " + uid + " copied" : "ID " + uid}
    </div>`;
};

export const Modal = ({ show, onClose, width, children }) => {
  if (!show) return null;
  return html`
    <div class="modal" onClick=${(e) => { if (e.target === e.currentTarget && onClose) onClose(); }}>
      <div class="modal-card" style=${width ? { width: "min(" + width + "px, 100%)" } : null}>${children}</div>
    </div>`;
};

/* Animated panel section switcher */
export const Fade = ({ children }) => {
  const [key, setKey] = useState(children);
  useEffect(() => { setKey(children); }, [children]);
  return html`<div key=${key} style=${{ animation: "popIn 160ms ease" }}>${children}</div>`;
};

/* Auto-scrolling log pane (keeps the stick-to-bottom behavior of the old UI) */
export function useFollow(ref, deps) {
  const follow = useRef(true);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const onScroll = () => { follow.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24; };
    el.addEventListener("scroll", onScroll);
    return () => el.removeEventListener("scroll", onScroll);
  }, [ref]);
  useEffect(() => {
    const el = ref.current;
    if (el && follow.current) el.scrollTop = el.scrollHeight;
  }, deps);
}
