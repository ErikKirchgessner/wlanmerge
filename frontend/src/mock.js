// Beispieldaten, damit man die Oberfläche auch ohne das Go-Programm
// (im normalen Browser) ansehen kann. Im echten Programm unbenutzt.
//
// In der Adresse:  ?n=1 / ?n=3   so viele Leitungen
//                  ?onboard=1    Einrichtung beim ersten Start zeigen
//                  ?update=1     Update-Hinweis zeigen
//                  ?on=1         gleich eingeschaltet (mit Kurve)
//                  ?paused=1     eingeschaltet, aber pausiert (nur eine Leitung)

export function createMock() {
  const q = new URLSearchParams(location.search);
  const listeners = {};
  const st = {
    on: false, want: false, paused: false, busy: false, error: "", publicIp: "", lang: "de", autoConnect: true, autoPause: true,
    rebalance: true, resetDay: 1, theme: "system", dlEnabled: true, appFilter: false, updateCheck: true,
    version: "1.2.0", onboarded: q.get("onboard") !== "1",
    update: q.get("update") === "1" ? { version: "1.3.0", url: "https://github.com/ErikKirchgessner/wlanmerge/releases/latest", download: "https://github.com/ErikKirchgessner/wlanmerge/releases/latest/download/WlanMerge-Setup-Windows.exe" } : null,
    downloads: [{ id: "a", name: "ubuntu-24.04-desktop-amd64.iso", size: 5.8e9, done: 2.1e9, state: "läuft", lines: 2, mode: "über 2 Leitungen", speed: 14.2e6 }],
    rxRate: 0, txRate: 0, events: [],
    history: Array.from({ length: 60 }, () => ({ total: 0, main: 0 })),
    links: [
      { name: "WLAN", kind: "WLAN", state: "ok", primary: true, target: 72, share: 0, rxRate: 0, txRate: 0, rttMs: 0, conns: 0, used: 18.4e9, limit: 0, disabled: false },
      { name: "Ethernet 2", kind: "Handy (USB)", state: "ok", primary: false, target: 28, share: 0, rxRate: 0, txRate: 0, rttMs: 0, conns: 0, used: 1.24e9, limit: 2e9, disabled: false },
    ],
    apps: [
      { name: "steam.exe", label: "Steam", files: ["steam.exe"], bytes: 8.4e9, phone: 2.1e9, allowed: true },
      { name: "battle.net.exe", label: "Battle.net", files: ["battle.net.exe", "agent.exe"], bytes: 3.1e9, phone: 0.9e9, allowed: true },
      { name: "chrome.exe", label: "Google Chrome", files: ["chrome.exe"], bytes: 1.3e9, phone: 0, allowed: false },
      { name: "discord.exe", bytes: 92e6, phone: 0, allowed: false },
      { name: "onedrive.exe", bytes: 41e6, phone: 0, allowed: false },
      { name: "browser-downloads", bytes: 0, phone: 0, allowed: true },
      { name: "epicgameslauncher.exe", label: "Epic Games", files: ["epicgameslauncher.exe"], bytes: 0, phone: 0, allowed: false },
      { name: "eadesktop.exe", label: "EA app", files: ["eadesktop.exe", "eabackgroundservice.exe"], bytes: 0, phone: 0, allowed: false },
      { name: "upc.exe", label: "Ubisoft Connect", files: ["upc.exe", "ubisoftconnect.exe"], bytes: 0, phone: 0, allowed: false },
    ],
  };
  // Statistik: 14 Tage mit erfundenen Werten
  const days = [];
  for (let i = 13; i >= 0; i--) {
    const d = new Date(Date.now() - i * 864e5);
    const main = (i % 4 === 2 ? 0.4 : 1.6 + Math.sin(i) * 0.9) * 1e9, extra = (i % 3 === 0 ? 0.1 : 0.7 + Math.cos(i) * 0.4) * 1e9;
    // die ältesten Tage stammen "von vor 1.2.0" (ohne Aufteilung je Leitung)
    const lines = i > 10 ? { "~main": main, "~extra": extra } : { WLAN: main, "Ethernet 2": extra };
    days.push({ date: d.toISOString().slice(0, 10), main, extra, lines });
  }
  const since = new Date(); since.setDate(1);
  const inPeriod = days.filter((d) => d.date >= since.toISOString().slice(0, 10));
  const sum = (k) => inPeriod.reduce((s, d) => s + d[k], 0);
  const lineSum = {};
  inPeriod.forEach((d) => Object.entries(d.lines).forEach(([n, v]) => { lineSum[n] = (lineSum[n] || 0) + v; }));
  st.stats = {
    today: { main: days[13].main, extra: days[13].extra, saved: 312, lines: days[13].lines },
    month: { main: sum("main"), extra: sum("extra"), saved: 6840, lines: lineSum },
    since: since.toISOString().slice(0, 10), days,
  };

  const n = +q.get("n");
  if (n === 1) st.links.length = 1;
  for (let i = 3; i <= n; i++) {
    st.links.push({ name: `Ethernet ${i}`, kind: "LAN", state: "ok", primary: false, target: 10, share: 0, rxRate: 0, txRate: 0, rttMs: 0, conns: 0, used: 0, limit: 0, disabled: false });
  }
  let autostart = true;
  const snap = () => ({ state: structuredClone(st), autostart, os: "Windows" });
  const emit = () => (listeners.state || []).forEach((f) => f(snap()));
  let tick = 0;
  setInterval(() => {
    tick++;
    if (st.on) {
      const base = [[9e6, 2e5, 18], [3.4e6, 8e4, 41]];
      st.links.forEach((l, i) => {
        const [rx, tx, rtt] = base[i] || [2e6, 5e4, 12];
        const wave = 0.75 + 0.25 * Math.sin(tick / 6 + i) + Math.random() * 0.15;
        l.rxRate = rx * wave; l.txRate = tx; l.rttMs = rtt;
        l.used += l.rxRate + l.txRate; // wie die Adapter-Zähler: aller Verkehr
      });
      const sum = st.links.reduce((s, l) => s + l.rxRate, 0);
      st.links.forEach((l) => { l.share = (l.rxRate / sum) * 100; });
      st.rxRate = sum; st.txRate = 2.8e5;
      st.downloads.forEach((d) => { if (d.state === "läuft") d.done = Math.min(d.size, d.done + d.speed); });
    }
    st.history.shift();
    st.history.push({ total: st.on ? st.rxRate + st.txRate : 0, main: st.on ? st.links[0].rxRate + st.links[0].txRate : 0 });
    emit();
  }, 1000);
  const ok = () => Promise.resolve("");
  const turnOn = () => {
    st.want = true; st.busy = true; emit();
    setTimeout(() => {
      st.busy = false; st.on = true; st.publicIp = "203.0.113.63";
      st.events.push({ time: new Date().toISOString(), text: "WlanMerge aktiv – 2 Leitungen gebündelt" });
      emit();
    }, 1200);
  };
  if (q.get("paused") === "1") { // eingeschaltet, aber nur eine Leitung hat Internet
    st.want = st.paused = true;
    st.links[1].state = "gestört";
  }
  if (q.get("on") === "1") {
    st.want = st.on = true;
    for (let i = 0; i < 60; i++) {
      const m = 9e6 * (0.75 + 0.25 * Math.sin(i / 6)), x = 3.4e6 * (0.75 + 0.25 * Math.sin(i / 6 + 1));
      st.history[i] = { total: m + x, main: m };
    }
  }
  return {
    __on: (n, f) => (listeners[n] ||= []).push(f),
    GetState: () => Promise.resolve(snap()),
    Toggle: () => {
      if (st.want) { st.on = st.want = st.paused = false; st.links.forEach((l) => { l.rxRate = l.txRate = l.conns = 0; l.share = 0; }); emit(); return ok(); }
      turnOn();
      return ok();
    },
    FinishOnboarding: (on) => { st.onboarded = true; if (on) turnOn(); else emit(); return ok(); },
    SetLimitGB: (n, gb) => { st.links.find((l) => l.name === n).limit = gb * 1e9; emit(); return ok(); },
    ResetUsage: (n) => { st.links.find((l) => l.name === n).used = 0; emit(); return ok(); },
    SetEnabled: (n, on) => { const l = st.links.find((l) => l.name === n); l.disabled = !on; l.state = on ? "ok" : "aus"; emit(); return ok(); },
    SetResetDay: (d) => { st.resetDay = d; emit(); return ok(); }, SetAutoConnect: (v) => { st.autoConnect = v; return ok(); },
    SetAutoPause: (v) => { st.autoPause = v; emit(); return ok(); },
    SetAutostart: (v) => { autostart = v; return ok(); }, SetRebalance: (v) => { st.rebalance = v; return ok(); },
    SetAppFilter: (v) => { st.appFilter = v; emit(); return ok(); },
    SetPhoneApp: (n, v) => {
      let a = st.apps.find((x) => x.name === n);
      if (!a) st.apps.push(a = { name: n, bytes: 0, phone: 0, allowed: false });
      a.allowed = v; emit(); return ok();
    },
    SetUpdateCheck: (v) => { st.updateCheck = v; if (!v) st.update = null; emit(); return ok(); },
    SetLanguage: (l) => { st.lang = l; emit(); return ok(); },
    SetTheme: (t) => { st.theme = t; emit(); return ok(); },
    Emergency: () => { st.on = st.want = false; emit(); return Promise.resolve(["Fertig: Windows-Netzwerk ist im Originalzustand"]); },
    SpeedTest: () => {
      // wie das echte Programm: kein Zwischenstand, nur "läuft" und nach 10 s das Ergebnis
      st.speedTest = { running: true, progress: 0, total: 0, lines: [] };
      emit();
      setTimeout(() => {
        const w = 10.5e6 + Math.random() * 1e6, h = 4.6e6 + Math.random() * 6e5;
        st.speedTest = { running: false, progress: 1, total: w + h, at: new Date().toISOString(),
          lines: [{ name: "WLAN", rate: w, bytes: w * 10 }, { name: "Ethernet 2", rate: h, bytes: h * 10 }] };
        emit();
      }, 10000);
      return ok();
    },
    SaveSpeedTest: () => { const t = st.speedTest; st.speedSaved = { total: t.total, at: t.at, lines: Object.fromEntries(t.lines.map((l) => [l.name, l.rate])) }; emit(); return ok(); },
    SetDownloadsEnabled: (v) => { st.dlEnabled = v; emit(); return ok(); }, OpenExtensionFolder() {},
    CopyDiagnose: () => ok(), OpenURL: () => ok(),
    Minimise() {}, Hide() {},
  };
}
