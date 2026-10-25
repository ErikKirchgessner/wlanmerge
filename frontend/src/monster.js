// Das WlanMerge-Monster: ploppt beim Ein-/Ausschalten aus dem Knopf, springt
// darauf, drückt ihn mit seinem Gewicht ein und verschwindet wieder.
// Es hat keine weiteren Funktionen – reine Freude.

// Pixel-Vorlage (20 × 17): # = Umrandung, x = Lila, o = Glanz, e = Auge
const SPRITE = [
  "...###........###...",
  "...#o####..####o#...",
  "..#####o#..#o#####..",
  "..#oo##o####o##oo#..",
  "###o##oooooooo##o###",
  "#x###xxxxxxxxxx###x#",
  "#x##xxxxxxxxxxxx##x#",
  "#x##xxeexxxxeexx##x#",
  "#x##xxeexxxxeexx##x#",
  "##xxxxeexxxxeexxxx##",
  ".###xxxxxxxxxxxx###.",
  "..##xxxxxxxxxxxx##..",
  "..####xx####xx####..",
  ".....#xx#..#xx#.....",
  "...######..######...",
  "...#x#........#x#...",
  "...###........###...",
];

// Die Augen sind eigene Flächen, damit das Monster blinzeln kann.
function buildSVG() {
  const runs = { "#": [], x: [], o: [], e: [] };
  SPRITE.forEach((row, y) => {
    let x = 0;
    while (x < row.length) {
      const c = row[x];
      let n = 1;
      while (row[x + n] === c) n++;
      if (runs[c]) runs[c].push(`<rect x="${x}" y="${y}" width="${n}" height="1"/>`);
      x += n;
    }
  });
  return `<svg viewBox="0 0 20 17" shape-rendering="crispEdges" xmlns="http://www.w3.org/2000/svg">
    <defs><linearGradient id="m-body" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#9478c6"/><stop offset="1" stop-color="#6d4fa3"/>
    </linearGradient></defs>
    <g fill="#1d1b24">${runs["#"].join("")}</g>
    <g fill="url(#m-body)">${runs.x.join("")}${runs.e.join("")}</g>
    <g fill="#ab98d4">${runs.o.join("")}</g>
    <g class="m-eyes" fill="#1d1b24">${runs.e.join("")}</g>
  </svg>`;
}

const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
let busy = false;

// Bewegungsablauf. Werte = Abstand Füße ↔ Oberkante des Ziels (top = halbe
// Zielhöhe), mit der Monstergröße skaliert (s) – so wirkt es auf dem großen
// Ein/Aus-Knopf genauso wie auf den kleinen WLAN-/Handy-Symbolen.
// Squash & Stretch: beim Steigen/Fallen gestreckt, beim Aufprall platt.
const IMPACT = 0.52;
const makeFrames = (top, s) => {
  const y = (d) => `translateY(${-(top + d * s)}px)`;
  return [
    { offset: 0, transform: `${y(-12)} scale(0, 0)`, easing: "cubic-bezier(.2,.9,.3,1.2)" },
    { offset: 0.16, transform: `${y(22)} scale(.9, 1.14)`, easing: "ease-out" },
    { offset: 0.3, transform: `${y(28)} scale(1, 1)`, easing: "cubic-bezier(.55,0,.9,.45)" },
    { offset: 0.47, transform: `${y(2)} scale(.92, 1.12)`, easing: "ease-out" },
    { offset: IMPACT, transform: `${y(-7)} scale(1.24, .72)`, easing: "ease-out" },
    { offset: 0.62, transform: `${y(-1)} scale(.95, 1.07)`, easing: "ease-in-out" },
    { offset: 0.7, transform: `${y(-2)} scale(1, 1)`, easing: "ease-in-out" },
    { offset: 0.84, transform: `${y(18)} scale(1.05, .96)`, easing: "cubic-bezier(.5,0,.75,0)" },
    { offset: 1, transform: `${y(-10)} scale(0, 0)` },
  ];
};
const DURATION = 1150; // 15 % schneller als zuvor (1350 ms)

export function initMonster(wrap) {
  // Eigene Ebene über dem ganzen Fenster, damit das Monster beim Springen
  // nicht vom Scrollbereich abgeschnitten wird.
  const layer = document.createElement("div");
  layer.className = "monster-layer";
  document.body.append(layer);
  const m = document.createElement("div");
  m.className = "monster";
  m.setAttribute("aria-hidden", "true");
  // Bewegungsstreifen: unter dem Monster beim Hochspringen, über ihm beim Fallen.
  m.innerHTML = buildSVG() +
    "<div class='m-trail below'><i></i><i></i><i></i></div><div class='m-trail above'><i></i><i></i><i></i></div>";
  const shadow = document.createElement("div");
  shadow.className = "monster-shadow";
  layer.append(shadow, m);

  // press(turningOn, onImpact, target): spielt die Animation auf target (Standard:
  // der Ein/Aus-Knopf); onImpact wird genau im Moment des Aufpralls aufgerufen
  // (dann schaltet der Knopf wirklich um).
  return function press(turningOn, onImpact, target = wrap) {
    if (busy) return false;
    if (reduceMotion.matches) {
      onImpact();
      return true;
    }
    busy = true;
    const r = target.getBoundingClientRect();
    Object.assign(layer.style, { left: r.left + "px", top: r.top + "px", width: r.width + "px", height: r.height + "px" });
    // Großer Knopf: 52 px breit (Oberkante 58 px über der Mitte); kleine Symbole: 40 px (2 px je Pixel)
    const big = target === wrap;
    const mw = big ? 52 : 40, s = mw / 52;
    Object.assign(m.style, { width: mw + "px", marginLeft: -mw / 2 + "px" });
    Object.assign(shadow.style, { width: 46 * s + "px", marginLeft: -23 * s + "px" });
    m.style.visibility = "visible";
    const anim = m.animate(makeFrames(big ? 58 : r.height / 2, s), { duration: DURATION, fill: "both" });

    // Streifen: kurz sichtbar, solange das Monster schnell steigt bzw. fällt
    m.querySelector(".m-trail.below").animate(
      [
        { opacity: 0, transform: "scaleY(.2)" },
        { opacity: 0.85, transform: "scaleY(1)", offset: 0.08 },
        { opacity: 0, transform: "scaleY(.4)", offset: 0.26 },
        { opacity: 0, transform: "scaleY(.2)", offset: 0.74 },
        { opacity: 0.55, transform: "scaleY(.6)", offset: 0.78 },
        { opacity: 0, transform: "scaleY(.3)", offset: 0.86 },
        { opacity: 0, transform: "scaleY(.2)" },
      ],
      { duration: DURATION, fill: "both" },
    );
    m.querySelector(".m-trail.above").animate(
      [
        { opacity: 0, transform: "scaleY(.2)" },
        { opacity: 0, transform: "scaleY(.2)", offset: 0.33 },
        { opacity: 0.85, transform: "scaleY(1.1)", offset: 0.45 },
        { opacity: 0, transform: "scaleY(.3)", offset: IMPACT + 0.02 },
        { opacity: 0, transform: "scaleY(.2)" },
      ],
      { duration: DURATION, fill: "both" },
    );

    // Schatten auf dem Knopf: dunkler und kleiner, je näher das Monster ist
    shadow.animate(
      [
        { opacity: 0, transform: "scale(.2)" },
        { opacity: 0.12, transform: "scale(.5)", offset: 0.3 },
        { opacity: 0.45, transform: "scale(1.15)", offset: IMPACT },
        { opacity: 0.35, transform: "scale(1)", offset: 0.7 },
        { opacity: 0.2, transform: "scale(.7)", offset: 0.84 },
        { opacity: 0, transform: "scale(.2)" },
      ],
      { duration: DURATION, fill: "both" },
    );

    // Augen: an = fröhliches Doppel-Zwinkern, aus = müde zufallen
    const eyes = m.querySelector(".m-eyes");
    const blink = turningOn
      ? [{ transform: "scaleY(1)" }, { transform: "scaleY(.15)", offset: 0.1 }, { transform: "scaleY(1)", offset: 0.2 },
         { transform: "scaleY(1)", offset: 0.35 }, { transform: "scaleY(.15)", offset: 0.45 }, { transform: "scaleY(1)", offset: 0.55 }, { transform: "scaleY(1)" }]
      : [{ transform: "scaleY(1)" }, { transform: "scaleY(.5)", offset: 0.3 }, { transform: "scaleY(.12)", offset: 0.6 }, { transform: "scaleY(.12)" }];
    eyes.animate(blink, { duration: DURATION * 0.45, delay: DURATION * (IMPACT + 0.06), fill: "both", easing: "ease-in-out" });

    setTimeout(() => {
      onImpact();
      dust(layer, turningOn, s);
      swoosh(layer, s);
    }, DURATION * IMPACT);

    anim.finished.finally(() => {
      m.style.visibility = "hidden";
      anim.cancel();
      m.getAnimations({ subtree: true }).forEach((a) => a.cancel());
      busy = false;
    });
    return true;
  };
}

// Pixel-Staub beim Aufprall: vier kleine Quadrate fliegen zur Seite.
function dust(wrap, on, k = 1) {
  const dirs = [[-1, -0.35], [-0.6, -1], [0.6, -1], [1, -0.35]];
  for (const [dx, dy] of dirs) {
    const d = document.createElement("i");
    d.className = "monster-dust" + (on ? " on" : "");
    wrap.append(d);
    const dist = (22 + Math.random() * 10) * k;
    d.animate(
      [
        { transform: "translate(-50%, 0) scale(1)", opacity: 0.9 },
        { transform: `translate(calc(-50% + ${dx * dist}px), ${dy * dist * 0.8}px) scale(.4)`, opacity: 0 },
      ],
      { duration: 420, easing: "cubic-bezier(.2,.7,.3,1)" },
    ).finished.then(() => d.remove());
  }
}

// Landung: links und rechts schießen kurze, waagerechte Wisch-Streifen weg.
function swoosh(layer, k = 1) {
  for (const side of [-1, 1]) {
    for (const [i, dy] of [[0, -6], [1, 0], [2, 6]]) {
      const s = document.createElement("i");
      s.className = "monster-swoosh";
      s.style.marginTop = `${dy - 2}px`;
      s.style.transformOrigin = side < 0 ? "100% 50%" : "0% 50%";
      layer.append(s);
      const from = side * (30 + i * 2) * k;
      const to = side * (54 + i * 6) * k;
      s.animate(
        [
          { transform: `translate(calc(-50% + ${from}px), 0) scaleX(.2)`, opacity: 0 },
          { transform: `translate(calc(-50% + ${(from + to) / 2}px), 0) scaleX(1)`, opacity: 0.8, offset: 0.35 },
          { transform: `translate(calc(-50% + ${to}px), 0) scaleX(.3)`, opacity: 0 },
        ],
        { duration: 300 + i * 40, delay: i * 25, easing: "cubic-bezier(.15,.8,.3,1)", fill: "both" },
      ).finished.then(() => s.remove());
    }
  }
}
