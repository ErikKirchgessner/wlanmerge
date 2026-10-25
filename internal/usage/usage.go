// Package usage zählt den Datenverbrauch pro Adapter dauerhaft
// (%ProgramData%\WlanMerge\usage.json) – auch über Neustarts hinweg.
package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"wlanmerge/internal/config"
)

// Gezählt wird mit den Zählern des Betriebssystems je Adapter: aller Verkehr
// (auch wenn WlanMerge aus ist), inklusive TCP/IP-Kopfdaten – so wie der
// Mobilfunkanbieter zählt.
type Entry struct {
	Bytes   int64     `json:"bytes"`
	Since   time.Time `json:"since"`   // seit wann gezählt wird (letztes Zurücksetzen)
	Counter uint64    `json:"counter"` // letzter Stand des Adapter-Zählers
}

type Store struct {
	mu      sync.Mutex
	entries map[string]*Entry
	dirty   bool
}

func path() string { return filepath.Join(config.Dir(), "usage.json") }

func Load() *Store {
	s := &Store{entries: map[string]*Entry{}}
	if b, err := os.ReadFile(path()); err == nil {
		json.Unmarshal(b, &s.entries)
	}
	return s
}

func (s *Store) entry(name string) *Entry {
	for k, e := range s.entries {
		if strings.EqualFold(k, name) {
			return e
		}
	}
	e := &Entry{Since: time.Now()}
	s.entries[name] = e
	return e
}

// Add zählt n Bytes für einen Adapter.
func (s *Store) Add(name string, n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	s.entry(name).Bytes += int64(n)
	s.dirty = true
	s.mu.Unlock()
}

// Observe übernimmt den aktuellen Stand des Adapter-Zählers (empfangen +
// gesendet) und zählt, was seit dem letzten Stand dazukam – auch über
// Programm-Neustarts hinweg. Ist der Zähler kleiner geworden, war der Adapter
// zwischendurch getrennt: dann zählt alles seit dem Neuverbinden.
func (s *Store) Observe(name string, counter uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entry(name)
	var delta uint64
	switch {
	case e.Counter == 0 && e.Bytes > 0:
		// Eintrag aus einer älteren Version (zählte anders): nur den Stand merken,
		// sonst würde der bisherige Verbrauch doppelt gezählt.
	case counter >= e.Counter:
		delta = counter - e.Counter
	default:
		delta = counter
	}
	if delta > 0 || e.Counter != counter {
		e.Bytes += int64(delta)
		e.Counter = counter
		s.dirty = true
	}
}

func (s *Store) Get(name string) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.entry(name)
}

// Reset setzt den Zähler eines Adapters (oder aller, wenn name leer) auf 0.
func (s *Store) Reset(name string) {
	s.mu.Lock()
	for k, e := range s.entries {
		if name == "" || strings.EqualFold(k, name) {
			*e = Entry{Since: time.Now(), Counter: e.Counter}
		}
	}
	s.dirty = true
	s.mu.Unlock()
}

// ResetBefore setzt alle Zähler auf 0, die seit vor "boundary" laufen
// (monatliches Zurücksetzen). Liefert die Namen der zurückgesetzten Zähler.
func (s *Store) ResetBefore(boundary time.Time) []string {
	if boundary.IsZero() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for k, e := range s.entries {
		if e.Since.Before(boundary) {
			if e.Bytes > 0 {
				names = append(names, k)
			}
			*e = Entry{Since: time.Now(), Counter: e.Counter}
			s.dirty = true
		}
	}
	return names
}

func (s *Store) All() map[string]Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Entry{}
	for k, e := range s.entries {
		out[k] = *e
	}
	return out
}

// Save schreibt nur, wenn sich etwas geändert hat.
func (s *Store) Save() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	b, _ := json.MarshalIndent(s.entries, "", "  ")
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
