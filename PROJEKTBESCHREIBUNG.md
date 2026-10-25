# WlanMerge – vollständige Projektbeschreibung

Stand: 2026-10-20. Diese Datei beschreibt, was WlanMerge ist, wie es funktioniert, was gebaut und getestet wurde, wo die Grenzen liegen und was als Nächstes möglich wäre. Sie ist so geschrieben, dass sie in einem neuen Chat ohne Vorwissen verstanden werden kann.

---

## 1. Kurzfassung

WlanMerge ist ein Programm für **Windows 10/11** (64 Bit, Deutsch/Englisch), das **mehrere Internetverbindungen gleichzeitig** nutzt, zum Beispiel Haus-WLAN plus Handy per USB-Tethering. Ziel: Downloads in Steam, Battle.net und Epic Games werden schneller, weil die vielen Verbindungen, die diese Launcher öffnen, auf alle Leitungen verteilt werden.

- Kein eigener Server, alles läuft lokal auf dem PC.
- Ein einziges Fenster mit großem An/Aus-Knopf und einer Karte pro Leitung, Tray-Symbol, startet mit Windows.
- Läuft mit Administratorrechten (nötig für virtuellen Netzwerkadapter und Routen).
- Verteilt **pro Verbindung**, nicht pro Paket. Eine einzelne Verbindung läuft immer komplett über eine Leitung.

Gemessene Ergebnisse auf dem Entwickler-PC: WLAN allein ca. 50–118 Mbit/s, Handy ca. 15–50 Mbit/s, zusammen bis ca. 188–302 Mbit/s bei Steam-ähnlichen Downloads mit vielen Verbindungen.

---

## 2. Hintergrund und Nutzer

- Nutzer: Programmier-Anfänger, deutschsprachig, will direkt bauen und testen, kurze einfache Erklärungen.
- Hardware des Nutzers: Laptop mit Intel AX201 WLAN, Realtek LAN (meist nicht verbunden), Samsung-Handy per USB-Tethering (erscheint als „Ethernet 2", Gerät „SAMSUNG Mobile USB Remote NDIS Network Device"). Wichtig: Beim Handy muss das Handy-WLAN **aus** sein, sonst teilt das Handy nur dasselbe Internet und die öffentliche IP ist identisch.
- Mobile Daten sind knapp: Standard-Datenlimit fürs Handy ist 2 GB (Wunsch des Nutzers).
- Ein Kollege soll die App mit eigenem Handy/PC/WLAN testen. Verteilt wird ein Installer ohne Code-Signatur (SmartScreen-Warnung, siehe Abschnitt 14).

---

## 3. Funktionsprinzip

1. Beim Einschalten wird ein **virtueller Netzwerkadapter „WlanMerge"** (Wintun, signierter Treiber von WireGuard LLC) angelegt.
2. Es werden zwei halbe Standardrouten **0.0.0.0/1** und **128.0.0.0/1** (plus für IPv6 `::/1` und `8000::/1`, siehe unten) auf diesen Adapter gelegt. Diese sind genauer als die Standardroute 0.0.0.0/0 und gewinnen deshalb, **ohne dass bestehende Windows-Routen, DNS-Einstellungen oder Adapter verändert werden**.
3. Windows schickt dadurch alle IPv4-Verbindungen an den virtuellen Adapter. Dahinter läuft ein Netzwerk-Stack (gVisor, über die Bibliothek tun2socks), der jede neue TCP-Verbindung und UDP-Sitzung abfängt.
4. Für jede abgefangene Verbindung wählt der **Balancer** einen echten Adapter und baut die Verbindung dort **neu** auf. Die Bindung an den Adapter geschieht über die Windows-Socket-Option `IP_UNICAST_IF` (Interface-Nummer in Netzwerk-Byte-Reihenfolge) plus Binden an die lokale IPv4-Adresse des Adapters. Dadurch umgehen diese eigenen Verbindungen den virtuellen Adapter, es gibt keine Schleife.
5. Daten werden in beide Richtungen kopiert (Relay) und pro Leitung gezählt.

Wichtige Eigenschaft der eigenen Vermittlung (`internal/engine/relay.go`): **Bei TCP wird zuerst die echte Verbindung aufgebaut, erst danach wird dem Programm der Verbindungsaufbau bestätigt.** Scheitert der Aufbau (zum Beispiel bei IPv6), bekommt das Programm sofort ein RST und weicht auf IPv4 aus. Die ursprüngliche tun2socks-Variante bestätigte zuerst und brach dann ab, was bei IPv6-Zielen alle Aufrufe scheitern ließ.

### 3.1 Nur IPv4
IPv6-Verbindungen (TCP und UDP) werden bewusst sofort abgelehnt (`errNoIPv6`). Gründe: Der Router des Nutzers vergab auf dem WLAN eine nicht geroutete DHCPv6-Adresse (Steam lud über IPv6 und hing), und alle relevanten Dienste funktionieren über IPv4. Die Adapter-Erkennung (`GetAdaptersAddresses`) arbeitet nur noch mit `AF_INET`. Die IPv6-Routen auf dem virtuellen Adapter existieren weiter, damit IPv6-Verkehr dort sauber abgelehnt wird statt über eine echte Leitung am Balancer vorbei zu laufen.

---

## 4. Verteilung der Verbindungen (internal/balancer)

### 4.1 Zustände einer Leitung
`ok`, `gestört` (Gesundheitsprüfung schlägt fehl), `limit` (Datenlimit erreicht), `aus` (vom Nutzer ausgeschaltet).

### 4.2 Soll-Anteile (automatisch)
- Pro Leitung wird die höchste gemessene Rate (Bytes/s) als „Kapazität" geführt, mit langsamem Verblassen (Faktor 0.995 pro Sekunde, Halbwertszeit ca. 2 Minuten). Sekündliche Messung, geglättete Ist-Rate (EWMA, Alpha 0.2).
- Soll-Anteil jeder funktionierenden Leitung = Kapazität / Summe. Solange nichts gemessen wurde, gilt ein Standardwert von 1 Mbit/s je Leitung.
- **Mindestanteil 10 %**: Zu kleine Leitungen bekommen genau 10 %, die übrigen teilen sich den Rest im alten Verhältnis. So wird jede Leitung weiter vermessen.

### 4.3 Wahl der Leitung für eine neue TCP-Verbindung
1. **Login-Server und Spiele-UDP** → immer Hauptleitung (siehe 4.5), „pinned", wird nie umverteilt.
2. Sonst: Score je funktionierender Leitung = `(offene TCP-Verbindungen + 1) / Soll-Anteil × (1 + clamp(Ist-Anteil − Soll-Anteil, −0.5, +0.5))`. Die Leitung mit dem kleinsten Score kommt zuerst. Bei kaum Verkehr (unter ca. 60 KB/s gesamt) wird der Ist-Anteil dem Soll gleichgesetzt.
3. Gestörte oder kurz blockierte Leitungen kommen nur als letzte Notlösung ans Ende der Liste.
4. Direkt nach einer Umverteilung wird die „bevorzugte" Leitung für 5 Sekunden an die erste Stelle gesetzt.
5. Verbindungsaufbau: Alle Kandidaten außer dem letzten bekommen 2,5 s Zeit, der letzte das volle Timeout (10 s). Schlägt ein Aufbau fehl, wird der nächste Kandidat probiert. Nach 3 aufeinanderfolgenden Aufbaufehlern wird eine Leitung 15 s lang gemieden.

### 4.4 Hauptleitung
Es gibt **keine** manuelle Einstellung mehr. Hauptleitung = die erste funktionierende Leitung in fester Reihenfolge: **Nicht-Handy vor Handy** (Datenvolumen schonen), dann alphabetisch nach Adaptername. Fällt das WLAN aus, wird automatisch das Handy Hauptleitung.

### 4.5 Was an die Hauptleitung gebunden ist
- **Login-/Konto-Server** (feste Liste, `config.Rules`): `*.battle.net`, `account.blizzard.com`, `login.steampowered.com`, `api.steampowered.com`, `store.steampowered.com`, `*.steamcommunity.com`, `*.steamserver.net`, `*.ol.epicgames.com`. Zweck: Die öffentliche IP bleibt für Logins gleich. Download-Server (CDNs) sind bewusst nicht enthalten, damit Downloads gebündelt werden.
- **UDP** (Spiele, Sprachchat, DNS) außer UDP-Port 443. UDP 443 ist QUIC/HTTP3 und wird wie TCP verteilt.
- Wie Namen zu IPs kommen: Der Tunnel liest DNS-Antworten mit (UDP Port 53 läuft über die Hauptleitung, die Antworten werden geparst) und merkt sich IP → Namen (`internal/rules`, DNS-Cache, mind. 10 Minuten, max. 50 000 Einträge). Zusätzlich werden Regeln ohne Platzhalter beim Einschalten selbst aufgelöst.
- **Wichtige Regel**: Eine IP wird nur festgehalten, wenn **alle** bekannten Namen dieser IP zu einer Regel passen. Sonst würde eine CDN-IP, die gleichzeitig die Steam-Shop-Seite und Downloads ausliefert, Downloads auf die Hauptleitung zwingen. CNAME-Ziele zählen nicht als Name, nur der angefragte Name.
- Regelabgleich: `*.x.y` passt nur auf Subdomains, `x.y` auf sich selbst und Subdomains, außerdem IPs und CIDR-Bereiche.

### 4.6 Gesundheitsprüfung
- Alle 4 s pro Leitung: kurzer TCP-Verbindungsaufbau (nur Handshake, kaum Daten) zu 1.1.1.1:443, 8.8.8.8:443, 9.9.9.9:443, gebunden an die Leitung. Die Zeit des Aufbaus ist der angezeigte „Ping".
- 2 Fehlschläge in Folge = `gestört`. Beim Wechsel zu gestört werden alle Verbindungen dieser Leitung geschlossen (die Programme bauen neu auf, dann über andere Leitungen), aber **nur wenn es eine andere funktionierende Leitung gibt**, sonst nicht (Schutz vor Fehlalarm).
- **Handy-Erkennung:** USB-Tethering (Gerätename „NDIS", „Mobile", „Android", „iPhone" …), WLAN-Hotspot über einen zweiten WLAN-Adapter (Windows führt die Verbindung als „getaktet" – Abfrage über den Network List Manager, `internal/netif/cost_windows.go` – oder iPhone-Netz 172.20.10.0/28) und getaktetes LAN (z. B. LTE-Router). Handy-Leitungen bekommen das Standardlimit und werden nie Hauptleitung. **Bluetooth wird bewusst nicht genutzt** (nur 1–2 Mbit/s).
- Adapter, die verschwinden (USB abgezogen), lösen das Schließen ihrer Verbindungen und eine Meldung aus. Neue Adapter werden alle 3 s erkannt.

### 4.7 Umverteilen laufender Downloads (internal/balancer/rebalance.go)
Technische Grenze: Eine laufende TCP-Verbindung kann ohne eigenen Server nicht die Leitung wechseln (der Server sähe eine andere Quell-IP). Launcher öffnen aber viele Verbindungen zum selben Server und bauen eine geschlossene sofort neu auf. Das wird genutzt:
- Alle 2 s wird geprüft. Nur Gruppen mit **mindestens 3 Verbindungen zum selben Ziel** zählen als „Download-Programm". Einzelne Verbindungen (Browser-Download) werden nie angefasst.
- Trägt eine Leitung weniger als 60 % ihres Soll-Anteils, obwohl sie nicht schon genug Verbindungen hat, und trägt eine andere mehr als ihr Soll, wird **eine** Verbindung (die schnellste, mindestens 5 s alt, mindestens 64 KB/s, nicht „pinned") auf der vollen Leitung geschlossen. Die neue Verbindung des Launchers landet dank der Bevorzugung auf der freien Leitung. Danach 6 s Pause. Mindestumsatz der Gruppe: 2 Mbit/s.
- **Hängende Verbindungen**: Steht eine Verbindung einer Gruppe über 20 s still, während Geschwister zum selben Ziel laufen, wird sie geschlossen (max. 2 pro Runde).
- Schaltbar in den Einstellungen („Laufende Downloads umverteilen", Standard an). Eine Meldung erscheint höchstens einmal pro Minute.
- Ehrliche Grenze: Es werden nur ganze Verbindungen verschoben, die Prozentwerte werden daher nie exakt, bei Steam mit 8–16 Verbindungen aber ungefähr.

---

## 5. Datenlimit und Verbrauch

- Pro Leitung zählt WlanMerge den **gesamten Verkehr des Adapters** über die Zähler des Betriebssystems (Windows: GetIfEntry2/`MibIfRow2`, Linux: `/sys/class/net/*/statistics`, macOS: `NET_RT_IFLIST2`), inklusive TCP/IP-Kopfdaten – also so, wie der Mobilfunkanbieter zählt, und auch dann, wenn WlanMerge aus oder geschlossen war (solange der Adapter verbunden blieb). Ohne Limit steht in der Kachel einfach der Verbrauch. Dauerhaft gespeichert in `usage.json` (mit letztem Zählerstand), überlebt Neustarts.
- Standardlimit: **Handy-Adapter 2 GB** (Dezimal, 2 000 000 000 Bytes), andere Adapter kein Limit. Pro Adapter einstellbar (GB, 0 oder leer = kein Limit).
- Bei Erreichen: Zustand `limit`, alle Verbindungen der Leitung werden geschlossen, Meldung in der App, **Windows-Benachrichtigung mit eigenem Klang** (Abschnitt 8). Das WLAN läuft normal weiter.
- **Monatlicher Reset**: Einstellung „Verbrauch zurücksetzen" mit Tag 1–28 (Standard 1.) oder „nie". Der Zähler jeder Leitung, der vor dem letzten Resetzeitpunkt (Tag, 0:00 Uhr) zu laufen begann, wird auf 0 gesetzt. Prüfung alle 10 s und bei Konfigurationsänderungen. Zusätzlich manuell per Pfeil-Symbol auf der Leitungskarte.
- Verbrauch bleibt nach Reset bestehen, was vorher unter Limit lag, ein gesperrtes Handy wird sofort wieder freigegeben.

---

## 6. Sicherheit und Notausschalter („nie ohne Internet")

Konzept (`internal/safety`):
1. WlanMerge **ändert keine bestehenden Windows-Einstellungen** (keine Routen löschen, kein DNS ändern, keine Adapter deaktivieren). Es werden nur zusätzliche Routen auf den eigenen virtuellen Adapter gelegt.
2. Diese Routen sind nicht persistent, ein Neustart löscht sie.
3. Der Wintun-Adapter existiert nur, solange das Programm läuft. Bei Absturz verschwindet er von selbst samt Routen. (Getestet: Prozess hart beendet, Adapter und Routen sofort weg, Internet läuft.)
4. Vor jeder Änderung wird `%ProgramData%\WlanMerge\state.json` geschrieben (PID, Startzeit). `Restore()` räumt alles auf: beendet ein hängendes WlanMerge (nur wenn der Prozess wirklich `wlanmerge.exe` heißt, Schutz vor PID-Wiederverwendung nach Neustart), entfernt Routen und IPs vom virtuellen Adapter, entfernt den Adapter per `pnputil /remove-device`, leert den DNS-Cache, löscht die Statusdatei. Mehrfaches Aufrufen ist harmlos.
5. **Selbsttest beim Einschalten**: Nach dem Start wird über den normalen Windows-Weg eine winzige Seite abgerufen (`http://1.1.1.1/cdn-cgi/trace`, Ersatz `msftconnecttest.com`), 8 s Timeout. Scheitert das, wird sofort alles zurückgesetzt und ein Fehler gezeigt.
6. **Wächter**: Alle 30 s derselbe Abruf. 3 Fehlschläge in Folge, obwohl mindestens eine Leitung direkt funktioniert, führen zur automatischen **Sicherheitsabschaltung** mit Fehlermeldung. Sind alle Leitungen tot, wird nicht abgeschaltet (würde nichts helfen).
7. **Notaus** in den Einstellungen („Netzwerk zurücksetzen") und per Kommandozeile `WlanMerge.exe --restore`.
8. **Beim Hochfahren**: Eine geplante Windows-Aufgabe „WlanMerge Reset" (Boot-Trigger, läuft als SYSTEM) führt `WlanMerge.exe --restore` aus, als zusätzliches Sicherheitsnetz.
9. Der Datenordner `%ProgramData%\WlanMerge` bekommt eine geschützte ACL: SYSTEM und Administratoren Vollzugriff, normale Benutzer nur Lesen (`D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)`). Änderungen an Einstellungsdateien werden atomar geschrieben (Temp-Datei + Rename).
10. Deinstallation (`--uninstall`) setzt zurück, entfernt beide Aufgaben, löscht den Datenordner und `wintun.dll`.

---

## 7. Benutzeroberfläche

Technik: **Wails v2** (Go-Backend, Webview2-Frontend in Vanilla-JS/HTML/CSS, Build mit Vite). Fenster ohne Rahmen (660×720, Minimum 420×480), eigene Titelleiste.

### 7.1 Hauptansicht
- Großer runder **An/Aus-Knopf** mit 3D-Optik (Glanzkante oben, Seitenwand, sinkt beim Drücken ein; eingeschaltet blau-türkiser Verlauf mit leisem „Atmen"), darunter Status („Aus", „Verbinde …", „Aktiv"), Gesamtgeschwindigkeit als große 3D-Zahl (Mbit/s), Zeile mit Upload, Anzahl aktiver Leitungen und öffentlicher IP. Im ausgeschalteten Zustand: „N Leitungen bereit".
- **Kachel pro Leitung** (nebeneinander, zentriert, je ca. 303 × 185 px, zusammen höchstens 616 px breit): 3D-Symbol (WLAN blau, Handy türkis, LAN) ist zugleich der Ein/Aus-Knopf der Leitung – das Monster springt darauf; aus = grau und eingedrückt. Status-Punkt am Symbol (grün aktiv, gelb gestört, rot Limit), Name, Art plus Ping (Störungen als Text), Live-Download/Upload, Balken „Anteil" (aktueller Ist-Anteil; ausgeschaltet: geplanter Anteil), Verbrauch mit Limit-Balken (orange ab 80 %, rot ab 100 %), „+ Limit"/„Limit", Reset-Pfeil – für jede Leitung, auch WLAN.
- **Speedtest** (Knopf unter dem Ein/Aus-Knopf): siehe Abschnitt 14b.
- Meldungsleiste (verschwindet nach 9 s, Fehler bleiben bis zur Behebung). Limit-Meldungen und „gestört" in Warnfarbe.
- Leere Ansicht, wenn keine Leitung da ist: Hinweis auf WLAN/LAN/USB-Tethering.
- Aktualisierung jede Sekunde über das Ereignis `state`; DOM-Updates schreiben nur geänderte Werte. Im Tray (Fenster versteckt) werden keine Ereignisse gesendet.

### 7.2 Einstellungen (Zahnrad)
- Automatisch einschalten (Standard an)
- Mit Windows starten
- Laufende Downloads umverteilen
- Handy verbinden: Hinweise zu USB-Kabel (empfohlen) und WLAN-Hotspot (zweiter WLAN-Adapter)
- Datenvolumen: Tag des monatlichen Resets (nie, 1.–28.)
- Notfall: „Netzwerk zurücksetzen"
- Erklärungstext (eine Verbindung kann nie auf zwei Leitungen laufen, Spiele und Logins bleiben auf der Hauptleitung)
- Titelleiste: Design-Wechsel (System/Hell/Dunkel), Einstellungen, Minimieren, Schließen (versteckt nur ins Tray).

### 7.3 Das Monster (reine Animation, keine Funktion)
Ein Pixel-Monster (20×17 Raster, nach Vorlage des Nutzers als Vektorgrafik nachgebaut, Lila-Verlauf, Glanzpunkte, Augen) ploppt beim Ein- und Ausschalten aus dem Knopf, springt hoch, landet, drückt den Knopf ein (Umschaltung geschieht genau beim Aufprall), blinzelt (an: doppeltes Zwinkern, aus: Augen fallen zu), springt zurück und verschwindet. Dauer 1150 ms (15 % schneller als zuvor), Squash-and-Stretch, Schatten auf dem Knopf, Pixel-Staub und Wisch-Streifen links/rechts bei der Landung, Bewegungsstreifen beim Steigen (unter) und Fallen (über dem Monster). Respektiert „reduzierte Bewegung" (dann sofort umschalten).

### 7.4 Optik
Dunkel und hell, minimalistisch. 3D-Effekte an Knöpfen, Schaltern, Leitungssymbolen und Text (Schatten, Verlauf). Farbvariablen im CSS (`--pw-*`, `--sw-off`, `--t3d`, …).

### 7.5 Tray
Symbol (an: farbig, aus: grau), Tooltip mit Geschwindigkeit, Menü „Öffnen / Einschalten bzw. Ausschalten / Beenden", Klick öffnet das Fenster. Fensterschließen versteckt; beim ersten Mal erscheint eine Windows-Benachrichtigung „WlanMerge läuft im Hintergrund weiter" mit dem Hinweis, dass Windows 11 Symbole hinter dem ^-Pfeil verstecken kann.

### 7.6 Benachrichtigung bei Limit
Windows-Toast (go-toast, stumm) mit Logo, Titel „Datenlimit erreicht" und Text. Gleichzeitig spielt ein selbst erzeugter **Klang** (`internal/sound`): ein Wassertropfen mit steigender Tonhöhe fällt in eine Klangschale, G-Dur-Akkord (G5, D6, später H5), zweiter kleiner Tropfen, etwas Hall, ca. 1,15 s, wird beim Start einmal berechnet und per `PlaySoundW` aus dem Speicher gespielt.

---

## 8. Autostart und Start-Verhalten

- Geplante Windows-Aufgabe **„WlanMerge"**: startet beim Anmelden des Benutzers (5 s Verzögerung) `WlanMerge.exe --tray` mit höchsten Rechten (keine UAC-Abfrage), keine Akku-Einschränkung, kein Zeitlimit. Wird über XML (UTF-16) angelegt, weil die einfachen schtasks-Parameter ungünstige Standardwerte setzen.
- `--tray`: kleiner **Wächter**. Wartet bis zu 90 s auf die Taskleiste (`Shell_TrayWnd`) + 2 s, startet dann `WlanMerge.exe --tray-run` (Fenster versteckt) und startet die App bei einem Fehler-Ende neu (5, 10, 15 … s Pause, max. 6 Versuche; Zähler zurück nach 5 min Laufzeit). Code 0 („Beenden" im Tray) beendet auch den Wächter.
- Protokoll: `%ProgramData%\WlanMerge\wlanmerge.log` (Go-`log` und Ausgaben/Abstürze der App; über 1 MB wird neu begonnen).
- **Auto-Einschalten** beim Programmstart: Wartet bis zu 5 Minuten, bis mindestens eine Leitung existiert (WLAN ist nach dem Hochfahren oft noch nicht verbunden), schaltet dann **genau einmal** ein (bei Fehler nicht ständig neu versuchen).
- **Einzelinstanz**: Ein zweiter Start öffnet nur das Fenster der ersten.
- **Bekannter Fehler (behoben, noch nicht per echtem Neustart verifiziert)**: Nach dem Anmelden fehlte das Tray-Symbol komplett. Ursache: Das Programm startet vor der Taskleiste, Windows lehnt das Symbol ab, die Meldung „TaskbarCreated" zum Nachtragen wird für Programme mit Admin-Rechten blockiert. Fix: Die Meldung wird per `ChangeWindowMessageFilter` erlaubt, und das Symbol wird nach ca. 3, 10, 30 und 60 Sekunden zusätzlich selbst nachgetragen (`app.go`, `allowTaskbarCreated`, `reAddTrayIcon`). Falls es nicht reicht: Autostart-Verzögerung von 5 auf ca. 20 s erhöhen.

---

## 9. Installer

- NSIS-Installer über `wails build -nsis`, eine Datei (ca. 8 MB), deutsche Oberfläche, installiert nach `C:\Program Files\WlanMerge\WlanMerge`, Verknüpfungen auf Desktop und im Startmenü, Admin-Manifest (`requireAdministrator`).
- Beim Installieren: laufendes WlanMerge beenden (`taskkill`), Dateien kopieren, `--restore` (Reste), `--setup` (legt die Aufgaben „WlanMerge" und „WlanMerge Reset" an). Am Ende Option „WlanMerge jetzt starten".
- Beim Deinstallieren: Prozess beenden, `--uninstall` (Reset, Aufgaben entfernen, Datenordner löschen), Dateien entfernen.
- `wintun.dll` ist ins Programm eingebettet (`internal/wintundll`) und wird bei Bedarf neben die EXE geschrieben.
- Update: Installer einfach über die alte Version ausführen, Einstellungen und Verbrauch bleiben erhalten (Datenordner in `%ProgramData%`, unabhängig vom Installationsordner).

---

## 10. Technik und Projektstruktur

Sprache Go 1.27, Wails v2.16, Bibliotheken: tun2socks v2.7 (nur Stack und TUN-Gerät, die eigentliche Vermittlung ist selbst geschrieben), gVisor (Netzwerk-Stack), WireGuard `wintun` und `winipcfg` (Adapter, Routen, IPs), `golang.org/x/sys/windows`, `golang.org/x/net/dns/dnsmessage`, energye/systray, go-toast. Frontend: Vanilla-JS, Vite 7.

```
supervise.go                Autostart-Wächter, Protokolldatei
main.go                     Wails-Start, Kommandozeilen (--tray, --tray-run, --restore, --setup, --uninstall), Einzelinstanz
app.go                      Verbindung Fenster/Tray/Steuerzentrale, Benachrichtigungen, Tray-Fix, aus JS aufrufbare Methoden
wails.json, build/          Wails-Konfiguration, Manifest (Admin), Installer-Skript (project.nsi), Icons
frontend/                   index.html, src/main.js, style.css, monster.js, mock.js (Beispieldaten für Browser-Vorschau)
cmd/wlanmerge-host/         Helfer für die Browser-Erweiterung (ohne Admin-Rechte)
extension/                  Browser-Erweiterung (Chrome/Edge)
third_party/wintun/         Lizenz von wintun (muss mitgeliefert werden)
internal/
  netif/                    Adapter-Erkennung (inkl. Handy-Hotspot über "getaktet"), Bindung an Adapter (IPv4)
  downloader/               Browser-Downloads in Stücken über alle Leitungen, lokale Weitergabe
  nativehost/               Pipe und Registrierung für die Browser-Erweiterung
  speedtest/                Speedtest über alle Leitungen
  engine/                   Wintun-Adapter, Stack, Routen, MTU; relay.go = eigene Vermittlung
  balancer/                 Verteilung, Zustände, Gesundheitsprüfung, Limit, Monatsreset; rebalance.go
  rules/                    Login-Regeln, DNS-Cache
  usage/                    Verbrauchszähler (persistent)
  config/                   Einstellungen, geschützter Datenordner, Standardregeln
  service/                  Steuerzentrale (Start/Stop, Selbsttest, Wächter, Einstellungen)
  safety/                   Statusdatei, Notaus/Restore
  autostart/                Geplante Windows-Aufgaben
  sound/                    Klang erzeugen und abspielen
  trayicon/                 Tray-Icons, Logo (eingebettet)
  wintundll/                eingebettete wintun.dll
  tuning/                   Laufzeit-Einstellungen (GC)
Weitergabe/                 Installer + Anleitung.txt + WlanMerge.zip für Kollegen
```

Daten zur Laufzeit in `%ProgramData%\WlanMerge`: `config.json`, `usage.json`, `state.json` (nur während des Betriebs). Umgebungsvariable nur für Tests: `WM_DATA_DIR`.

Build:
```
wails build -nsis -trimpath -ldflags "-s -w"     (NSIS muss installiert sein, z. B. via winget NSIS.NSIS)
go test ./internal/...
```

Das frühere Test-Werkzeug `wmcli` und der Icon-Generator wurden entfernt (2026-10-20); Messungen laufen über den eingebauten Speedtest.

---

## 11. Leistung (gemessen, 3 Runden je 100 MB, WLAN-Schwankung ±10 Mbit/s)

| | Download | CPU pro MB | Seiten mit IPv4+IPv6 |
|---|---|---|---|
| ursprüngliche Vermittlung (tun2socks-Tunnel) | ca. 85 Mbit/s | 22,4 ms | 0 von 15 |
| aktuelle Version | ca. 82 Mbit/s | 9,5 ms | 15 von 15 |

Wirksame Maßnahmen: eigene Vermittlung (erst verbinden, dann bestätigen), MTU des virtuellen Adapters 9000 (die Pakete verlassen den PC nie), GC-Prozent 200, Pufferpools, keine UI-Updates im Tray, Tooltip nur bei Änderung, asynchrone Autostart-Prüfung, DNS-UDP-Sitzungen nach 10 s statt 60 s freigeben. Die Mbit/s selbst blieben gleich, weil die Internetleitung der Engpass ist. Eine andere Programmiersprache wurde bewusst nicht gewählt (kein messbarer Vorteil, nur Risiko).

**Sparsamkeit (2026-10-22, gemessen):** Der WebView-Grafikprozess von WlanMerge brauchte im Leerlauf ~12 % eines Kerns – Ursache waren Dauer-Animationen (Atmen des Ein/Aus-Knopfs, 0,6-s-Gleiten der Balken bei sekündlichen Werten). Jetzt: keine Endlos-Animation mehr, Balken springen, keine Zustands-Updates an das Fenster, wenn es versteckt **oder minimiert** ist, Tray-Tooltip höchstens alle 5 s, Knopf-Zustände/Tooltips nur bei echter Änderung. Speedtest: keine Zwischenstände (nur CSS-Balken), 256-KB-Lesepuffer, ~3 % eines Kerns bei ~100 Mbit/s; Ausweich-Quellen (Hetzner, OVH), wenn Cloudflare nach mehreren Tests mit HTTP 429 sperrt. Die Restverzögerung während eines Speedtests (System-Timer bis ~9 ms statt 1 ms) kommt vom WLAN-Treiber unter Volllast, nicht von WlanMerge. Gesundheitsprüfung: keine Test-Verbindung, solange über die Leitung Daten fließen; Leitungen ohne Verbindungen nur alle 30 s (spart am Handy ~8 MB/Tag).

---

## 12. Teststatus

Automatisch (Go-Tests, alle grün): Verteilung nach Kapazität (2:1), Mindestanteil, Limit schaltet Leitung ab und erzeugt Limit-Meldung, monatlicher Reset gibt Limit frei, gestörte Leitung nur als Notlösung, Login/Spiele-UDP auf Hauptleitung, Hauptleitungs-Wechsel, IPv6 wird abgelehnt, Umverteilen verschiebt genau eine Verbindung, einzelne Verbindungen werden nie angefasst, hängende Verbindung wird geschlossen, QUIC verteilt/Spiele nicht, geteilte CDN-IP wird nicht festgehalten, DNS-Parsing, Regel-Abgleich, Letzter-Reset-Berechnung, Klang-Synthese.

Live auf dem Entwickler-PC bestätigt: Adapter-Erkennung, Parallel-Download (1,2× bis ca. 1,5× der besten Einzelleitung, später 188–302 Mbit/s mit Steam), virtueller Adapter, Abbruch-Test (Adapter verschwindet), Notaus, Fenster und Tray, Benachrichtigung ausgelöst.

**Nicht verifiziert**: Tray-Symbol nach echtem Neustart (Fix bereitgestellt), Limit-Abschaltung und Handy-Ausfall (USB abziehen) mit echter App, Installer/Deinstaller auf fremdem PC, Verhalten bei iPhone-Tethering, mehr als zwei Adapter, VPN-Software, Test durch den Kollegen auf anderer Hardware.

---

## 13. Gelöste Fehler (Auswahl, für Kontext)

- Gleiche öffentliche IP auf beiden Leitungen: Handy-WLAN war an.
- Programm stürzte beim Aufräumen ab, wenn der Start scheiterte (nil-Engine).
- WLAN ungenutzt bei Steam: IPv6-DHCPv6-Adresse des Routers wurde nicht geroutet. Gelöst durch IPv6 ablehnen (vorher: Adresswahl SLAAC/temporär und IPv6-Gesundheitsprüfung).
- Eine Login-Regel hielt Downloads auf der Hauptleitung fest, weil eine CDN-IP mehrere Namen hat.
- Mindestanteil im Auto-Modus fiel nach dem Normieren unter 10 %.
- Frontend-Build brach ab, wenn `frontend/dist` leer war (Platzhalter-Datei `gitkeep`, auch in `frontend/public`).
- Tray-Symbol fehlte nach Windows-Start (Abschnitt 8).

---

## 14. Grenzen und ehrliche Einschränkungen

- **Eine einzelne Verbindung wird nie schneller**: Sie läuft komplett über eine Leitung. Browser-Downloads profitieren über den Download-Manager (die Datei wird in Stücke geteilt), sofern der Server Teil-Anfragen erlaubt.
- Prozente werden nicht exakt getroffen (nur ganze Verbindungen verschiebbar).
- Beim Einschalten brechen bestehende Verbindungen kurz ab (Discord u. a. verbinden neu).
- Windows-`ping` zeigt mit WlanMerge falsche Werte (ICMP läuft nicht über die Vermittlung).
- Nur IPv4 wird gebündelt.
- Der Verbrauch kommt aus den Adapter-Zählern des Systems; kleine Abweichungen zur Abrechnung des Anbieters sind trotzdem möglich (z. B. Abrechnung in 10-kB-Schritten).
- **Kein Server-Modus**: Ein Server (gemietet im Rechenzentrum oder ein PC bei Bekannten) könnte auch einzelne Verbindungen über beide Leitungen aufteilen. Das wäre ein neues großes Projekt. Nachteile: Alles läuft über den Server, höherer Ping, Steam sieht die Server-IP. Ein Heim-PC als Server lohnt nur, wenn dessen **Upload** größer ist als die Summe der eigenen Leitungen (Download-Speed und WLAN dort sind egal). Der Nutzer erwog den PC der Eltern, wollte den Upload-Wert per Speedtest klären.
- Installer ist **nicht digital signiert**: SmartScreen warnt („Unbekannter Herausgeber", „Weitere Informationen → Trotzdem ausführen"). Zertifikat kostet ca. 100–300 €/Jahr.
- Manche Virenscanner reagieren misstrauisch (Netzwerkverkehr wird umgeleitet).
- Läuft nur mit Administratorrechten.

---

## 14a. Download-Manager (umgesetzt 2026-10-20)

Datei-Downloads werden per HTTP-Range in Stücke geteilt und über alle Leitungen gleichzeitig geladen (je Stück eine eigene Verbindung, fest an eine Leitung gebunden). Automatische Kompatibilität ohne Serverliste: Range-Prüfung vorab, selbstlernende Ausnahmeliste bei 403/429 auf der zweiten Leitung, Retry-After, nur HTTP/1.1 und IPv4, Weiterleitungen einmal auflösen, sauberer Rückfall an den Browser. Keine eigene Download-Oberfläche: Die Erweiterung übergibt jeden Browser-Download (jede Größe), WlanMerge lädt ihn über alle Leitungen (Verbindungen je Leitung automatisch 6–12) und reicht ihn über `127.0.0.1` an den Browser zurück – der Browser zeigt die Summen-Geschwindigkeit. Im Hauptfenster nur eine kleine Zeile je laufendem Download. Browser-Anbindung über die Erweiterung (`extension/`) und `wlanmerge-host.exe`. Details, Abweichungen und Messwerte: `DOWNLOAD-MANAGER.md`, Abschnitt 0.

## 14b. Speedtest (2026-10-20)

Knopf „Speedtest" unter dem Ein/Aus-Knopf; ein Klick startet den Test und klappt das Ergebnis auf, „^" klappt es ein. „Ergebnis speichern" legt genau ein Ergebnis in `config.json` (`speedSaved`) ab; spätere Tests zeigen die Differenz in Mbit/s (grün = mehr, rot = weniger), gesamt und je Leitung. Leitungskarten stehen nebeneinander (zentriert); das WLAN-/Handy-Symbol ist der Ein/Aus-Knopf der Leitung (mit Sprung-Animation, aus = grau/eingedrückt). `internal/speedtest` lädt 10 s lang Testdaten von `speed.cloudflare.com/__down` – je Leitung 4 eigene HTTP/1.1-Verbindungen (über `DownloadLines`, also fest an die Leitung gebunden und im Verbrauch gezählt). Die ersten 2 s zählen nicht (TCP-Anlauf). Höchstens 150 MB je Leitung. Anzeige: live, danach Summe, jede Leitung einzeln, „+X % gegenüber nur Hauptleitung" und der Verbrauch der Nebenleitungen. Funktioniert auch, wenn WlanMerge aus ist.

## 15. Offene Ideen und mögliche nächste Schritte

1. Tray-Symbol-Fix nach Neustart bestätigen (sonst Autostart-Verzögerung auf ca. 20 s).
2. Echter Test durch den Kollegen (anderes Handy, evtl. iPhone, anderes WLAN), Fehlerberichte: ein Button „Diagnose kopieren" (Leitungen, Zustände, letzte Meldungen, Version) wäre dafür die wichtigste Ergänzung. Außerdem Versionsnummer in den Einstellungen.
3. Optionaler Server-Modus (nur sinnvoll mit schnellem Server-Upload), dazu Protokolldesign, Verschlüsselung, Einrichtung (Portfreigabe oder Tunnel), Server-Programm, Ping-Folgen.
4. Code-Signatur für den Installer.
5. Auto-Update.
6. Weitere Ziele für die Login-Regeln (zum Beispiel Riot, EA, Ubisoft), falls Logins in anderen Launchern haken. Eigene Regeln wurden bewusst aus der Oberfläche entfernt, die Liste steht fest im Code (`internal/config/config.go`).
7. Per-App-Regeln (zum Beispiel Handy nur für Steam), derzeit nicht vorhanden.

---

## 16. Entscheidungshistorie (Warum ist es so?)

- Nach Rückmeldung des Nutzers entfernt (zugunsten Minimalismus): Manuell-Modus mit Prozent-Reglern, Stern „Hauptleitung", eigene Regeln, Schalter „Spiele-Verkehr verteilen", IPv6. Alles läuft automatisch.
- Neu auf Wunsch: monatlicher Verbrauchs-Reset, Limit-Benachrichtigung mit Wassertropfen-Klang, 3D-Optik, Monster-Animation (Original des Nutzers als Pixel-Vorlage).
- Datenlimit-Standard 2 GB (ursprünglich 5 GB in der Idee).
- Go statt anderer Sprache: Wintun/tun2socks/WireGuard-Bibliotheken sind in Go.
- Git-Repository vorhanden (mehrere Commits, jeweils mit Beschreibung). Beim Weitermachen zuerst `git log` lesen.

---

## 18. Veröffentlichung

`.github/workflows/build.yml` baut bei jedem Push die Windows-Version und bei einem Tag `v*` ein GitHub-Release (mit `SHA256SUMS.txt`). Lizenz: MIT (`LICENSE`), Haftungsausschluss: `DISCLAIMER.md` (auch in der App unter Einstellungen → Rechtliches und als Lizenzseite im Windows-Setup).

> 2026-10-25: Die Mac- und Linux-Versionen wurden entfernt (nicht testbar). Der Stand davor liegt in der Git-Historie (Commit b015df7).
