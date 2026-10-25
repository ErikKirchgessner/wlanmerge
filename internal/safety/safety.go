// Package safety ist der Notausschalter.
//
// Sicherheitskonzept (damit du nie ohne Internet dastehst):
//  1. Wir löschen oder ändern KEINE bestehenden Netzwerk-Einstellungen.
//     Wir legen nur zusätzliche Routen auf UNSEREN virtuellen Adapter.
//  2. Diese Routen sind nicht dauerhaft gespeichert: ein Neustart löscht sie.
//  3. Der virtuelle Adapter existiert nur, solange unser Programm läuft.
//     Stürzt es ab, verschwindet der Adapter mitsamt Routen von selbst.
//  4. Vor jeder Änderung schreiben wir eine Statusdatei. Restore() räumt
//     alles auf, was eventuell übrig geblieben ist.
//
// Was je Betriebssystem zu tun ist, steht in safety_windows.go und safety_unix.go.
package safety

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"wlanmerge/internal/config"
	"wlanmerge/internal/i18n"
)

type State struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Adapter string    `json:"adapter"`
}

func stateFile() string { return filepath.Join(config.Dir(), "state.json") }

// MarkActive wird aufgerufen, BEVOR wir irgendetwas am Netzwerk ändern.
func MarkActive(adapter string) error {
	if err := config.EnsureDir(); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(State{PID: os.Getpid(), Started: time.Now(), Adapter: adapter}, "", "  ")
	return os.WriteFile(stateFile(), b, 0o644)
}

// Clear wird aufgerufen, nachdem alles sauber zurückgesetzt ist.
func Clear() { os.Remove(stateFile()) }

// Load liest die Statusdatei; ok=false heißt: es ist nichts aktiv.
func Load() (State, bool) {
	b, err := os.ReadFile(stateFile())
	if err != nil {
		return State{}, false
	}
	var s State
	if json.Unmarshal(b, &s) != nil {
		return State{}, true // kaputte Datei: sicherheitshalber aufräumen
	}
	return s, true
}

// Restore setzt alles zurück und gibt aus, was es getan hat.
// Es ist harmlos, Restore mehrfach oder ohne Grund aufzurufen.
func Restore(say func(string)) {
	s, active := Load()
	if active && s.PID != 0 && s.PID != os.Getpid() {
		if killProcess(s.PID) {
			say(i18n.Tf("Laufendes WlanMerge (PID %d) beendet", s.PID))
			time.Sleep(time.Second) // dem System Zeit geben, den Adapter zu entfernen
		}
	}
	restoreNetwork(s.Adapter, say)
	Clear()
	say(i18n.T(doneText))
}
