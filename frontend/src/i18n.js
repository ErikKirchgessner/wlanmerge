// Übersetzungen der Oberfläche (Deutsch/Englisch).
//
// Der deutsche Text ist der Schlüssel: t("Aus") liefert auf Englisch "Off".
// {name} usw. sind Platzhalter: t("{n} Leitungen", { n: 2 }).
// Feste Texte in index.html tragen data-t (Inhalt) bzw. data-tt (Tooltip).

const EN = {
  // Titelleiste
  "Design wechseln": "Change theme",
  "Einstellungen": "Settings",
  "Zurück": "Back",
  "Minimieren": "Minimize",
  "In den Hintergrund": "Hide to background",
  "Design: wie {os}": "Theme: like {os}",
  "Design: hell": "Theme: light",
  "Design: dunkel": "Theme: dark",

  // Startseite
  "Ein/Aus": "On/Off",
  "Aus": "Off",
  "Aktiv": "Active",
  "Verbinde …": "Connecting …",
  "Einschalten": "Turn on",
  "Ausschalten": "Turn off",
  "1 Leitung": "1 line",
  "{n} Leitungen": "{n} lines",
  "{n} Leitungen bereit": "{n} lines ready",
  "1 Leitung gefunden – schließ eine zweite an": "1 line found – connect a second one",
  "Keine Internetverbindung gefunden.<br>Verbinde WLAN, LAN oder dein Handy per USB-Tethering.":
    "No internet connection found.<br>Connect Wi-Fi, LAN or your phone via USB tethering.",

  // Kacheln
  "Anteil": "Share",
  "Gestört": "Disrupted",
  "Limit erreicht": "Limit reached",
  "bereit": "ready",
  "{name}: aus – antippen zum Einschalten": "{name}: off – tap to turn on",
  "{name}: {state} – antippen zum Ausschalten": "{name}: {state} – tap to turn off",
  "WLAN": "Wi-Fi",
  "LAN": "LAN",
  "Handy (USB)": "Phone (USB)",
  "Handy (WLAN)": "Phone (Wi-Fi)",
  "Handy (LAN)": "Phone (LAN)",
  "Limit": "Limit",
  "+ Limit": "+ Limit",
  "Limit ändern": "Change limit",
  "Limit setzen": "Set limit",
  "Verbrauch zurücksetzen": "Reset usage",
  "Verbrauch von „{name}\" auf 0 setzen?": "Reset the usage of “{name}” to 0?",
  "Limit in GB · leer oder 0 = kein Limit · Enter speichert": "Limit in GB · empty or 0 = no limit · Enter saves",

  // Speedtest
  "Misst ca. 10 s alle Leitungen gleichzeitig": "Measures all lines at once for about 10 s",
  "Speedtest läuft …": "Speed test running …",
  "Ergebnis speichern": "Save result",
  "Als Vergleich speichern": "Save for comparison",
  "Gespeichert ✓": "Saved ✓",
  "Nochmal": "Again",
  "Einklappen": "Collapse",
  "zusammen": "combined",
  "zusammen (live)": "combined (live)",
  "Fehler": "Error",
  "Handy: höchstens 150 MB Datenvolumen.": "Phone: at most 150 MB of data.",
  "Misst alle Leitungen gleichzeitig … (ca. 10 s, Handy höchstens 150 MB)": "Measuring all lines at once … (about 10 s, phone at most 150 MB)",
  "Nur {name} wären etwa {v} Mbit/s – mit WlanMerge <b>+{p} %</b>.": "{name} alone would be about {v} Mbit/s – with WlanMerge <b>+{p} %</b>.",
  "Nur eine Leitung aktiv – schließ dein Handy per USB-Tethering an, dann addieren sich beide.":
    "Only one line active – connect your phone via USB tethering and both add up.",
  "Verbrauch für den Test: {list}.": "Data used by the test: {list}.",
  "Vergleich mit gespeichertem Ergebnis vom {when} (Mbit/s).": "Compared with the saved result from {when} (Mbit/s).",
  "Dieses Ergebnis ist gespeichert – der nächste Test zeigt den Unterschied.": "This result is saved – the next test shows the difference.",

  // Einstellungen
  "Sprache": "Language",
  "Automatisch einschalten": "Turn on automatically",
  "Beim Programmstart sofort bündeln": "Start bonding as soon as the app starts",
  "Mit {os} starten": "Start with {os}",
  "Startet unauffällig im Hintergrund": "Starts quietly in the background",
  "Laufende Downloads umverteilen": "Rebalance running downloads",
  "Steam, Battle.net &amp; Epic nutzen so alle Leitungen voll aus": "Lets Steam, Battle.net &amp; Epic use every line fully",
  "Handy verbinden": "Connect your phone",
  "USB-Kabel": "USB cable",
  "Am schnellsten und stabilsten. Am Handy „USB-Tethering\" einschalten.": "Fastest and most stable. Turn on “USB tethering” on your phone.",
  "Empfohlen": "Recommended",
  "WLAN-Hotspot": "Wi-Fi hotspot",
  "Braucht einen <b>zweiten</b> WLAN-Adapter am Computer (z. B. USB-WLAN-Stick), der mit dem Handy-Hotspot verbunden ist. Wird automatisch als Handy erkannt.":
    "Needs a <b>second</b> Wi-Fi adapter on the computer (e.g. a USB Wi-Fi stick) connected to the phone's hotspot. Detected as a phone automatically.",
  "Schnell": "Fast",
  "Datenvolumen": "Data usage",
  "Jeden Monat an diesem Tag, passend zu deinem Handyvertrag": "Every month on this day, to match your mobile plan",
  "nie": "never",
  "am {d}.": "on day {d}",
  "Browser-Downloads": "Browser downloads",
  "Browser-Downloads beschleunigen": "Speed up browser downloads",
  "Downloads in Chrome und Edge laden automatisch über alle Leitungen": "Downloads in Chrome and Edge automatically use all lines",
  "Browser-Erweiterung": "Browser extension",
  "Einmalig in Chrome oder Edge laden": "Load once in Chrome or Edge",
  "Einrichten": "Set up",
  "1. In Chrome <code>chrome://extensions</code> bzw. in Edge <code>edge://extensions</code> öffnen.<br>2. Oben rechts den <b>Entwicklermodus</b> einschalten.<br>3. <b>Entpackte Erweiterung laden</b> und den eben geöffneten Ordner „extension\" auswählen.<br>Fertig – Downloads laufen ab jetzt automatisch über alle Leitungen.":
    "1. Open <code>chrome://extensions</code> in Chrome or <code>edge://extensions</code> in Edge.<br>2. Turn on <b>Developer mode</b> at the top right.<br>3. Click <b>Load unpacked</b> and choose the “extension” folder that just opened.<br>Done – downloads now use all lines automatically.",
  "Notfall": "Emergency",
  "Netzwerk zurücksetzen": "Reset network",
  "Schaltet aus und stellt die Netzwerk-Einstellungen von {os} garantiert wieder her": "Turns off and reliably restores the {os} network settings",
  "Zurücksetzen": "Reset",
  "Netzwerk zurückgesetzt: {step}": "Network reset: {step}",
  "fertig": "done",
  "Rechtliches": "Legal",
  "Haftungsausschluss": "Disclaimer",
  "Lesen": "Read",
  "Gut zu wissen: Eine einzelne Verbindung kann nie auf zwei Leitungen gleichzeitig laufen. Steam, Battle.net und Epic laden über viele Verbindungen – die werden verteilt und bei Bedarf umgeschichtet. Browser-Downloads teilt WlanMerge in Stücke auf, wenn der Server das erlaubt – der Browser zeigt dann die Geschwindigkeit aller Leitungen zusammen. Spiele und Logins laufen immer stabil über die Hauptleitung (WLAN bzw. LAN).":
    "Good to know: a single connection can never use two lines at once. Steam, Battle.net and Epic download over many connections – those are spread out and moved when needed. Browser downloads are split into pieces when the server allows it – the browser then shows the speed of all lines combined. Games and logins always run steadily over the main line (Wi-Fi or LAN).",
};

let lang = "de";
let vars = { os: "Windows" };

export const getLang = () => lang;
export const locale = () => (lang === "en" ? "en-US" : "de-DE");

/** setLang stellt um; liefert true, wenn sich etwas geändert hat. */
export function setLang(l, os) {
  const next = l === "en" ? "en" : "de";
  const changed = next !== lang || (os && os !== vars.os);
  lang = next;
  if (os) vars.os = os;
  document.documentElement.lang = lang;
  return changed;
}

export function t(de, v) {
  let s = lang === "en" && EN[de] != null ? EN[de] : de;
  const all = { ...vars, ...v };
  return s.replace(/\{(\w+)\}/g, (m, k) => (all[k] != null ? String(all[k]) : m));
}

/** applyStatic übersetzt alle festen Texte in index.html. */
export function applyStatic(root = document) {
  root.querySelectorAll("[data-t]").forEach((el) => {
    el.__de ??= el.innerHTML.trim();
    el.innerHTML = t(el.__de);
  });
  root.querySelectorAll("[data-tt]").forEach((el) => {
    el.__det ??= el.getAttribute("title") || el.getAttribute("aria-label") || "";
    if (el.hasAttribute("title")) el.title = t(el.__det);
    if (el.hasAttribute("aria-label")) el.setAttribute("aria-label", t(el.__det));
  });
}

// Flaggen als kleine Grafiken (Emoji-Flaggen zeigt Windows nicht an).
export const FLAGS = {
  de: '<svg viewBox="0 0 5 3" aria-hidden="true"><rect width="5" height="1" fill="#000"/><rect y="1" width="5" height="1" fill="#DD0000"/><rect y="2" width="5" height="1" fill="#FFCE00"/></svg>',
  en: '<svg viewBox="0 0 60 30" aria-hidden="true"><clipPath id="uk-s"><path d="M0,0 v30 h60 v-30 z"/></clipPath><clipPath id="uk-t"><path d="M30,15 h30 v15 z v15 h-30 z h-30 v-15 z v-15 h30 z"/></clipPath><g clip-path="url(#uk-s)"><path d="M0,0 v30 h60 v-30 z" fill="#012169"/><path d="M0,0 L60,30 M60,0 L0,30" stroke="#fff" stroke-width="6"/><path d="M0,0 L60,30 M60,0 L0,30" clip-path="url(#uk-t)" stroke="#C8102E" stroke-width="4"/><path d="M30,0 v30 M0,15 h60" stroke="#fff" stroke-width="10"/><path d="M30,0 v30 M0,15 h60" stroke="#C8102E" stroke-width="6"/></g></svg>',
};

// Haftungsausschluss (gleicher Inhalt wie DISCLAIMER.md im Projekt).
export const DISCLAIMER = {
  de: "<b>WlanMerge ist kostenlose Software und wird „wie besehen“ bereitgestellt – ohne jede Gewährleistung.</b><br>" +
    "Die Nutzung erfolgt ausschließlich auf eigene Gefahr. WlanMerge verändert die Netzwerkeinstellungen deines Computers und leitet Datenverkehr über deine Internetverbindungen, auch über mobile Daten.<br>" +
    "Du bist selbst verantwortlich für: Kosten durch Datenvolumen (auch wenn ein eingestelltes Limit nicht greift), die Einhaltung der Vertrags- und Nutzungsbedingungen deines Mobilfunk- oder Internetanbieters (z. B. Tethering-Verbote) sowie von Spiele- und Download-Diensten, mögliche Kontosperren, Netzwerkstörungen, Datenverlust und Schäden am Gerät.<br>" +
    "Eine Haftung der Entwickler ist ausgeschlossen, soweit das Gesetz es zulässt. Unberührt bleibt die gesetzlich zwingende Haftung (bei Vorsatz, grober Fahrlässigkeit sowie bei Verletzung von Leben, Körper oder Gesundheit).<br>" +
    "Mit der Nutzung erkennst du diese Bedingungen an. Es gilt die MIT-Lizenz (Datei LICENSE).",
  en: "<b>WlanMerge is free software provided “as is”, without warranty of any kind.</b><br>" +
    "You use it entirely at your own risk. WlanMerge changes your computer's network settings and routes traffic over your internet connections, including mobile data.<br>" +
    "You alone are responsible for: data charges (even if a configured limit does not take effect), complying with the terms of your mobile or internet provider (e.g. tethering bans) and of game and download services, possible account bans, network problems, data loss and damage to your device.<br>" +
    "The developers accept no liability to the extent permitted by law. Liability that cannot be excluded by law (intent, gross negligence, injury to life, body or health) remains unaffected.<br>" +
    "By using WlanMerge you accept these terms. The MIT License applies (file LICENSE).",
};