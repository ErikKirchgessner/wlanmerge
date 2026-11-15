// WlanMerge-Oberfläche. Spricht über window.go.main.App mit dem Go-Programm
// und bekommt jede Sekunde das Ereignis "state".

import { createMock } from "./mock.js";
import { initMonster } from "./monster.js";
import { t, setLang, getLang, locale, applyStatic, FLAGS, DISCLAIMER } from "./i18n.js";

const api = window.go?.main?.App ?? createMock();
const runtime = window.runtime ?? { EventsOn: (n, f) => api.__on?.(n, f) };

const $ = (s, el = document) => el.querySelector(s);
// Nur schreiben, wenn sich der Wert geändert hat (spart Neuzeichnen).
const setText = (el, v) => { if (el.textContent !== v) el.textContent = v; };
const setHTML = (el, v) => { if (el.__html !== v) { el.__html = v; el.innerHTML = v; } };
const setStyle = (el, k, v) => { if (el.style[k] !== v) el.style[k] = v; };
const setProp = (el, k, v) => { if (el[k] !== v) el[k] = v; }; // z. B. disabled, title, checked
const esc = (s) => String(s).replace(/[&<>"']/g, (ch) => "&#" + ch.charCodeAt(0) + ";");
const h = (tag, cls, html) => { const e = document.createElement(tag); if (cls) e.className = cls; if (html != null) e.innerHTML = html; return e; };

// ---------- Symbole (Lucide-Stil) ----------
const svg = (p, w = 2) => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="${w}" stroke-linecap="round" stroke-linejoin="round">${p}</svg>`;
const I = {
  power: svg('<path d="M12 2v10"/><path d="M18.4 6.6a9 9 0 1 1-12.77.04"/>', 2.2),
  wifi: svg('<path d="M12 20h.01"/><path d="M2 8.82a15 15 0 0 1 20 0"/><path d="M5 12.86a10 10 0 0 1 14 0"/><path d="M8.5 16.43a5 5 0 0 1 7 0"/>'),
  phone: svg('<rect x="5" y="2" width="14" height="20" rx="2"/><path d="M12 18h.01"/>'),
  lan: svg('<rect x="16" y="16" width="6" height="6" rx="1"/><rect x="2" y="16" width="6" height="6" rx="1"/><rect x="9" y="2" width="6" height="6" rx="1"/><path d="M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3"/><path d="M12 12V8"/>'),
  gear: svg('<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>'),
  back: svg('<path d="m15 18-6-6 6-6"/>'),
  sun: svg('<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2m-7.07-2.93 1.41-1.41M17.66 6.34l1.41-1.41M2 12h2m16 0h2M4.93 4.93l1.41 1.41m11.32 11.32 1.41 1.41"/>'),
  moon: svg('<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>'),
  auto: svg('<circle cx="12" cy="12" r="9"/><path d="M12 3v18" /><path d="M12 3a9 9 0 0 1 0 18" fill="currentColor"/>'),
  min: svg('<path d="M5 12h14"/>'),
  x: svg('<path d="M18 6 6 18M6 6l12 12"/>'),
  split: svg('<path d="M4 12h6"/><path d="M10 12c4 0 4-5 10-5"/><path d="M10 12c4 0 4 5 10 5"/>'),
  one: svg('<path d="M4 12h16"/>'),
  gauge: svg('<path d="m12 14 4-4"/><path d="M3.34 19a10 10 0 1 1 17.32 0"/>'),
  reset: svg('<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/>'),
  chart: svg('<path d="M3 3v16a2 2 0 0 0 2 2h16"/><path d="M8 17v-5"/><path d="M13 17V8"/><path d="M18 17v-9"/>'),
  check: svg('<path d="M20 6 9 17l-5-5"/>', 2.6),
  up: svg('<path d="M12 19V5"/><path d="m5 12 7-7 7 7"/>', 2.4),
};

// ---------- Formatierung ----------
// Zahlen je nach Sprache: 123,4 (Deutsch) bzw. 123.4 (Englisch)
let nf1, nf0, nf2;
const makeFormats = () => {
  nf1 = new Intl.NumberFormat(locale(), { maximumFractionDigits: 1, minimumFractionDigits: 1 });
  nf0 = new Intl.NumberFormat(locale(), { maximumFractionDigits: 0 });
  nf2 = new Intl.NumberFormat(locale(), { maximumFractionDigits: 2 });
};
makeFormats();
const linesText = (n) => (n === 1 ? t("1 Leitung") : t("{n} Leitungen", { n }));
const mbit = (bps) => nf1.format((bps * 8) / 1e6);
const bytes = (b) => b >= 1e9 ? `${nf2.format(b / 1e9)} GB`
  : b >= 1e6 ? `${nf0.format(b / 1e6)} MB` : `${nf0.format(b / 1e3)} KB`;
// Sekunden als "12 s", "8 min" oder "1 h 20 min"
const dur = (s) => {
  s = Math.round(s || 0);
  if (s < 60) return `${s} s`;
  if (s < 3600) return `${Math.round(s / 60)} min`;
  const m = Math.round(s / 60);
  return `${Math.floor(m / 60)} h ${m % 60} min`;
};
const isPhone = (k) => k.startsWith("Handy");
const kindIcon = (k) => k === "WLAN" ? I.wifi : isPhone(k) ? I.phone : I.lan;
const STATE = { ok: ["Aktiv", "ok"], "gestört": ["Gestört", "warn"], limit: ["Limit erreicht", "bad"], aus: ["Aus", ""] };

// ---------- Zustand ----------
let S = null;          // letzter Zustand vom Go-Programm
let busyClick = false; // Schalter gedrückt, Antwort steht noch aus
let lastEventSeen = Date.now();
let bannerTimer = null;
let editingLimit = null;
let shownError = false;
let sparkMax = 1;      // Skala der Kurve (sinkt langsam, damit sie nicht springt)

// ---------- Titelleiste ----------
$("#btn-settings").innerHTML = I.gear;
$("#btn-stats").innerHTML = I.chart;
$("#btn-min").innerHTML = I.min;
$("#btn-close").innerHTML = I.x;
$("#banner-close").innerHTML = I.x;
$("#power").innerHTML = I.power;
$("#btn-min").onclick = () => api.Minimise();
$("#btn-close").onclick = () => api.Hide();
$("#btn-settings").onclick = () => showView(view === "settings" ? "main" : "settings");
$("#btn-stats").onclick = () => showView(view === "stats" ? "main" : "stats");
$("#update-chip").onclick = () => {
  const u = S?.state.update;
  if (!u) return;
  api.OpenURL(u.download || u.url); // lädt den neuen Installer im Browser herunter
  banner(t(u.download ? "Der neue Installer wird heruntergeladen – danach einfach starten." : "Die Download-Seite wurde im Browser geöffnet."));
};
$("#banner-close").onclick = () => $("#banner").classList.add("hidden");

// Drei Ansichten: Start, Statistik und Einstellungen
let view = "main";
function showView(v) {
  view = v;
  for (const n of ["main", "stats", "settings"]) $(`#view-${n}`).classList.toggle("hidden", n !== v);
  setHTML($("#btn-settings"), v === "settings" ? I.back : I.gear);
  setHTML($("#btn-stats"), v === "stats" ? I.back : I.chart);
  $("#btn-settings").title = t(v === "settings" ? "Zurück" : "Einstellungen");
  $("#btn-stats").title = t(v === "stats" ? "Zurück" : "Statistik");
  $("#btn-stats").classList.toggle("hidden", v === "settings");
  $("#btn-settings").classList.toggle("hidden", v === "stats");
  if (S) render();
}
const showSettings = (on) => showView(on ? "settings" : "main");

// Design: System → Hell → Dunkel
const themes = ["system", "light", "dark"];
$("#btn-theme").onclick = () => {
  const next = themes[(themes.indexOf(S?.state.theme || "system") + 1) % themes.length];
  if (S) S.state.theme = next;
  applyTheme(next);
  api.SetTheme(next);
};
const mq = window.matchMedia("(prefers-color-scheme: light)");
mq.addEventListener?.("change", () => applyTheme(S?.state.theme || "system"));
function applyTheme(th) {
  const tr = t;
  const light = th === "light" || (th === "system" && mq.matches);
  document.documentElement.dataset.theme = light ? "light" : "dark";
  setHTML($("#btn-theme"), th === "light" ? I.sun : th === "dark" ? I.moon : I.auto);
  $("#btn-theme").title = tr(th === "system" ? "Design: wie {os}" : th === "light" ? "Design: hell" : "Design: dunkel");
}

// ---------- Schalter ----------
// Das Monster springt auf den Knopf – im Moment der Landung wird umgeschaltet.
const monsterPress = initMonster($("#power-wrap"));
$("#power").onclick = () => {
  if (S?.state.busy || busyClick) return;
  const turningOn = !S?.state.want; // "an" heißt auch: pausiert, wartet auf zweite Leitung
  monsterPress(turningOn, () => {
    const p = $("#power");
    p.classList.add("pressed");
    setTimeout(() => p.classList.remove("pressed"), 220);
    busyClick = true;
    render();
    Promise.resolve(api.Toggle()).finally(() => setTimeout(() => { busyClick = false; }, 400));
  });
};

// ---------- Meldungen ----------
function banner(text, error = false, sticky = false) {
  const b = $("#banner");
  $("#banner-text").textContent = text;
  b.classList.toggle("error", error);
  b.classList.remove("hidden");
  clearTimeout(bannerTimer);
  if (!sticky) bannerTimer = setTimeout(() => b.classList.add("hidden"), 9000);
}

// ---------- Darstellung ----------
function render() {
  if (!S) return;
  const st = S.state;
  applyTheme(st.theme || "system");

  const busy = st.busy || (busyClick && !st.want);
  const paused = st.paused && !busy;
  const p = $("#power");
  p.classList.toggle("on", st.on);
  p.classList.toggle("paused", paused);
  p.classList.toggle("busy", busy);
  setProp(p, "title", t(st.want ? "Ausschalten" : "Einschalten"));

  const status = $("#status");
  setText(status, t(busy ? "Verbinde …" : st.on ? "Aktiv" : paused ? "Pausiert" : "Aus"));
  status.classList.toggle("on", st.on);
  status.classList.toggle("paused", paused);
  const active = st.links.filter((l) => l.state === "ok").length;
  setHTML($("#speed"), st.on ? `${mbit(st.rxRate)}<small>Mbit/s</small>` : "");
  setText($("#substatus"), st.on
    ? `↑ ${mbit(st.txRate)} Mbit/s · ${linesText(active)}${st.publicIp ? " · IP " + st.publicIp : ""}`
    : paused ? t(active ? "Nur eine Leitung hat Internet – geht von selbst weiter, sobald eine zweite da ist" : "Kein Internet – geht von selbst weiter, sobald eine Leitung da ist")
    : st.links.length > 1 ? t("{n} Leitungen bereit", { n: st.links.length }) : st.links.length === 1 ? t("1 Leitung gefunden – schließ eine zweite an") : "");
  renderSpark(st);

  const chip = $("#update-chip");
  chip.classList.toggle("hidden", !st.update);
  if (st.update) {
    setHTML(chip, `${I.up}<span>${esc(t("Update {v}", { v: st.update.version }))}</span>`);
    setProp(chip, "title", t("Neue Version {v} herunterladen", { v: st.update.version }) + (st.update.download ? "" : " (GitHub)"));
  }

  // Fehler bleiben stehen; neue Ereignisse kurz einblenden
  if (st.error) banner(st.error, true, true);
  else if (shownError) $("#banner").classList.add("hidden"); // Fehler ist behoben
  shownError = !!st.error;
  const fresh = (st.events || []).filter((e) => new Date(e.time).getTime() > lastEventSeen);
  const loud = fresh.filter((e) => e.kind !== "quiet"); // Pause/Fortsetzen nur im Protokoll
  if (fresh.length) {
    lastEventSeen = Math.max(...fresh.map((e) => new Date(e.time).getTime()));
    const last = loud[loud.length - 1];
    if (last && !st.error) banner(last.text, last.kind === "limit" || /gestört|disrupted/.test(last.text));
  }

  renderLinks(st);
  renderActive(st);
  renderSpeed(st);
  if (view === "stats") renderStats(st);
  if (view === "settings") renderSettings();
  renderOnboard(st);
}

// ---------- Live-Kurve (letzte 60 s) ----------
// Ein einziger SVG-Pfad, einmal pro Sekunde neu – keine Animation.
const sparkEls = { area: $("#spark-area"), total: $("#spark-total"), main: $("#spark-main") };
function renderSpark(st) {
  const hist = st.history || [];
  const show = st.on && hist.length > 1;
  $("#spark").classList.toggle("hidden", !show);
  if (!show) return;
  const peak = Math.max(1, ...hist.map((p) => p.total));
  sparkMax = Math.max(peak, sparkMax * 0.96);
  const W = 240, H = 44, n = hist.length;
  const x = (i) => ((i / (n - 1)) * W).toFixed(1);
  const y = (v) => (H - 2 - (v / sparkMax) * (H - 6)).toFixed(1);
  const path = (k) => hist.map((p, i) => `${i ? "L" : "M"}${x(i)},${y(p[k])}`).join("");
  const tot = path("total");
  sparkEls.area.setAttribute("d", `${tot}L${W},${H}L0,${H}Z`);
  sparkEls.total.setAttribute("d", tot);
  sparkEls.main.setAttribute("d", path("main"));
  const prim = st.links.find((l) => l.primary);
  setText($("#spark-main-lbl"), t("nur {name}", { name: prim ? t(prim.name) : t("Hauptleitung") }));
}

// ---------- Statistik ----------
// Jede Leitung hat eine feste Farbe (Reihenfolge wie die Kacheln). Tage von
// vor 1.2.0 kennen nur "Hauptleitung" und "zusätzlich" (~main/~extra).
const PALETTE = ["#4f6bff", "#14c8b4", "#f59e0b", "#a78bfa", "#f472b6", "#94a3b8"];
const OLD = { "~main": ["Hauptleitung", "color-mix(in srgb, var(--accent) 45%, var(--surface-2))"], "~extra": ["zusätzlich", "color-mix(in srgb, var(--accent-2) 45%, var(--surface-2))"] };
const lineLabel = (n) => OLD[n] ? t(OLD[n][0]) : t(n);
function lineOrder(st, names) {
  const pos = new Map(st.links.map((l, i) => [l.name, i]));
  const tot = (n) => (st.stats?.month?.lines || {})[n] || 0;
  return [...names].filter((n) => !OLD[n]).sort((a, b) =>
    (pos.has(a) ? pos.get(a) : 99) - (pos.has(b) ? pos.get(b) : 99) || tot(b) - tot(a) || a.localeCompare(b));
}
const chart = $("#chart");
function renderStats(st) {
  const s = st.stats || {};
  const day = (d) => ({ main: d?.main || 0, extra: d?.extra || 0, saved: d?.saved || 0, lines: d?.lines || {} });
  const td = day(s.today), mo = day(s.month);
  const dm = new Intl.DateTimeFormat(locale(), { day: "2-digit", month: "2-digit" });
  const periodLbl = s.since ? t("Seit dem {d}", { d: dm.format(new Date(s.since + "T12:00:00")) }) : t("Letzte 30 Tage");
  setText($("#k-today"), bytes(td.main + td.extra));
  setText($("#k-today-x"), t("davon {v} zusätzlich", { v: bytes(td.extra) }));
  setText($("#k-month-lbl"), periodLbl);
  setText($("#k-month"), bytes(mo.main + mo.extra));
  setText($("#k-month-x"), t("davon {v} zusätzlich", { v: bytes(mo.extra) }));
  setText($("#k-saved"), dur(mo.saved));
  setText($("#k-saved-x"), t("heute {v}", { v: dur(td.saved) }));

  // Farben: alle Leitungen aus Zeitraum und Balken, in fester Reihenfolge
  const days = s.days || [];
  const names = new Set(Object.keys(mo.lines));
  days.forEach((d) => Object.keys(d.lines || {}).forEach((n) => names.add(n)));
  const order = lineOrder(st, names);
  const color = (n) => OLD[n] ? OLD[n][1] : PALETTE[order.indexOf(n) % PALETTE.length];
  const olds = Object.keys(OLD).filter((n) => names.has(n));

  // Je Leitung im Zeitraum
  setText($("#per-line-lbl"), t("Je Leitung · {p}", { p: periodLbl }));
  const sum = Math.max(1, mo.main + mo.extra);
  const rows = [...order, ...olds].filter((n) => mo.lines[n] > 0).sort((a, b) => mo.lines[b] - mo.lines[a]);
  setHTML($("#per-line"), rows.length ? rows.map((n) => {
    const v = mo.lines[n];
    return `<div class="pl-row"><i class="pl-dot" style="background:${color(n)}"></i><span class="pl-n">${esc(lineLabel(n))}</span>` +
      `<div class="bar pl-bar"><i style="transform:scaleX(${v / sum});background:${color(n)}"></i></div>` +
      `<b class="pl-v">${bytes(v)}</b><span class="pl-p">${nf0.format((v / sum) * 100)} %</span></div>`;
  }).join("") : `<div class="pl-empty">${esc(t("Noch nichts gezählt – die Statistik füllt sich, sobald WlanMerge an ist."))}</div>`);

  // 14-Tage-Balken, je Leitung gestapelt
  if (chart.children.length !== days.length) {
    chart.innerHTML = days.map(() => `<div class="col"><div class="stack"></div><span></span></div>`).join("");
  }
  const max = Math.max(1, ...days.map((d) => d.main + d.extra));
  const wd = new Intl.DateTimeFormat(locale(), { weekday: "short" });
  const stackOrder = [...olds].reverse().concat([...order].reverse()); // Hauptleitung unten
  days.forEach((d, i) => {
    const col = chart.children[i];
    const date = new Date(d.date + "T12:00:00");
    const ls = d.lines || {};
    setHTML($(".stack", col), stackOrder.filter((n) => ls[n] > 0)
      .map((n) => `<i style="height:${((ls[n] / max) * 100).toFixed(2)}%;background:${color(n)}"></i>`).join(""));
    setText($("span", col), i === days.length - 1 ? t("heute") : wd.format(date));
    const parts = Object.keys(ls).filter((n) => ls[n] > 0).map((n) => `${lineLabel(n)} ${bytes(ls[n])}`);
    setProp(col, "title", `${dm.format(date)}: ${bytes(d.main + d.extra)}${parts.length ? " · " + parts.join(", ") : ""}`);
  });
  setHTML($("#chart-legend"), [...order, ...olds].map((n) => `<span class="lg"><i style="background:${color(n)}"></i>${esc(lineLabel(n))}</span>`).join(""));
}
const cards = new Map();

function renderLinks(st) {
  const box = $("#links");
  if (!st.links.length) {
    setHTML(box, `<div class="empty">${t("Keine Internetverbindung gefunden.<br>Verbinde WLAN, LAN oder dein Handy per USB-Tethering.")}</div>`);
    cards.clear();
    return;
  }
  if (box.querySelector(".empty")) { box.querySelector(".empty").remove(); box.__html = null; }
  const names = new Set(st.links.map((l) => l.name));
  for (const [n, c] of cards) if (!names.has(n)) { c.el.remove(); cards.delete(n); }
  st.links.forEach((l, i) => {
    let c = cards.get(l.name);
    if (!c) { c = makeCard(l); cards.set(l.name, c); }
    if (box.children[i] !== c.el) box.insertBefore(c.el, box.children[i] || null);
    updateCard(c, l, st);
  });
}

function makeCard(l) {
  const el = h("div", "card");
  el.innerHTML = `
    <button class="kind-icon${l.kind.startsWith("Handy") ? " phone" : ""}">${kindIcon(l.kind)}<span class="dot"></span></button>
    <div class="card-title"><b class="n"></b><small class="sub"></small></div>
    <div class="metrics"><span class="down"></span><span class="up"></span></div>
    <div class="share">
      <div class="line"><span>${t("Anteil")}</span><b class="share-val"></b></div>
      <div class="bar"><i></i></div>
    </div>
    <div class="usage">
      <div class="line"><span class="used"></span><button class="limit-btn"></button><button class="reset-btn" title="${t("Verbrauch zurücksetzen")}">${I.reset.replace("<svg", '<svg width="12" height="12"')}</button></div>
      <div class="bar usage-bar"><i></i></div>
    </div>`;
  const c = { el, name: l.name };
  $(".n", el).textContent = l.name;
  $(".n", el).title = l.name;
  // Symbol = Schalter: das Monster springt drauf und drückt es (wie beim Ein/Aus-Knopf)
  const ic = $(".kind-icon", el);
  ic.onclick = () => {
    const cur = S?.state.links.find((x) => x.name === l.name);
    const on = !!cur?.disabled; // war aus → einschalten
    monsterPress(on, () => {
      el.classList.toggle("off", !on); // sofort zeigen, Go bestätigt gleich
      ic.classList.add("pressed");
      setTimeout(() => ic.classList.remove("pressed"), 220);
      ic.classList.remove("pop"); void ic.offsetWidth; ic.classList.add("pop");
      api.SetEnabled(l.name, on);
    }, ic);
  };
  ic.onanimationend = (e) => { if (e.animationName === "icon-pop") ic.classList.remove("pop"); };
  $(".reset-btn", el).onclick = () => { if (confirm(t("Verbrauch von „{name}\" auf 0 setzen?", { name: l.name }))) api.ResetUsage(l.name); };
  $(".limit-btn", el).onclick = () => editLimit(c);
  return c;
}

function updateCard(c, l, st) {
  const el = c.el;
  const [label, cls] = STATE[l.state] || [l.state, ""];
  el.classList.toggle("off", l.disabled);
  // Status als Punkt am Symbol; nur Störungen stehen als Text da
  $(".dot", el).className = "dot " + (l.disabled ? "" : st.on ? cls : l.state === "ok" ? "" : cls);
  setProp($(".kind-icon", el), "title", l.disabled ? t("{name}: aus – antippen zum Einschalten", { name: l.name })
    : t("{name}: {state} – antippen zum Ausschalten", { name: l.name, state: st.on ? label : l.state === "ok" ? t("bereit") : label }));
  const problem = !l.disabled && l.state !== "ok" ? label : null;
  setText($(".sub", el), problem || [l.kind !== l.name ? t(l.kind) : null, st.on && l.rttMs ? `${l.rttMs} ms` : null].filter(Boolean).join(" · ") || " ");
  $(".sub", el).classList.toggle("warn", !!problem);

  $(".metrics", el).classList.toggle("hidden", !st.on);
  setHTML($(".down", el), `↓ ${mbit(l.rxRate)}<small>Mbit/s</small>`);
  setHTML($(".up", el), `↑ ${mbit(l.txRate)}`);

  // Anteil: was die Leitung gerade trägt (aus: was sie tragen würde)
  const ok = l.state === "ok" && !l.disabled;
  const share = st.on ? l.share : l.target;
  setText($(".share-val", el), ok ? `${nf0.format(share)} %` : "—");
  setStyle($(".share .bar i", el), "transform", `scaleX(${(ok ? share : 0) / 100})`);

  // Verbrauch & Limit – für jede Leitung
  setHTML($(".used", el), `<b>${bytes(l.used)}</b>${l.limit > 0 ? ` / ${bytes(l.limit)}` : ""}`);
  if (editingLimit !== l.name) { // während der Eingabe ist der Knopf durch das Eingabefeld ersetzt
    const lb = $(".limit-btn", el);
    setText(lb, t(l.limit > 0 ? "Limit" : "+ Limit"));
    setProp(lb, "title", t(l.limit > 0 ? "Limit ändern" : "Limit setzen"));
  }
  const ub = $(".usage-bar", el);
  const pct = l.limit > 0 ? Math.min(100, (l.used / l.limit) * 100) : 0;
  ub.classList.toggle("none", !(l.limit > 0));
  ub.classList.toggle("warn", pct >= 80 && pct < 100);
  ub.classList.toggle("bad", pct >= 100);
  setStyle($("i", ub), "transform", `scaleX(${pct / 100})`);
}

function editLimit(c) {
  const l = S.state.links.find((x) => x.name === c.name);
  editingLimit = c.name;
  const btn = $(".limit-btn", c.el);
  const input = h("input", "limit-input");
  input.type = "number"; input.min = "0"; input.step = "0.5";
  input.placeholder = "GB";
  input.value = l.limit > 0 ? +(l.limit / 1e9).toFixed(2) : "";
  input.title = t("Limit in GB · leer oder 0 = kein Limit · Enter speichert");
  btn.replaceWith(input);
  input.focus(); input.select();
  const done = (save) => {
    if (editingLimit !== c.name) return;
    editingLimit = null;
    input.replaceWith(btn);
    if (save) api.SetLimitGB(c.name, Math.max(0, parseFloat(String(input.value).replace(",", ".")) || 0));
  };
  input.onkeydown = (e) => { if (e.key === "Enter") done(true); if (e.key === "Escape") done(false); };
  input.onblur = () => done(true);
}

// ---------- Laufende Browser-Downloads (nur Anzeige) ----------
// Eine dezente Zeile auf der Startseite zeigt, dass WlanMerge gerade einen
// Browser-Download beschleunigt – sonst ist davon nichts zu sehen.
function renderActive(st) {
  const list = st.downloads || [];
  const box = $("#dl-active");
  if (!list.length) { setHTML(box, ""); return; }
  setHTML(box, list.map((d) => {
    const pct = d.size > 0 ? Math.min(100, (d.done / d.size) * 100) : 0;
    const lines = linesText(d.lines);
    return `<div class="dl-row"><span class="dl-ic">${d.lines > 1 ? I.split : I.one}</span><span class="dl-n"></span><span class="dl-v">${lines} · ${mbit(d.speed)} Mbit/s · ${nf0.format(pct)} %</span></div>`;
  }).join(""));
  box.querySelectorAll(".dl-n").forEach((el, i) => {
    const d = list[i];
    el.textContent = d.name;
    el.title = `${d.name} – ${d.mode}`;
  });
}
// ---------- Speedtest ----------
// Ein Klick startet den Test und klappt das Ergebnis auf; "Einklappen" macht
// es wieder klein. Ein Ergebnis kann gespeichert werden – spätere Tests zeigen
// dann den Unterschied (grün = schneller, rot = langsamer).
$(".st-ic").innerHTML = I.gauge;
$("#st-close").innerHTML = svg('<path d="m18 15-6-6-6 6"/>');
let stOpen = false;
// Der Zeitbalken ist eine reine CSS-Animation (10 s, läuft auf der Grafikkarte):
// völlig gleichmäßig und ohne Rechenlast – Go meldet sich nur jede Sekunde.
let stStart = 0;
const startTest = () => {
  if (S?.state.speedTest?.running) return;
  stOpen = true;
  stStart = performance.now();
  if (S) S.state.speedTest = { running: true, progress: 0, total: 0, lines: [] };
  const prog = $("#st-prog");
  prog.classList.remove("run", "hidden");
  void prog.offsetWidth; // Animation von vorn starten
  prog.classList.add("run");
  render();
  api.SpeedTest();
};
$("#st-btn").onclick = startTest;
$("#st-again").onclick = startTest;
$("#st-close").onclick = () => { stOpen = false; render(); };
$("#st-save").onclick = () => api.SaveSpeedTest();

const diff = (now, old) => {
  if (old == null) return "";
  const d = ((now - old) * 8) / 1e6;
  if (Math.abs(d) < 0.05) return `<span class="diff">±0</span>`;
  return `<span class="diff ${d > 0 ? "up" : "down"}">${d > 0 ? "+" : "−"}${nf1.format(Math.abs(d))}</span>`;
};
const when = (iso) => new Date(iso).toLocaleString(locale(), { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });

function renderSpeed(st) {
  let r = st.speedTest;
  if (stStart && !r?.running && performance.now() - stStart < 1500) r = { running: true, progress: 0, total: 0, lines: [] }; // alter Stand direkt nach dem Start
  if (!r?.running && stStart) { stStart = 0; $("#st-prog").classList.add("hidden"); $("#st-prog").classList.remove("run"); }
  const saved = st.speedSaved;
  const canTest = st.links.some((l) => l.state === "ok");
  $("#st").classList.toggle("open", stOpen && !!r);
  const btn = $("#st-btn");
  setProp(btn, "disabled", !canTest);
  setText($("#st-label"), r?.running ? t("Speedtest läuft …")
    : r && !r.error ? `Speedtest · ${mbit(r.total)} Mbit/s` : "Speedtest");
  setProp($("#st-again"), "disabled", !!r?.running || !canTest);
  const isSaved = saved && r && !r.running && saved.at === r.at;
  const save = $("#st-save");
  setProp(save, "disabled", !r || r.running || !!r.error || isSaved);
  setText(save, t(isSaved ? "Gespeichert ✓" : saved ? "Als Vergleich speichern" : "Ergebnis speichern"));
  if (!r) return;
  const body = $("#st-body");
  if (r.error) {
    setHTML(body, `<div class="st-note" style="color:var(--bad)"></div>`);
    body.firstChild.textContent = r.error;
    return;
  }
  // Während des Tests nichts neu zeichnen – nur der animierte Balken läuft.
  if (r.running) {
    setHTML(body, `<div class="st-note">${t("Misst alle Leitungen gleichzeitig … (ca. 10 s, Handy höchstens 150 MB)")}</div>`);
    return;
  }
  const lines = r.lines || [];
  const max = Math.max(1, ...lines.map((l) => l.rate));
  const cmp = !r.running && saved && !isSaved ? saved : null; // nur fertige Tests vergleichen
  let html = `<div class="st-total"><b>${mbit(r.total)}<small>Mbit/s</small></b>${cmp ? diff(r.total, cmp.total) : ""}<span class="lbl">${t(r.running ? "zusammen (live)" : "zusammen")}</span></div>`;
  for (const l of lines) {
    const v = l.error && !l.rate ? t("Fehler") : `${mbit(l.rate)} Mbit/s`;
    html += `<div class="st-line${l.error ? " err" : ""}"><span class="n"></span><div class="bar"><i style="transform:scaleX(${l.rate / max})"></i></div><span class="v">${v}${cmp ? diff(l.rate, cmp.lines?.[l.name]) : ""}</span></div>`;
  }
  const notes = [];
  if (r.running) notes.push(t("Handy: höchstens 150 MB Datenvolumen."));
  else if (lines.length) {
    const first = lines[0];
    if (lines.length > 1 && first.rate > 0) {
      notes.push(t("Nur {name} wären etwa {v} Mbit/s – mit WlanMerge <b>+{p} %</b>.", { name: esc(first.name), v: mbit(first.rate), p: nf0.format(Math.max(0, (r.total / first.rate - 1) * 100)) }));
    } else if (lines.length === 1) {
      notes.push(t("Nur eine Leitung aktiv – schließ dein Handy per USB-Tethering an, dann addieren sich beide."));
    }
    const used = lines.slice(1).filter((l) => l.bytes > 0).map((l) => `${esc(l.name)} ${bytes(l.bytes)}`);
    if (used.length) notes.push(t("Verbrauch für den Test: {list}.", { list: used.join(", ") }));
    if (cmp) notes.push(t("Vergleich mit gespeichertem Ergebnis vom {when} (Mbit/s).", { when: when(cmp.at) }));
    else if (isSaved) notes.push(t("Dieses Ergebnis ist gespeichert – der nächste Test zeigt den Unterschied."));
  }
  if (notes.length) html += `<div class="st-note">${notes.join("<br>")}</div>`;
  setHTML(body, html);
  body.querySelectorAll(".st-line .n").forEach((el, i) => { el.textContent = lines[i].name; el.title = lines[i].error || lines[i].name; });
}
// ---------- Einstellungen ----------
const resetSel = $("#set-resetday");
const fillResetDays = () => {
  resetSel.innerHTML = `<option value="0">${t("nie")}</option>` +
    Array.from({ length: 28 }, (_, i) => `<option value="${i + 1}">${t("am {d}.", { d: i + 1 })}</option>`).join("");
};
fillResetDays();

// Sprache: Flaggen-Knöpfe
$("#lang-de").innerHTML = FLAGS.de;
$("#lang-en").innerHTML = FLAGS.en;
$("#lang-de").onclick = () => chooseLang("de");
$("#lang-en").onclick = () => chooseLang("en");
function chooseLang(l) {
  if (S) S.state.lang = l;
  applyLang(l, S?.os);
  api.SetLanguage(l);
}
// applyLang stellt die ganze Oberfläche um (feste Texte, Zahlenformat, Kacheln).
function applyLang(l, os) {
  if (!setLang(l, os)) return;
  makeFormats();
  applyStatic();
  fillResetDays();
  $("#btn-settings").title = t(view === "settings" ? "Zurück" : "Einstellungen");
  $("#legal-body").innerHTML = DISCLAIMER[getLang()];
  cards.forEach((c) => c.el.remove()); // Kacheln mit neuen Texten neu aufbauen
  cards.clear();
  fillObReset();
  $("#ob-legal-text").innerHTML = DISCLAIMER[getLang()];
  $("#app-add").placeholder = t("z. B. steam.exe");
  $("#btn-stats").title = t(view === "stats" ? "Zurück" : "Statistik");
  $("#links").__html = null;
  $("#st-btn").__html = null;
  if (S) render();
}
$("#set-legal").onclick = () => $("#legal-text").classList.toggle("hidden");

function renderSettings() {
  const st = S.state;
  setProp($("#set-autoconnect"), "checked", st.autoConnect);
  setProp($("#set-autostart"), "checked", S.autostart);
  setProp($("#set-autopause"), "checked", st.autoPause);
  setProp($("#set-rebalance"), "checked", st.rebalance);
  if (document.activeElement !== resetSel) resetSel.value = String(st.resetDay ?? 1);
  setProp($("#set-dl-enabled"), "checked", st.dlEnabled);
  setProp($("#set-appfilter"), "checked", st.appFilter);
  setProp($("#set-update"), "checked", st.updateCheck);
  setText($("#version"), t("Version {v} · MIT-Lizenz · Danke fürs Testen!", { v: st.version || "–" }));
  $("#lang-de").classList.toggle("on", getLang() === "de");
  $("#lang-en").classList.toggle("on", getLang() === "en");
  renderApps(st);
}

// ---------- Programme, die das Handy nutzen dürfen ----------
const appRows = new Map();
// Anzeigename: bekannte Programme mit Namen, sonst Dateiname ohne ".exe"
const appName = (a) => a.label ? a.label
  : a.name === "browser-downloads" ? t("Browser-Downloads (über WlanMerge)")
  : a.name === "system" ? t("Windows (System)")
  : a.name.replace(/\.exe$/, "").replace(/^./, (c) => c.toUpperCase());
const appFiles = (a) => a.name === "browser-downloads" || a.name === "system" ? "" : (a.files || [a.name]).join(", ");
function renderApps(st) {
  $("#apps").classList.toggle("hidden", !st.appFilter);
  if (!st.appFilter) return;
  const list = st.apps || [];
  const box = $("#app-list");
  const names = new Set(list.map((a) => a.name));
  for (const [n, r] of appRows) if (!names.has(n)) { r.remove(); appRows.delete(n); }
  list.forEach((a, i) => {
    let r = appRows.get(a.name);
    if (!r) {
      r = h("label", "app", `<input type="checkbox" class="check"/><span class="an"></span><small class="af"></small><span class="ab"></span>`);
      $("input", r).onchange = (e) => api.SetPhoneApp(a.name, e.target.checked);
      appRows.set(a.name, r);
    }
    if (box.children[i] !== r) box.insertBefore(r, box.children[i] || null);
    setText($(".an", r), appName(a));
    setText($(".af", r), appFiles(a));
    setProp(r, "title", appFiles(a));
    setText($(".ab", r), a.bytes ? (a.phone ? t("{v} · davon Handy {p}", { v: bytes(a.bytes), p: bytes(a.phone) }) : bytes(a.bytes)) : "");
    setProp($("input", r), "checked", a.allowed);
  });
}
const addApp = () => {
  const inp = $("#app-add");
  let n = inp.value.trim().toLowerCase();
  if (!n) return;
  if (!/\.exe$/.test(n)) n += ".exe";
  inp.value = "";
  api.SetPhoneApp(n, true);
};
$("#app-add-btn").onclick = addApp;
$("#app-add").onkeydown = (e) => { if (e.key === "Enter") addApp(); };

$("#set-autoconnect").onchange = (e) => api.SetAutoConnect(e.target.checked);
$("#set-autopause").onchange = (e) => api.SetAutoPause(e.target.checked);
$("#set-appfilter").onchange = (e) => api.SetAppFilter(e.target.checked);
$("#set-update").onchange = (e) => api.SetUpdateCheck(e.target.checked);
$("#set-issue").onclick = () => api.OpenURL("https://github.com/ErikKirchgessner/wlanmerge/issues/new");
$("#set-diag").onclick = async () => {
  const b = $("#set-diag");
  const err = await api.CopyDiagnose();
  b.textContent = err ? t("Fehler") : t("Kopiert ✓");
  b.title = err || "";
  setTimeout(() => { b.textContent = t("Kopieren"); }, 2500);
};
$("#set-autostart").onchange = (e) => api.SetAutostart(e.target.checked);
$("#set-rebalance").onchange = (e) => api.SetRebalance(e.target.checked);
resetSel.onchange = () => api.SetResetDay(+resetSel.value);
$("#set-dl-enabled").onchange = (e) => api.SetDownloadsEnabled(e.target.checked);
$("#set-ext").onclick = () => { api.OpenExtensionFolder(); $("#ext-help").classList.remove("hidden"); };
$("#emergency").onclick = async () => {
  const steps = await api.Emergency();
  showSettings(false);
  banner(t("Netzwerk zurückgesetzt: {step}", { step: steps?.length ? steps[steps.length - 1] : t("fertig") }));
};

// ---------- Einrichtung beim ersten Start ----------
// 1. Willkommen  2. Handy anschließen (live erkannt)  3. Datenlimit + einschalten
let obStep = 0;
let obDone = false; // schon abgeschlossen (bis Go es bestätigt)
let obAuto = false; // automatisch weiter nach "Handy erkannt" (nur einmal)
const obReset = $("#ob-resetday");
const fillObReset = () => {
  const v = obReset.value;
  obReset.innerHTML = resetSel.innerHTML;
  obReset.value = v || String(S?.state.resetDay ?? 1);
};
fillObReset();
const obGo = (n) => {
  obStep = n;
  if (n === 2) buildObLimits();
  if (S) renderOnboard(S.state);
};
$("#ob-next0").onclick = () => obGo(1);
$("#ob-back1").onclick = () => obGo(0);
$("#ob-next1").onclick = () => obGo(2);
$("#ob-legal").onclick = () => $("#ob-legal-text").classList.toggle("hidden");

// Für jedes erkannte Handy ein Eingabefeld (einmal beim Betreten von Schritt 3).
function buildObLimits() {
  const phones = (S?.state.links || []).filter((l) => isPhone(l.kind));
  const box = $("#ob-limits");
  if (!phones.length) {
    box.innerHTML = `<div class="ob-note">${t("Kein Handy angeschlossen – das Limit kannst du später an der Kachel setzen.")}</div>`;
    return;
  }
  box.innerHTML = phones.map(() => `<label class="ob-row"><span class="ob-n"></span><span class="ob-gb"><input type="number" min="0" step="0.5" class="text-input num"/> GB</span></label>`).join("");
  box.querySelectorAll(".ob-row").forEach((row, i) => {
    const l = phones[i];
    row.dataset.name = l.name;
    $(".ob-n", row).textContent = l.name;
    $("input", row).value = l.limit > 0 ? +(l.limit / 1e9).toFixed(2) : "";
    $("input", row).placeholder = t("kein Limit");
  });
  obReset.value = String(S?.state.resetDay ?? 1);
}

const finishOb = (turnOn) => {
  $("#ob-limits").querySelectorAll(".ob-row[data-name]").forEach((row) => {
    const gb = Math.max(0, parseFloat(String($("input", row).value).replace(",", ".")) || 0);
    api.SetLimitGB(row.dataset.name, gb);
  });
  api.SetResetDay(+obReset.value);
  obDone = true;
  if (turnOn) busyClick = true;
  api.FinishOnboarding(turnOn);
  render();
};
$("#ob-finish").onclick = () => finishOb(true);
$("#ob-later").onclick = () => finishOb(false);

function renderOnboard(st) {
  const show = !st.onboarded && !obDone;
  $("#onboard").classList.toggle("hidden", !show);
  if (!show) return;
  document.querySelectorAll(".ob-step").forEach((s) => s.classList.toggle("active", +s.dataset.step === obStep));
  document.querySelectorAll(".ob-dots i").forEach((d, i) => d.classList.toggle("on", i <= obStep));
  if (obStep !== 1) return;
  const phones = st.links.filter((l) => isPhone(l.kind) && l.state === "ok");
  const others = st.links.filter((l) => !isPhone(l.kind));
  let html = phones.length
    ? `<div class="ob-found ok"><span class="ob-ic">${I.check}</span><b>${esc(t("Handy erkannt"))}</b><small></small></div>`
    : `<div class="ob-found"><span class="ob-ic">${I.phone}</span><b>${esc(t("Noch kein Handy erkannt"))}</b><small>${esc(t("Das geht meist in wenigen Sekunden, nachdem USB-Tethering an ist."))}</small></div>`;
  if (others.length) html += `<div class="ob-note">${esc(t("Außerdem gefunden: {list}", { list: others.map((l) => t(l.name)).join(", ") }))}</div>`;
  const box = $("#ob-detect");
  setHTML(box, html);
  if (phones.length) setText($(".ob-found small", box), phones.map((l) => l.name).join(", "));
  setText($("#ob-next1"), t(phones.length ? "Weiter" : "Ohne Handy weiter"));
  if (phones.length && !obAuto) {
    obAuto = true; // kurz das grüne Häkchen zeigen, dann weiter
    setTimeout(() => { if (obStep === 1) obGo(2); }, 1500);
  }
}

// ---------- Tastatur: Esc = zurück ----------
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape" || e.defaultPrevented) return;
  if (/^(INPUT|SELECT|TEXTAREA)$/.test(e.target.tagName)) return; // Eingabefelder haben ihr eigenes Esc
  if (!$("#onboard").classList.contains("hidden")) return;
  if (view !== "main") showView("main");
  else if (stOpen) { stOpen = false; render(); }
  else $("#banner").classList.add("hidden");
});

// ---------- Minimiert: schlafen ----------
// Minimiert sieht niemand hin – Animationen anhalten. Beim Wiederherstellen
// sofort den aktuellen Stand holen. (Bündelung und Tray laufen im Dienst
// davon unberührt weiter.)
const sleep = (on) => {
  document.documentElement.classList.toggle("asleep", on);
  if (!on) Promise.resolve(api.GetState()).then(onState);
};
document.addEventListener("visibilitychange", () => sleep(document.hidden));
runtime.EventsOn("sleep", sleep);

// ---------- Start ----------
// Feste Texte einmal sofort (Deutsch), damit nichts leer bleibt
applyStatic();
$("#legal-body").innerHTML = DISCLAIMER.de;
$("#ob-legal-text").innerHTML = DISCLAIMER.de;
const onState = (s) => {
  if (s.state.busy || s.state.error || s.state.want !== S?.state.want) busyClick = false;
  S = s;
  applyLang(s.state.lang, s.os); // tut nichts, wenn sich nichts geändert hat
  render();
};
runtime.EventsOn("state", onState);
Promise.resolve(api.GetState()).then(onState);
