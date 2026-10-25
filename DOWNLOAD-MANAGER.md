# WlanMerge Download-Manager – Konzept zum Einbauen

Stand: 2026-10-20. Ergänzung zu `PROJEKTBESCHREIBUNG.md`. **Umgesetzt** – Abschnitt 0 beschreibt den tatsächlichen Stand und die Abweichungen vom ursprünglichen Konzept (Abschnitte 1–9).

---

## 0. Umsetzung – Stand und Abweichungen

- **Pakete:** `internal/downloader` (Kern), `internal/nativehost` (Pipe + Browser-Registrierung), `cmd/wlanmerge-host` (Helfer für Chrome/Edge, eigenes Programm OHNE Admin-Rechte, weil Chrome das Admin-Programm `WlanMerge.exe` nicht starten könnte), `extension/` (Manifest V3, feste ID `jjihegfggjgeeiehnidpiemloclhbeek` über `key` im Manifest), in den Einstellungen nur der Schalter „Browser-Downloads beschleunigen" und „Einrichten" für die Erweiterung. **Keine eigene Download-Ansicht, kein Link-Einfügen** – alles läuft unsichtbar über den Browser; das Hauptfenster zeigt nur eine kleine Zeile je laufendem Download (Name, Leitungen, Mbit/s, %).
- **Weitergabe an den Browser (Streaming):** WlanMerge lädt in `%ProgramData%\WlanMerge\cache` und stellt die Datei unter `http://127.0.0.1:<Port>/d/<Token>/<Name>` bereit, der Reihe nach, sobald der zusammenhängende Anfang da ist (Range-Anfragen für Browser-Fortsetzen). Die Erweiterung bricht den Original-Download ab und startet diesen lokalen Download – der Browser zeigt so die **Summe aller Leitungen**. Ohne Leser wird nach 15 s pausiert, nach 2 h verworfen. War der Browser schon fertig (kleine Datei), schickt die Erweiterung `drop` und WlanMerge verwirft die Arbeit. Nachteil: Die Datei wird zweimal geschrieben (Cache + Browser-Ziel). Die Pipe `\\.\pipe\WlanMerge-Downloads` ist nur für den angemeldeten Benutzer und SYSTEM zugänglich, lehnt Netzwerk-Clients ab, jede Nachricht wird geprüft (nur http/https, Größenlimits, keine Zeilenumbrüche in Headern). Fertige Dateien werden dem Benutzer zugeordnet und mit `Zone.Identifier` (ZoneId=3, HostUrl, ReferrerUrl) markiert.
- **Browser-Ablauf:** Der Browser-Download läuft immer erst normal los. Die Erweiterung fragt WlanMerge. Nur bei Zusage (Range möglich, Größe bekannt, Schalter an – **keine Mindestgröße**) wird der Browser-Download beendet. Sonst lädt der Browser einfach weiter (kein Pausieren, Einmal-Links bleiben intakt).
- **Endspurt geändert:** Statt ein Stück doppelt zu laden, wird das größte laufende Stück **geteilt** (beide Hälften ≥ 1 MB). Kein Byte wird doppelt geladen (Handy-Datenvolumen), und es wirkt auch mit nur einer Leitung. Gemessen: Geschwindigkeit in der letzten Sekunde 19,7 → 62–82 Mbit/s, gesamt 69 → 75–83 Mbit/s.
- **Fortsetzen** über gespeicherte fertige Byte-Bereiche in `%ProgramData%\WlanMerge\downloads.json` (statt `.wmmeta` neben der Datei; der Ordner ist vor Nicht-Admins geschützt). Cookies werden nie gespeichert, Downloads hinter Logins lassen sich nach einem Neustart daher eventuell nicht fortsetzen.
- **Verbindungen je Leitung automatisch:** Start mit 6, alle 3 s +2, solange die Geschwindigkeit um ≥ 8 % steigt, höchstens 12; Stopp bei 429/503 oder ab 80 % fertig (gemessen auf einer Leitung: 1 → 20 Mbit/s, 4 → 64–69 Mbit/s, 8 → 83 Mbit/s). Je Leitung heißt je öffentlicher IP.
- **Ausnahmeliste** `%ProgramData%\WlanMerge\dl-exceptions.json`, 7 Tage. 403 auf der zweiten Leitung → Stück nur noch über die erste Leitung; klappt es dort, kommt der Server auf die Liste. 429/503: Retry-After (max. 60 s) und Verbindungen je Leitung −1; kommt 429 nur von der zweiten Leitung (erste Leitung in den letzten 30 s ohne 429) → Ausnahmeliste.
- Max. 3 Downloads gleichzeitig, weitere warten. Interne Adressen (localhost, private Netze, 100.64/10, 198.18/15) werden nie geladen, auch nicht über Weiterleitungen.
- **Tests:** `go test ./internal/downloader ./internal/nativehost` (Range ja/nein, 403 auf zweiter Leitung inkl. gespeicherter Ausnahme, 429, Weiterleitung, langsamere Leitung, Leitungsausfall, Pause/Fortsetzen, Neustart/Fortsetzen, kein Byte doppelt, Dateinamen, lokale Adressen, Pipe-Prüfung). (Das Live-Werkzeug `wmcli` wurde entfernt.)
- **Noch nicht live geprüft:** zwei echte Leitungen gleichzeitig (Handy war nicht angeschlossen) und die Erweiterung im echten Chrome/Edge nach der Installation.

---

## 1. Ziel und Grundidee

Der Balancer von WlanMerge verteilt bisher **ganze Verbindungen** auf die Leitungen. Ein Browser-Download nutzt oft nur **eine** Verbindung und damit nur eine Leitung. Eine einzelne Verbindung lässt sich ohne eigenen Server nicht auf zwei Leitungen aufteilen.

**Lösung ohne Server:** Nicht die Verbindung, sondern die **Datei** wird aufgeteilt (HTTP-Range-Anfragen). Das Programm lädt viele Stücke der Datei gleichzeitig, jedes in einer **eigenen Verbindung**. Für den Webserver ist jede Verbindung ein eigener Kunde, genau wie bei Steam. Der Balancer schickt sie auf verschiedene Leitungen. Zum Schluss werden die Stücke in der richtigen Reihenfolge zur Datei zusammengesetzt.

- Kein Server nötig, alles läuft lokal auf dem PC.
- Funktioniert nur für **Datei-Downloads** (ZIP, ISO, Installer, Videodateien), nicht für Webseiten, Livestreams oder Videoanrufe.

---

## 2. Parallelität und Bandbreite

- **Alles läuft gleichzeitig.** Mehrere Arbeiter (Verbindungen) laden zur selben Zeit unterschiedliche Stücke, über beide Leitungen.
- **Die Bandbreiten addieren sich ungefähr**, solange der Download-Server pro Verbindung und insgesamt schnell genug liefert. Beispiel: WLAN 80 + Handy 40 Mbit/s ergibt bis ca. 120 Mbit/s, abzüglich Overhead.
- Grenzen: Der Webserver kann pro Verbindung oder pro Nutzer drosseln, und die Gegenstelle muss die Summe liefern können. Messwerte aus dem Hauptprojekt: Parallel-Downloads erreichten 1,2× bis 1,5× der besten Einzelleitung, mit Steam bis ca. 188–302 Mbit/s. Das ist der Maßstab für die Erwartung.
- **Selbstregelnde Verteilung:** Eine schnelle Leitung holt automatisch mehr Stücke ab, weil ihre Arbeiter schneller fertig werden. Geschwindigkeiten müssen nicht geschätzt werden.

---

## 3. Gesamtablauf

```
Browser-Erweiterung  --Native Messaging-->  WlanMerge (Download-Manager)
                                                  |
                                   Prüfung (Range möglich?)
                                                  |
                              Stückliste (z. B. 4 MB pro Stück)
                                                  |
                  Arbeiter je Leitung (WLAN: 4, Handy: 4), jeder holt das nächste offene Stück
                                                  |
                          Stücke werden in die vorab angelegte Datei geschrieben
                                                  |
                       Abschluss: Größe prüfen, umbenennen, Internet-Markierung setzen
```

---

## 4. Schritt für Schritt

### 4.1 Download abfangen (Browser-Erweiterung, Chrome/Edge)
- Erkennt neue Downloads ab einer Mindestgröße (Standard 50 MB, einstellbar).
- Bricht den Browser-Download ab und sendet an WlanMerge: URL, Dateiname, Referrer, User-Agent und die **Cookies der Seite** (sonst scheitern Downloads hinter Logins).
- Übertragung per **Native Messaging**, kein offener Netzwerkport. Chrome startet `WlanMerge.exe --native-host`, dieser Prozess spricht per Named Pipe mit der laufenden App. Der Installer legt den nötigen Registry-Eintrag für den Native-Messaging-Host an und der Deinstaller entfernt ihn.
- Ist WlanMerge nicht aktiv, lässt die Erweiterung den Browser-Download normal laufen (Fallback).

### 4.2 Prüfen, ob Aufteilen geht
- Anfrage `GET` mit `Range: bytes=0-0` und `Accept-Encoding: identity`.
- Nur bei Antwort `206 Partial Content` mit `Content-Range: bytes 0-0/GESAMTGRÖSSE` wird aufgeteilt. Sonst: Download an den Browser zurückgeben (Fallback).
- Merken: `ETag` oder `Last-Modified` (um Änderungen während des Downloads zu erkennen), Gesamtgröße, endgültiger Dateiname (`Content-Disposition`, sonst aus der URL), Weiterleitungen (Redirect-Ziel einmal auflösen, danach dieses verwenden).

### 4.3 Stücke planen
- Datei vorab in voller Größe als `Dateiname.wmpart` im Download-Ordner anlegen.
- Stückgröße ca. 4 MB (bei sehr großen Dateien bis 16 MB, bei kleinen kleiner).
- Stückliste mit Zustand: `offen`, `läuft`, `fertig`. Die Liste wird regelmäßig in eine Begleitdatei (`.wmmeta`) gespeichert, damit Pausieren, Fortsetzen und Neustart möglich sind.

### 4.4 Arbeiter und Leitungen (Herzstück)
- Pro funktionierender Leitung laufen mehrere Arbeiter (Standard 4, einstellbar).
- Jeder Arbeiter: nimmt das nächste offene Stück, sendet `Range: bytes=a-b`, schreibt die Daten an die richtige Stelle der Datei (Schreibzugriff auf die Datei mit Offset), meldet Fortschritt, nimmt das nächste Stück.
- Die Verbindung des Arbeiters wird **direkt an die Leitung gebunden**, wie im Balancer (`IP_UNICAST_IF` plus lokale IPv4-Adresse), und umgeht damit den virtuellen Adapter. Der Verbrauch fließt in den Zähler der Leitung und damit in das Datenlimit.
- **Nur IPv4** und **nur HTTP/1.1** erzwingen (HTTP/2 würde mehrere Anfragen in einer Verbindung bündeln, dann würde sich nichts verteilen).
- Dauer des Verbindungsaufbaus und Fehler fließen in die Gesundheitsprüfung ein.

### 4.5 Endspurt, Fehler, Sonderfälle
- **Endspurt:** Sind keine offenen Stücke mehr da, bekommen die letzten laufenden Stücke einen zweiten Arbeiter auf der anderen Leitung. Wer zuerst fertig ist, gewinnt, der andere wird abgebrochen. So verzögert eine langsame Leitung nicht das Ende.
- **Fehler pro Stück:** bis zu 3 Wiederholungen, möglichst über die andere Leitung. Ohne Daten für 15 Sekunden: abbrechen und neu.
- **Leitung gestört oder Datenlimit erreicht:** deren Arbeiter stoppen, die offenen Stücke gehen an die übrigen Leitungen. Ist nur noch eine Leitung da, läuft der Download dort weiter.
- **Antwort `429` oder `503`:** Anzahl der Verbindungen verringern, `Retry-After` beachten.
- **`ETag` ändert sich:** Download neu beginnen.
- **Antwort `200` statt `206` bei einem Stück:** Server ignoriert Range. Abbrechen, Fallback auf eine einzige Verbindung.
- **Link an eine IP gebunden:** Manche Seiten antworten mit `403`, sobald eine zweite öffentliche IP den Link benutzt. Dann diese Domain für die Sitzung merken und nur eine Leitung verwenden.
- **Einmal-Links** funktionieren oft nicht mit mehreren Anfragen. Dann Fallback an den Browser.

### 4.6 Abschluss
- Prüfen, ob alle Stücke fertig sind und die Dateigröße stimmt (optional: Prüfsumme, wenn der Server eine liefert).
- `.wmpart` in den endgültigen Namen umbenennen, bei Namenskonflikt nummerieren.
- Datei mit der **Internet-Markierung** versehen (alternativer Datenstrom `Zone.Identifier`, `ZoneId=3`), damit Windows und SmartScreen sie wie einen Browser-Download behandeln.
- Meldung in der App, optional Windows-Benachrichtigung.

---

## 5. Sicherheit (wichtig, weil WlanMerge Administratorrechte hat)

- Native-Messaging-Host nur für die **eigene Erweiterungs-ID** zulassen (`allowed_origins` im Manifest).
- Nachrichten streng prüfen: nur `http` und `https`, keine `file:`-Links, Größenlimits für alle Felder.
- **Dateinamen bereinigen:** keine Pfadanteile, kein `..`, keine verbotenen Windows-Zeichen oder reservierten Namen. Geschrieben wird nur in den Download-Ordner des angemeldeten Benutzers, **nicht** in Systemordner.
- Da das Programm als Administrator läuft: Datei nach dem Schreiben dem Benutzer zuordnen (Besitzer/Rechte), nicht als Administrator-Datei zurücklassen.
- Cookies nur an die Domain senden, für die sie gelten. Cookies nicht protokollieren, nicht dauerhaft speichern.
- Keine Weiterleitung zu internen Adressen (localhost, 192.168.x.x, 10.x.x.x) folgen (Schutz vor Missbrauch).
- Auch der Download-Ordner muss bei Admin-Rechten sicher gewählt werden (nicht aus der Nachricht übernehmen).

---

## 6. Einbau in die bestehende Software

Neue Pakete:

```
internal/downloader/    Stückplanung, Arbeiter, Fortsetzen (.wmmeta), Endspurt, Fehlerbehandlung
internal/nativehost/    Native-Messaging-Protokoll (stdin/stdout), Named Pipe zur App
extension/              Browser-Erweiterung (Manifest V3): background.js, Optionen
```

Anpassungen:

- **`internal/balancer`** stellt dem Downloader bereit: Liste der funktionierenden Leitungen (inkl. Zustand), Funktion „Verbindung an Leitung X binden" (aus `internal/netif`), Zählen des Verbrauchs je Leitung (`internal/usage`). Der Downloader verwendet **nicht** den Tunnel, sondern bindet seine Verbindungen selbst, damit er die Leitung gezielt wählen kann.
- **`internal/service`**: Downloads starten/pausieren/abbrechen, Zustand für die Oberfläche.
- **`app.go`**: neue aus JS aufrufbare Methoden (Downloads auflisten, hinzufügen per Link, pausieren, abbrechen, Ordner öffnen).
- **`main.go`**: neuer Kommandozeilenparameter `--native-host`.
- **`internal/autostart` / Installer (`project.nsi`)**: Registry-Eintrag für den Native-Messaging-Host anlegen und beim Deinstallieren entfernen.
- **Oberfläche:** neuer Bereich „Downloads" (Fortschritt, Geschwindigkeit je Leitung, Pause/Abbrechen, Ordner öffnen). Einstellungen: Download-Manager an/aus, Mindestgröße, Verbindungen je Leitung, Download-Ordner.
- **Datenlimit:** Downloads über das Handy zählen wie alles andere in den Verbrauch. Bei erreichtem Limit läuft der Download nur noch über die anderen Leitungen weiter.

Standardwerte (zum Starten): Stückgröße 4 MB, 4 Arbeiter je Leitung, Mindestgröße 50 MB, 3 Wiederholungen je Stück, 15 s Timeout ohne Daten.

---

## 7. Teststrategie

Automatisch (Go-Tests, mit lokalem Testserver, der Range unterstützt):
- Datei wird korrekt aus Stücken zusammengesetzt (Prüfsumme gleich).
- Schnellere Leitung holt mehr Stücke.
- Ausfall einer Leitung mitten im Download: Datei trotzdem vollständig.
- Server ohne Range-Unterstützung: Fallback.
- `ETag` ändert sich: Neustart.
- Fortsetzen nach Abbruch über `.wmmeta`.
- Gefährliche Dateinamen werden bereinigt.
- Endspurt verdoppelt nur die letzten Stücke.

Live auf dem Entwickler-PC:
- `wmcli download` erweitern: Datei von `http://speedtest.tele2.net/100MB.zip` mit 1 Leitung gegen 2 Leitungen messen.
- Erwartung: deutlich über der besten Einzelleitung, im Bereich der Summe beider Leitungen.
- Echten Browser-Download mit Erweiterung testen, danach Test durch den Kollegen (anderes Handy, anderes WLAN).

---

## 8. Grenzen (ehrlich)

- Nur Datei-Downloads mit Range-Unterstützung. Nicht möglich: Videostreams (HLS/DASH), `blob:`-Links, POST-Downloads, Livestreams.
- Manche Server drosseln oder sperren mehrere Verbindungen pro Nutzer.
- Der Gewinn hängt vom Download-Server ab. Ist er pro Verbindung langsam, hilft Parallelität stark. Ist er insgesamt langsam, hilft sie nicht.
- Das Fortsetzen nach einem Neustart klappt nur, wenn die Datei auf dem Server unverändert geblieben ist.
- Die Erweiterung muss im Chrome Web Store veröffentlicht oder manuell geladen werden, das erhöht den Aufwand für Nutzer.

---

## 9. Empfohlene Reihenfolge der Umsetzung

1. **Downloader-Kern** (`internal/downloader`) mit Testserver und `wmcli`-Befehl. Beweist den Gewinn ohne Oberfläche und ohne Erweiterung.
2. **App-Oberfläche:** Feld „Link einfügen" und Bereich „Downloads".
3. **Native Host und Browser-Erweiterung.**
4. **Fortsetzen, Endspurt, Feinschliff** und alle Sonderfälle.
5. Installer-Anpassung und Test durch den Kollegen.

Beim Weitermachen zuerst `git log` lesen und die Pakete `internal/balancer` und `internal/netif` ansehen, da der Downloader deren Funktionen zum Binden an Leitungen nutzt.
