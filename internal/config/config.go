// Package config speichert die Einstellungen von WlanMerge dauerhaft
// in %ProgramData%\WlanMerge\config.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
	// AutoPause: Hat nur eine Leitung Internet, gibt es nichts zu bündeln –
	// WlanMerge pausiert dann von selbst und macht weiter, sobald eine zweite da ist.
	AutoPause bool `json:"autoPause"`
	// AppFilter: Nur die Programme in PhoneApps dürfen Handy-Leitungen benutzen.
	AppFilter bool `json:"appFilter"`
	// PhoneApps: Programme (Dateiname klein, z. B. "steam.exe"), die das Handy nutzen dürfen.
	PhoneApps map[string]bool `json:"phoneApps"`
	// UpdateCheck: gelegentlich auf GitHub nach einer neuen Version sehen.
	UpdateCheck bool `json:"updateCheck"`
	// Onboarded: Die Einrichtung beim ersten Start wurde abgeschlossen.
	Onboarded bool `json:"onboarded"`
}

// AppDownloads steht in PhoneApps für Browser-Downloads, die WlanMerge selbst lädt.
const AppDownloads = "browser-downloads"

// KnownApp ist ein bekanntes Programm. Manche laden über eine zweite Datei
// herunter (Battle.net über agent.exe) – erlaubt man das Programm, gilt das
// für alle seine Dateien. Die Liste steht immer in der Auswahl (ohne Haken).
type KnownApp struct {
	Label string
	Exes  []string // Dateinamen klein, die erste ist der Schlüssel
}

var KnownApps = []KnownApp{
	{"Steam", []string{"steam.exe"}},
	{"Battle.net", []string{"battle.net.exe", "agent.exe"}},
	{"Epic Games", []string{"epicgameslauncher.exe"}},
	{"EA app", []string{"eadesktop.exe", "eabackgroundservice.exe"}},
	{"Ubisoft Connect", []string{"upc.exe", "ubisoftconnect.exe"}},
	{"Riot Client", []string{"riotclientservices.exe", "riotclientux.exe"}},
	{"Google Chrome", []string{"chrome.exe"}},
	{"Microsoft Edge", []string{"msedge.exe"}},
	{"Firefox", []string{"firefox.exe"}},
}

// KnownGroup liefert das bekannte Programm zu einem Dateinamen (oder nil).
func KnownGroup(exe string) *KnownApp {
	exe = strings.ToLower(exe)
	for i := range KnownApps {
		for _, e := range KnownApps[i].Exes {
			if e == exe {
				return &KnownApps[i]
			}
		}
	}
	return nil
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
		AutoPause:   true,
		UpdateCheck: true,
		PhoneApps:   map[string]bool{},
	}
}

// Dir ist der Ordner für Einstellungen, Verbrauch und Status (%ProgramData%\WlanMerge).
func Dir() string {
	if d := os.Getenv("WM_DATA_DIR"); d != "" { // für Tests
		return d
	}
	return dataDir()
}

func path() string { return filepath.Join(Dir(), "config.json") }

// Sandbox liefert im Testmodus (WM_DATA_DIR gesetzt) ein kurzes Kürzel, sonst "".
// Im Testmodus bekommen Pipes und die Einzelinstanz-Sperre dieses Kürzel als
// Anhang, und das Netzwerk wird nie verändert – so stört ein Testlauf ein
// installiertes, laufendes WlanMerge nicht.
func Sandbox() string {
	d := os.Getenv("WM_DATA_DIR")
	if d == "" {
		return ""
	}
	var h uint32 = 2166136261
	for i := 0; i < len(d); i++ {
		h = (h ^ uint32(d[i])) * 16777619
	}
	return "-test-" + strconv.FormatUint(uint64(h), 36)
}

// MayUsePhone: Darf dieses Programm Handy-Leitungen benutzen?
func (c *Config) MayUsePhone(app string) bool {
	return !c.AppFilter || c.PhoneApps[strings.ToLower(app)]
}

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
		// Einstellungen einer älteren Version: Wer WlanMerge schon benutzt,
		// braucht die Einrichtung für den ersten Start nicht mehr.
		if !strings.Contains(string(b), `"onboarded"`) {
			c.Onboarded = true
		}
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
	if c.PhoneApps == nil {
		c.PhoneApps = map[string]bool{}
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
