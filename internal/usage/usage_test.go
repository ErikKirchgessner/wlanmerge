package usage

import "testing"

func TestObserveCountsAdapterCounter(t *testing.T) {
	t.Setenv("WM_DATA_DIR", t.TempDir())
	s := Load()
	s.Observe("WLAN", 1000) // neuer Adapter: alles seit dem Verbinden zählt
	s.Observe("WLAN", 1500)
	if got := s.Get("WLAN").Bytes; got != 1500 {
		t.Fatalf("erwartet 1500, bekommen %d", got)
	}
	s.Observe("WLAN", 200) // Adapter neu verbunden: Zähler fing von vorn an
	if got := s.Get("WLAN").Bytes; got != 1700 {
		t.Fatalf("nach Neuverbinden erwartet 1700, bekommen %d", got)
	}
	s.Reset("WLAN")
	s.Observe("WLAN", 260) // nach dem Zurücksetzen zählt nur das Neue
	if got := s.Get("WLAN").Bytes; got != 60 {
		t.Fatalf("nach Reset erwartet 60, bekommen %d", got)
	}
}

// Ein Eintrag aus der alten Version (ohne Zählerstand) darf nicht doppelt zählen.
func TestObserveOldEntryOnlyTakesBaseline(t *testing.T) {
	t.Setenv("WM_DATA_DIR", t.TempDir())
	s := Load()
	s.Add("Handy", 5000)
	s.Observe("Handy", 900000)
	if got := s.Get("Handy").Bytes; got != 5000 {
		t.Fatalf("alter Eintrag: erwartet 5000, bekommen %d", got)
	}
	s.Observe("Handy", 901000)
	if got := s.Get("Handy").Bytes; got != 6000 {
		t.Fatalf("erwartet 6000, bekommen %d", got)
	}
}
