package stats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSummaryAndPersistence(t *testing.T) {
	t.Setenv("WM_DATA_DIR", t.TempDir())
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.Local)
	s := Load()
	s.now = func() time.Time { return now }

	s.Add(map[string]int64{"WLAN": 1000, "Ethernet 2": 400}, "WLAN", 2.5)
	now = now.AddDate(0, 0, -3) // drei Tage früher
	s.Add(map[string]int64{"WLAN": 500}, "WLAN", 0)
	now = now.AddDate(0, 0, -40) // älter als 30 Tage
	s.Add(map[string]int64{"WLAN": 7000}, "WLAN", 0)
	now = time.Date(2026, 11, 9, 13, 0, 0, 0, time.Local)

	sum := s.Summary(time.Time{})
	if sum.Today.Main != 1000 || sum.Today.Extra != 400 || sum.Today.Saved != 2.5 {
		t.Fatalf("heute falsch: %+v", sum.Today)
	}
	if sum.Today.Lines["WLAN"] != 1000 || sum.Today.Lines["Ethernet 2"] != 400 {
		t.Fatalf("heute je Leitung falsch: %+v", sum.Today.Lines)
	}
	if sum.Month.Main != 1500 || sum.Month.Lines["WLAN"] != 1500 || sum.Since != "" {
		t.Fatalf("30 Tage falsch: %+v", sum.Month)
	}
	if sum.Total.Main != 8500 {
		t.Fatalf("gesamt falsch: %+v", sum.Total)
	}
	if len(sum.Days) != 14 || sum.Days[13].Lines["Ethernet 2"] != 400 || sum.Days[10].Main != 500 || len(sum.Days[12].Lines) != 0 {
		t.Fatalf("Balken falsch: %+v", sum.Days)
	}
	// Die Zusammenfassung darf die gespeicherten Tage nicht verändern.
	if s.Summary(time.Time{}).Today.Lines["WLAN"] != 1000 {
		t.Fatal("Zusammenfassung hat die Daten verändert")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := Load()
	s2.now = s.now
	if got := s2.Summary(time.Time{}).Total.Main; got != 8500 {
		t.Fatalf("nach dem Laden: %d", got)
	}
}

// Zeitraum ab dem Reset-Tag des Handyvertrags statt der letzten 30 Tage.
func TestSinceResetDay(t *testing.T) {
	t.Setenv("WM_DATA_DIR", t.TempDir())
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.Local)
	s := Load()
	s.now = func() time.Time { return now }
	s.Add(map[string]int64{"WLAN": 100}, "WLAN", 0) // 9.11.
	now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)
	s.Add(map[string]int64{"WLAN": 50}, "WLAN", 0) // 31.10. – vor dem Reset
	now = time.Date(2026, 11, 9, 13, 0, 0, 0, time.Local)

	sum := s.Summary(time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local))
	if sum.Month.Main != 100 || sum.Since != "2026-11-01" {
		t.Fatalf("seit dem 1. falsch: %+v, since %q", sum.Month, sum.Since)
	}
}

// Tage aus Version 1.1/1.2-Vorabstand ohne Aufteilung je Leitung.
func TestOldDaysWithoutLines(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WM_DATA_DIR", dir)
	os.WriteFile(filepath.Join(dir, "stats.json"), []byte(`{"2026-11-08":{"main":300,"extra":200,"saved":4}}`), 0o644)
	s := Load()
	s.now = func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.Local) }
	sum := s.Summary(time.Time{})
	if sum.Month.Lines[OldMain] != 300 || sum.Month.Lines[OldExtra] != 200 || sum.Days[12].Lines[OldMain] != 300 {
		t.Fatalf("alte Tage falsch: %+v / %+v", sum.Month.Lines, sum.Days[12].Lines)
	}
}
