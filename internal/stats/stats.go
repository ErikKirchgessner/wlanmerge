// Package stats führt die Statistik von WlanMerge: wie viel je Tag über welche
// Leitung DURCH WlanMerge gelaufen ist und wie viel Download-Zeit das Bündeln
// ungefähr gespart hat. Gespeichert in stats.json (90 Tage).
//
// Zum Unterschied: usage zählt allen Verkehr eines Adapters (fürs Datenlimit),
// stats nur das, was WlanMerge verteilt hat (um zu zeigen, was es bringt).
package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"wlanmerge/internal/config"
)

const keepDays = 90

// Day ist ein Kalendertag.
type Day struct {
	Main  int64            `json:"main"`            // Bytes über die Hauptleitung
	Extra int64            `json:"extra"`           // Bytes über alle weiteren Leitungen (das "Mehr" durchs Bündeln)
	Saved float64          `json:"saved"`           // geschätzt gesparte Download-Zeit in Sekunden
	Lines map[string]int64 `json:"lines,omitempty"` // Bytes je Leitung (Adaptername)
}

// Ältere Tage (vor 1.2.0) kennen nur Haupt- und Zusatzleitungen. Für die
// Anzeige stehen sie unter diesen beiden Schlüsseln.
const (
	OldMain  = "~main"
	OldExtra = "~extra"
)

// lines liefert die Bytes je Leitung – auch für ältere Tage ohne Aufteilung.
func (d *Day) lines() map[string]int64 {
	if len(d.Lines) > 0 || d.Main+d.Extra == 0 {
		return d.Lines
	}
	m := map[string]int64{}
	if d.Main > 0 {
		m[OldMain] = d.Main
	}
	if d.Extra > 0 {
		m[OldExtra] = d.Extra
	}
	return m
}

// Bar ist ein Tag für das Balkendiagramm.
type Bar struct {
	Date  string           `json:"date"` // "2026-11-09"
	Main  int64            `json:"main"`
	Extra int64            `json:"extra"`
	Lines map[string]int64 `json:"lines"`
}

// Summary ist das, was die Oberfläche anzeigt.
type Summary struct {
	Today Day    `json:"today"`
	Month Day    `json:"month"` // seit dem letzten Reset-Tag (sonst letzte 30 Tage)
	Since string `json:"since"` // Beginn von Month ("2026-11-01"), "" = letzte 30 Tage
	Total Day    `json:"total"` // alles Gespeicherte (bis 90 Tage)
	Days  []Bar  `json:"days"`  // letzte 14 Tage, ältester zuerst (auch leere)
}

type Store struct {
	mu    sync.Mutex
	days  map[string]*Day
	dirty bool
	now   func() time.Time
}

func path() string { return filepath.Join(config.Dir(), "stats.json") }

func key(t time.Time) string { return t.Format("2006-01-02") }

func Load() *Store {
	s := &Store{days: map[string]*Day{}, now: time.Now}
	if b, err := os.ReadFile(path()); err == nil {
		json.Unmarshal(b, &s.days)
	}
	if s.days == nil {
		s.days = map[string]*Day{}
	}
	return s
}

func (s *Store) today() *Day {
	k := key(s.now())
	d := s.days[k]
	if d == nil {
		d = &Day{}
		s.days[k] = d
	}
	return d
}

// Add zählt übertragene Bytes je Leitung (primary = Name der Hauptleitung)
// und geschätzt gesparte Sekunden zum heutigen Tag.
func (s *Store) Add(lines map[string]int64, primary string, saved float64) {
	var total int64
	for _, n := range lines {
		total += max(0, n)
	}
	if total <= 0 && saved <= 0 {
		return
	}
	s.mu.Lock()
	d := s.today()
	for name, n := range lines {
		if n <= 0 {
			continue
		}
		if d.Lines == nil {
			d.Lines = map[string]int64{}
		}
		d.Lines[name] += n
		if name == primary {
			d.Main += n
		} else {
			d.Extra += n
		}
	}
	d.Saved += max(0, saved)
	s.dirty = true
	s.mu.Unlock()
}

// Summary fasst zusammen. since ist der Beginn des Abrechnungszeitraums
// (letzter Reset-Tag des Handyvertrags); Nullzeit = letzte 30 Tage.
func (s *Store) Summary(since time.Time) Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var out Summary
	if d := s.days[key(now)]; d != nil {
		add(&out.Today, d)
	}
	monthStart := key(now.AddDate(0, 0, -29))
	if !since.IsZero() {
		monthStart = key(since)
		out.Since = monthStart
	}
	for k, d := range s.days {
		add(&out.Total, d)
		if k >= monthStart {
			add(&out.Month, d)
		}
	}
	for i := 13; i >= 0; i-- {
		k := key(now.AddDate(0, 0, -i))
		b := Bar{Date: k, Lines: map[string]int64{}}
		if d := s.days[k]; d != nil {
			b.Main, b.Extra = d.Main, d.Extra
			for n, v := range d.lines() {
				b.Lines[n] = v
			}
		}
		out.Days = append(out.Days, b)
	}
	return out
}

// add zählt d zu to dazu (Kopie, nie die gespeicherten Daten selbst).
func add(to, d *Day) {
	to.Main += d.Main
	to.Extra += d.Extra
	to.Saved += d.Saved
	for n, v := range d.lines() {
		if to.Lines == nil {
			to.Lines = map[string]int64{}
		}
		to.Lines[n] += v
	}
}

// Save schreibt nur, wenn sich etwas geändert hat, und wirft alte Tage weg.
func (s *Store) Save() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	if len(s.days) > keepDays {
		keys := make([]string, 0, len(s.days))
		for k := range s.days {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys[:len(keys)-keepDays] {
			delete(s.days, k)
		}
	}
	b, _ := json.Marshal(s.days)
	s.dirty = false
	s.mu.Unlock()
	if err := config.EnsureDir(); err != nil {
		return err
	}
	tmp := path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path())
}
