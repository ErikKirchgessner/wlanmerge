// WlanMerge – Browser-Erweiterung.
//
// Grundsatz: Der Browser-Download läuft IMMER erst einmal normal los.
// WlanMerge prüft im Hintergrund, ob der Server Teil-Anfragen kann. Wenn ja,
// lädt WlanMerge die Datei über alle Leitungen gleichzeitig und reicht sie über
// eine lokale Adresse (127.0.0.1) an den Browser weiter. Der Browser zeigt dann
// seinen ganz normalen Download – nur mit der Geschwindigkeit aller Leitungen
// zusammen. Bei jeder Absage, oder wenn WlanMerge nicht läuft, lädt der Browser
// einfach selbst weiter. So geht nie etwas verloren.

const HOST = "com.wlanmerge.host";

const send = (msg) =>
  new Promise((resolve) => {
    chrome.runtime.sendNativeMessage(HOST, msg, (reply) => {
      resolve(chrome.runtime.lastError ? null : reply);
    });
  });

const search = (id) => new Promise((resolve) => chrome.downloads.search({ id }, (r) => resolve(r && r[0])));

chrome.downloads.onCreated.addListener(async (item) => {
  const url = item.finalUrl || item.url || "";
  if (!/^https?:\/\//i.test(url)) return; // blob:, data:, file: … bleiben beim Browser
  if (/^http:\/\/127\.0\.0\.1[:/]/.test(url)) return; // unsere eigene Weitergabe
  if (item.byExtensionId) return; // von Erweiterungen gestartete Downloads nicht anfassen
  if (item.state && item.state !== "in_progress") return;

  let cookies = "";
  try {
    const list = await chrome.cookies.getAll({ url });
    cookies = list.map((c) => `${c.name}=${c.value}`).join("; ");
  } catch (_) { /* ohne Cookies weiter */ }

  const reply = await send({
    type: "offer",
    url,
    referrer: item.referrer || "",
    userAgent: navigator.userAgent,
    cookies,
    filename: (item.filename || "").split(/[\\/]/).pop(),
    size: item.totalBytes > 0 ? item.totalBytes : 0,
  });
  if (!reply || !reply.accept || !reply.url) return; // Browser lädt einfach weiter

  // War der Browser schon fertig (kleine Datei)? Dann nichts doppelt laden.
  const now = await search(item.id);
  if (!now || now.state !== "in_progress") {
    send({ type: "drop", token: reply.token });
    return;
  }
  chrome.downloads.cancel(item.id, () => {
    chrome.downloads.erase({ id: item.id });
    // Gleicher Download, aber über alle Leitungen – der Browser zeigt ihn ganz normal an.
    chrome.downloads.download({ url: reply.url, conflictAction: "uniquify", saveAs: false });
  });
});
