// Package config speichert die Einstellungen von WlanMerge dauerhaft
// in %ProgramData%\WlanMerge\config.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// RulePrimary als Regel-Ziel heißt: immer die Hauptleitung.
	RulePrimary = "haupt"

	// Standard-Datenlimit für Handy-Adapter, solange nichts anderes eingestellt ist.
	DefaultPhoneLimit int64 = 2_000_000_000 // 2 GB
)

// Rule: Ziele, die immer über dieselbe Leitung gehen (damit die IP gleich bleibt).
type Rule struct {
	Match string // "*.battle.net", "battle.net", "1.2.3.4" oder "1.2.3.0/24"
	Link  string // Adaptername oder "haupt"
}

// Rules: Login-Server bleiben auf der Hauptleitung, damit sich dort die IP
// nicht ändert. Die Download-Server (CDNs) sind bewusst NICHT dabei,
// damit Downloads weiter gebündelt werden.
var Rules = []Rule{
	{Match: "*.battle.net", Link: RulePrimary},
	{Match: "account.blizzard.com", Link: RulePrimary},
	{Match: "login.steampowered.com", Link: RulePrimary},
	{Match: "api.steampowered.com", Link: RulePrimary},
	{Match: "store.steampowered.com", Link: RulePrimary},
	{Match: "*.steamcommunity.com", Link: RulePrimary},
	{Match: "*.steamserver.net", Link: RulePrimary},
	{Match: "*.ol.epicgames.com", Link: RulePrimary},
}

type Config struct {
	// Limits: Datenlimit in Bytes pro Adapter. 0 = kein Limit.
	// Fehlt ein Handy-Adapter hier, gilt DefaultPhoneLimit.
	Limits map[string]int64 `json:"limits"`
	// ResetDay: an diesem Tag im Monat (1–28) beginnt der Verbrauch wieder bei 0.
	// 0 = nie automatisch.
	ResetDay int `json:"resetDay"`
	// Disabled: Adapter, die WlanMerge nicht nutzen soll.
	Disabled map[string]bool `json:"disabled"`
	// Rebalance: laufende Downloads (Steam, Battle.net …) umverteilen.
	Rebalance bool `json:"rebalance"`
	// AutoConnect: beim Programmstart automatisch einschalten.
	AutoConnect bool `json:"autoConnect"`
	// Theme: "system", "dark" oder "light".
	Theme string `json:"theme"`
	// Lang: "de", "en" oder "" (= wie das Betriebssystem).
	Lang string `json:"lang"`
	// DLEnabled: Browser-Downloads über alle Leitungen beschleunigen.
	DLEnabled bool `json:"dlEnabled"`
	// HideHintShown: Hinweis "läuft im Hintergrund weiter" wurde schon gezeigt.
	HideHintShown bool `json:"hideHintShown"`
	// SpeedSaved: gespeichertes Speedtest-Ergebnis zum Vergleichen (nur eins).
	SpeedSaved *SavedSpeed `json:"speedSaved,omitempty"`
}

// SavedSpeed: Bytes/s gesamt und je Leitung.
type SavedSpeed struct {
	Total int64            `json:"total"`
	Lines map[string]int64 `json:"lines"`
	At    time.Time        `json:"at"`
}

func Default() *Config {
	return &Config{
		Limits:      map[string]int64{},
		ResetDay:    1,
		Disabled:    map[string]bool{},
		AutoConnect: true,
		Rebalance:   true,
		DLEnabled:   true,
		Theme:       "system",
	}
}

// Dir ist der Ordner für Einstellungen, Verbrauch und Status
// (Windows: %ProgramData%\WlanMerge, macOS: /Library/Application Support/WlanMerge,
// Linux: ~/.config/wlanmerge).
func Dir() string {
	if d := os.Getenv("WM_DATA_DIR"); d != "" { // für Tests
		return d
	}
	return dataDir()
}

func path() string { return filepath.Join(Dir(), "config.json") }

// EnsureDir legt den Datenordner an und schützt ihn (siehe harden).
func EnsureDir() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	if os.Getenv("WM_DATA_DIR") != "" {
		return nil // Tests
	}
	hardenOnce.Do(harden)
	return nil
}

var hardenOnce sync.Once
var mu sync.Mutex

// Load liest die Einstellungen (oder Standardwerte, falls es noch keine gibt).
// Felder alter Versionen (Modus, Regeln …) werden einfach ignoriert.
func Load() *Config {
	mu.Lock()
	defer mu.Unlock()
	c := Default()
	if b, err := os.ReadFile(path()); err == nil {
		json.Unmarshal(b, c)
	}
	return c.normalize()
}

func (c *Config) normalize() *Config {
	if c.Limits == nil {
		c.Limits = map[string]int64{}
	}
	if c.Disabled == nil {
		c.Disabled = map[string]bool{}
	}
	c.ResetDay = max(0, min(28, c.ResetDay))
	return c
}

// Clone liefert eine unabhängige Kopie (Änderungen nie am laufenden Objekt).
func (c *Config) Clone() *Config {
	b, _ := json.Marshal(c)
	n := Default()
	json.Unmarshal(b, n)
	return n.normalize()
}

func (c *Config) Save() error {
	mu.Lock()
	defer mu.Unlock()
	if err := EnsureDir(); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path()) // atomar: nie eine halb geschriebene Datei
}

func lookup[V any](m map[string]V, name string) (V, bool) {
	if v, ok := m[name]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	var zero V
	return zero, false
}

// LimitFor liefert das Datenlimit eines Adapters (0 = keins).
func (c *Config) LimitFor(name string, isPhone bool) int64 {
	if v, ok := lookup(c.Limits, name); ok {
		return v
	}
	if isPhone {
		return DefaultPhoneLimit
	}
	return 0
}

func (c *Config) IsDisabled(name string) bool {
	v, _ := lookup(c.Disabled, name)
	return v
}

// LastReset liefert den Zeitpunkt des letzten monatlichen Zurücksetzens
// (ResetDay um 0:00 Uhr) – oder die Nullzeit, wenn ResetDay aus ist.
func (c *Config) LastReset(now time.Time) time.Time {
	if c.ResetDay < 1 {
		return time.Time{}
	}
	t := time.Date(now.Year(), now.Month(), c.ResetDay, 0, 0, 0, 0, now.Location())
	if t.After(now) {
		t = t.AddDate(0, -1, 0)
	}
	return t
}
