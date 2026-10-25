# WlanMerge

**Mehrere Internetverbindungen bündeln – für schnellere Downloads. Kostenlos, ohne Server. Für Windows 10/11.**
**Combine several internet connections for faster downloads. Free, no server needed. For Windows 10/11.**

WlanMerge verteilt die Verbindungen deines PCs auf alle Leitungen, die du hast – zum Beispiel Heim-WLAN **plus** Handy per USB-Tethering. Steam, Battle.net, Epic und Browser-Downloads werden dadurch schneller.

WlanMerge spreads your PC's connections over all the lines you have – e.g. home Wi-Fi **plus** your phone via USB tethering. Steam, Battle.net, Epic and browser downloads get faster.

> ⚠️ **Nutzung auf eigene Gefahr / Use at your own risk** – siehe / see [DISCLAIMER.md](DISCLAIMER.md). Achte auf dein Handy-Datenvolumen und die Bedingungen deines Anbieters. / Mind your mobile data and your provider's terms.

## Download

Unter **Releases** (rechts auf dieser Seite): `WlanMerge-Setup-Windows.exe`.
Windows warnt, weil das Programm nicht signiert ist: **Weitere Informationen → Trotzdem ausführen**.

Under **Releases** (on the right): `WlanMerge-Setup-Windows.exe`. Windows warns because the app is not signed: **More info → Run anyway**.

## Funktionen / Features

- Automatische Verteilung nach gemessener Geschwindigkeit, Ausfall-Umschaltung / automatic distribution by speed, failover
- Handy-Datenlimit in GB mit Benachrichtigung, monatlicher Reset / phone data limit with notification, monthly reset
- Logins und Spiele bleiben auf der Hauptleitung (keine neue Anmeldung nötig) / logins and games stay on the main line
- Laufende Steam-/Battle.net-/Epic-Downloads werden umverteilt / running downloads are rebalanced
- Browser-Downloads über alle Leitungen (Chrome/Edge-Erweiterung) / browser downloads over all lines
- Eingebauter Speedtest mit Vergleich / built-in speed test with comparison
- Deutsch & English, hell/dunkel / German & English, light/dark
- Notaus: Netzwerk jederzeit sauber zurücksetzen / emergency network reset

## Handy verbinden / Connect your phone

1. **USB-Kabel (empfohlen):** am Handy „USB-Tethering“ einschalten, Handy-WLAN **aus**. / turn on “USB tethering”, phone Wi-Fi **off**.
2. **WLAN-Hotspot:** braucht einen **zweiten** WLAN-Adapter am PC (z. B. USB-WLAN-Stick). / needs a **second** Wi-Fi adapter.

## Selbst bauen / Build from source

Go 1.27+, Node 22+, [Wails v2.16](https://wails.io), NSIS:

```sh
go test ./...
wails build -nsis
```

## Technik / How it works

Ein virtueller Netzwerkadapter (Wintun) nimmt neue Verbindungen an; WlanMerge baut jede über einen echten Adapter neu auf (`IP_UNICAST_IF`). Nichts wird dauerhaft verändert – beim Ausschalten, Absturz oder Neustart ist alles wie vorher. Details: [PROJEKTBESCHREIBUNG.md](PROJEKTBESCHREIBUNG.md).

## Lizenz / License

[MIT](LICENSE) – ohne Gewährleistung / without warranty. Wintun: `third_party/wintun/LICENSE.txt`.
