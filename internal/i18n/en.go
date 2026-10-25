package i18n

// en: Englische Übersetzungen, Schlüssel = deutscher Text (bzw. Vorlage).
var en = map[string]string{
	// Meldungen der Leitungen
	"%s verbunden": "%s connected",
	"%s getrennt – Verbindungen werden über die anderen Leitungen neu aufgebaut":     "%s disconnected – connections are rebuilt over the other lines",
	"%s: Datenlimit %s erreicht – Leitung abgeschaltet (%d Verbindungen umgeleitet)": "%s: data limit %s reached – line turned off (%d connections moved)",
	"%s ausgeschaltet": "%s turned off",
	"%s gestört – %d Verbindungen auf andere Leitungen umgeleitet": "%s disrupted – %d connections moved to other lines",
	"%s gestört – keine andere Leitung verfügbar":                  "%s disrupted – no other line available",
	"%s funktioniert wieder":                                       "%s is working again",
	"%s wieder aktiv":                                              "%s active again",
	"Neuer Monat: Datenverbrauch zurückgesetzt (%s)":               "New month: data usage reset (%s)",
	"Laufende Downloads werden umverteilt: %s → %s":                "Rebalancing running downloads: %s → %s",
	"Browser-Download beschleunigt: %s":                            "Browser download sped up: %s",
	"WlanMerge aktiv – %d Leitungen gebündelt":                     "WlanMerge active – %d lines bonded",

	// Ein-/Ausschalten
	"Keine Internetverbindung gefunden":                                                                          "No internet connection found",
	"Wintun-Treiber konnte nicht abgelegt werden: %w":                                                            "Could not install the Wintun driver: %w",
	"Selbsttest fehlgeschlagen, Netzwerk zurückgesetzt (%v)":                                                     "Self-test failed, network restored (%v)",
	"Sicherheitsabschaltung: Internet ging nicht mehr durch WlanMerge. Normales Netzwerk ist wiederhergestellt.": "Safety shutdown: the internet no longer worked through WlanMerge. Your normal network has been restored.",
	"Statusdatei: %w": "State file: %w",
	"virtuellen Adapter erstellen (liegt wintun.dll neben dem Programm?): %w": "Creating the virtual adapter failed (is wintun.dll next to the program?): %w",
	"virtuellen Adapter erstellen: %w":                                        "Creating the virtual adapter failed: %w",
	"Netzwerk-Stack: %w":                                                      "Network stack: %w",
	"IP setzen: %w":                                                           "Setting IP: %w",
	"Route %s: %w":                                                            "Route %s: %w",
	"virtueller Adapter erscheint nicht: %w":                                  "Virtual adapter does not appear: %w",
	"Kein fertiges Ergebnis zum Speichern":                                    "No finished result to save",

	// Notaus
	"Laufendes WlanMerge (PID %d) beendet":   "Stopped running WlanMerge (PID %d)",
	"Routen vom virtuellen Adapter entfernt": "Removed routes from the virtual adapter",
	"Virtuellen Adapter entfernt":            "Removed the virtual adapter",
	"WARNUNG: Virtueller Adapter konnte nicht entfernt werden (hat aber keine Routen mehr)": "WARNING: Could not remove the virtual adapter (it has no routes any more)",
	"DNS-Zwischenspeicher geleert":                    "Flushed the DNS cache",
	"Fertig: Windows-Netzwerk ist im Originalzustand": "Done: the Windows network is back to its original state",
	"Fertig: Netzwerk ist im Originalzustand":         "Done: the network is back to its original state",

	// Speedtest
	"Keine Leitung verfügbar":                               "No line available",
	"Test fehlgeschlagen – keine Leitung hat Daten geladen": "Test failed – no line loaded any data",
	"Zeitüberschreitung":                                    "Timed out",
	"abgebrochen":                                           "cancelled",

	// Browser-Downloads (Anzeige)
	"über %d Leitungen":               "over %d lines",
	"über 1 Leitung":                  "over 1 line",
	"über 1 Leitung (%s)":             "over 1 line (%s)",
	"Server erlaubt kein Aufteilen":   "server does not allow splitting",
	"nur eine Leitung verfügbar":      "only one line available",
	"Server erlaubt nur eine Leitung": "server only allows one line",

	"WlanMerge-Dienst läuft nicht": "WlanMerge service is not running",

	// macOS: Einrichtung
	"Bitte ziehe WlanMerge zuerst in den Ordner „Programme“ und starte es dann von dort.":         "Please drag WlanMerge into the Applications folder first and start it from there.",
	"WlanMerge richtet einmalig seinen Hintergrund-Dienst ein.":                                   "WlanMerge is setting up its background service (only once).",
	"Der WlanMerge-Dienst startet nicht. Bitte den Mac neu starten und es noch einmal versuchen.": "The WlanMerge service does not start. Please restart the Mac and try again.",

	// Tray und Benachrichtigungen
	"Öffnen":                                "Open",
	"Einschalten":                           "Turn on",
	"Ausschalten":                           "Turn off",
	"Beenden":                               "Quit",
	"WlanMerge ausschalten und beenden":     "Turn off WlanMerge and quit",
	"WlanMerge – aus":                       "WlanMerge – off",
	"WlanMerge – aktiv · ↓ %.1f Mbit/s":     "WlanMerge – active · ↓ %.1f Mbit/s",
	"Datenlimit erreicht":                   "Data limit reached",
	"%s. Das WLAN läuft normal weiter.":     "%s. Wi-Fi keeps working normally.",
	"WlanMerge läuft im Hintergrund weiter": "WlanMerge keeps running in the background",
	"Du findest es unten rechts in der Taskleiste (evtl. unter ^). Beenden: Rechtsklick auf das Symbol → Beenden.":                     "You'll find it at the bottom right of the taskbar (maybe under ^). To quit: right-click the icon → Quit.",
	"Du findest es oben rechts in der Menüleiste. Beenden: Klick auf das Symbol → Beenden.":                                            "You'll find it at the top right of the menu bar. To quit: click the icon → Quit.",
	"Du findest es im Infobereich der Leiste (oder öffne es wieder über das Anwendungsmenü). Beenden: Klick auf das Symbol → Beenden.": "You'll find it in the panel's system tray (or open it again from the app menu). To quit: click the icon → Quit.",
}
