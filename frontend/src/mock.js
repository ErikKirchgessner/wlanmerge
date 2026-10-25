// Beispieldaten, damit man die Oberfläche auch ohne das Go-Programm
// (im normalen Browser) ansehen kann. Im echten Programm unbenutzt.

export function createMock() {
  const listeners = {};
  const st = {
    on: false, busy: false, error: "", publicIp: "", lang: "de", autoConnect: true, rebalance: true, resetDay: 1, theme: "system",
    dlEnabled: true,
    downloads: [{ id: "a", name: "ubuntu-24.04-desktop-amd64.iso", size: 5.8e9, done: 2.1e9, state: "läuft", lines: 2, mode: "über 2 Leitungen", speed: 14.2e6 }],
    rxRate: 0, txRate: 0, events: [],
    links: [
      { name: "WLAN", kind: "WLAN", state: "ok", primary: true, target: 72, share: 0, rxRate: 0, txRate: 0, rttMs: 0, conns: 0, used: 18.4e9, limit: 0, disabled: false },
      { name: "Ethernet 2", kind: "Handy (USB)", state: "ok", primary: false, target: 28, share: 0, rxRate: 0, txRate: 0, rttMs: 0, conns: 0, used: 1.24e9, limit: 2e9, disabled: false },
    ],
  };
  // ?n=1, ?n=3 … in der Adresse: so viele Leitungen anzeigen (zum Ansehen)
  const n = +new URLSearchParams(location.search).get("n");
  if (n === 1) st.links.length = 1;
  for (let i = 3; i <= n; i++) {
    st.links.push({ name: `Ethernet ${i}`, kind: "LAN", state: "ok", primary: false, target: 10, share: 0, rxRate: 0, txRate: 0, rttMs: 0, conns: 0, used: 0, limit: 0, disabled: false });
  }
  let autostart = true;
  const emit = () => (listeners.state || []).forEach((f) => f({ state: structuredClone(st), autostart, os: "Windows" }));
  setInterval(() => {
    if (st.on) {
      const base = [[9e6, 2e5, 18], [3.4e6, 8e4, 41]];
      st.links.forEach((l, i) => {
        const [rx, tx, rtt] = base[i] || [2e6, 5e4, 12];
        l.rxRate = rx * (0.85 + Math.random() * 0.3); l.txRate = tx; l.rttMs = rtt;
        l.used += l.rxRate + l.txRate; // wie die Adapter-Zähler: aller Verkehr
      });
      const sum = st.links.reduce((s, l) => s + l.rxRate, 0);
      st.links.forEach((l) => { l.share = (l.rxRate / sum) * 100; });
      st.rxRate = sum; st.txRate = 2.8e5;
      st.downloads.forEach((d) => { if (d.state === "läuft") d.done = Math.min(d.size, d.done + d.speed); });
    }
    emit();
  }, 1000);
  const ok = () => Promise.resolve("");
  return {
    __on: (n, f) => (listeners[n] ||= []).push(f),
    GetState: () => Promise.resolve({ state: structuredClone(st), autostart, os: "Windows" }),
    Toggle: () => {
      if (st.on) { st.on = false; st.links.forEach((l) => { l.rxRate = l.txRate = l.conns = 0; l.share = 0; }); emit(); return ok(); }
      st.busy = true; emit();
      setTimeout(() => {
        st.busy = false; st.on = true; st.publicIp = "92.208.185.63";
        st.events.push({ time: new Date().toISOString(), text: "WlanMerge aktiv – 2 Leitungen gebündelt" });
        emit();
      }, 1200);
      return ok();
    },
    SetLimitGB: (n, gb) => { st.links.find((l) => l.name === n).limit = gb * 1e9; emit(); return ok(); },
    ResetUsage: (n) => { st.links.find((l) => l.name === n).used = 0; emit(); return ok(); },
    SetEnabled: (n, on) => { const l = st.links.find((l) => l.name === n); l.disabled = !on; l.state = on ? "ok" : "aus"; emit(); return ok(); },
    SetResetDay: (d) => { st.resetDay = d; emit(); return ok(); }, SetAutoConnect: (v) => { st.autoConnect = v; return ok(); },
    SetAutostart: (v) => { autostart = v; return ok(); }, SetRebalance: (v) => { st.rebalance = v; return ok(); },
    SetLanguage: (l) => { st.lang = l; emit(); return ok(); },
    SetTheme: (t) => { st.theme = t; emit(); return ok(); },
    Emergency: () => { st.on = false; emit(); return Promise.resolve(["Fertig: Windows-Netzwerk ist im Originalzustand"]); },
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
    Minimise() {}, Hide() {}, Quit() {},
  };
}
