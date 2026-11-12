package remote

import (
	"context"
	"testing"

	"wlanmerge/internal/service"
)

// Der Zustand muss vollständig durch die Pipe kommen, und ein zweiter Dienst
// darf nicht starten.
func TestRoundTrip(t *testing.T) {
	t.Setenv("WM_DATA_DIR", t.TempDir()) // eigene Daten, Netzwerk wird nicht verändert
	PipeName = `\\.\pipe\WlanMerge-Test-` + t.Name()
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(); err == nil {
		t.Fatal("ein zweiter Dienst dürfte die Pipe nicht öffnen können")
	}
	s := service.New()
	defer s.Close()
	closed := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go Serve(ctx, l, &API{S: s, WindowClosed: func() { closed <- struct{}{} }})

	c := &Client{}
	defer c.Close()
	if !c.Alive() {
		t.Fatal("Dienst nicht erreichbar")
	}
	if err := c.SetTheme("light"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPhoneApp("Steam.exe", true); err != nil {
		t.Fatal(err)
	}
	st, ok := c.Snapshot()
	if !ok || st.Theme != "light" || st.Version == "" || len(st.History) == 0 {
		t.Fatalf("Zustand unvollständig: ok=%v %+v", ok, st)
	}
	found := false
	for _, a := range st.Apps {
		found = found || (a.Name == "steam.exe" && a.Allowed)
	}
	if !found {
		t.Fatalf("erlaubtes Programm fehlt: %+v", st.Apps)
	}
	if c.Diagnose() == "" {
		t.Fatal("Diagnose leer")
	}
	// Fehler des Dienstes kommen als normale Fehler an – die Verbindung bleibt.
	if err := c.SaveSpeedTest(); err == nil {
		t.Fatal("Fehler erwartet (kein Ergebnis zum Speichern)")
	}
	if !c.Alive() {
		t.Fatal("Verbindung nach einem Fehler verloren")
	}
	c.WindowClosed()
	<-closed

	// Dienst weg → der Client merkt es.
	cancel()
	l.Close()
	c.Close()
	if _, ok := c.Snapshot(); ok {
		t.Fatal("Dienst ist beendet, Snapshot dürfte nicht klappen")
	}
}
