package service

import (
	"testing"
	"time"
)

// sim spielt den Autopiloten Sekunde für Sekunde durch (simulierte Zeit).
type sim struct {
	p   pilot
	now time.Time
	in  pilotIn
}

func newSim() *sim {
	t0 := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	return &sim{now: t0, in: pilotIn{want: true, auto: true, lastSwitch: t0.Add(-time.Hour)}}
}

// run lässt n Sekunden vergehen und liefert die erste Aktion (oder actNone) und
// in welchem Takt sie kam. Takt 1 bemerkt einen neuen Zustand, die Wartezeit
// zählt ab dort: "10 s warten" heißt also Aktion in Takt 11.
func (s *sim) run(n int) (pilotAct, int) {
	for i := 1; i <= n; i++ {
		s.now = s.now.Add(time.Second)
		if a := s.p.step(s.in, s.now); a != actNone {
			return a, i
		}
	}
	return actNone, 0
}

func TestPausesAfterQuietTenSeconds(t *testing.T) {
	s := newSim()
	s.in.on, s.in.usable = true, 1
	a, at := s.run(30)
	if a != actPause || at != 11 {
		t.Fatalf("nach 10 ruhigen Sekunden pausieren erwartet, bekam %v nach %d s", a, at)
	}
}

func TestNoPauseDuringDownload(t *testing.T) {
	s := newSim()
	s.in.on, s.in.usable, s.in.rate = true, 1, 2_000_000 // es läuft gerade ein Download
	if a, _ := s.run(60); a != actNone {
		t.Fatalf("während eines Downloads darf nicht pausiert werden: %v", a)
	}
	s.in.rate = 0 // Download fertig → erst jetzt beginnen die 10 s
	if a, at := s.run(30); a != actPause || at != 11 {
		t.Fatalf("nach dem Download 10 s warten, bekam %v nach %d s", a, at)
	}
}

func TestNoPauseWhileGuardingLimit(t *testing.T) {
	s := newSim()
	s.in.on, s.in.usable, s.in.guard = true, 1, true // Handy am Limit: der Tunnel schützt es
	if a, _ := s.run(60); a != actNone {
		t.Fatalf("bei einer Leitung am Limit nicht pausieren: %v", a)
	}
}

func TestNoPauseWhenSwitchedOff(t *testing.T) {
	s := newSim()
	s.in.on, s.in.usable, s.in.auto = true, 1, false
	if a, _ := s.run(60); a != actNone {
		t.Fatalf("automatische Pause ist aus: %v", a)
	}
}

func TestShortDropDoesNotPause(t *testing.T) {
	s := newSim()
	s.in.on, s.in.usable = true, 1
	s.run(8)        // 8 s nur eine Leitung …
	s.in.usable = 2 // … dann ist die zweite wieder da
	s.run(1)
	s.in.usable = 1
	if a, at := s.run(30); a != actPause || at != 11 {
		t.Fatalf("Zähler muss neu beginnen, bekam %v nach %d s", a, at)
	}
}

func TestResumesAfterThreeVerifiedSeconds(t *testing.T) {
	s := newSim()
	s.in.paused, s.in.usable, s.in.verified = true, 2, 1
	if a, _ := s.run(20); a != actNone {
		t.Fatalf("nicht nachgewiesene Leitung darf nicht fortsetzen: %v", a)
	}
	s.in.verified = 2
	if a, at := s.run(10); a != actStart || at != 4 {
		t.Fatalf("nach 3 s fortsetzen erwartet, bekam %v nach %d s", a, at)
	}
}

func TestThirtySecondsBetweenSwitches(t *testing.T) {
	s := newSim()
	s.in.paused, s.in.usable, s.in.verified = true, 2, 2
	s.in.lastSwitch = s.now // gerade erst pausiert
	if a, at := s.run(60); a != actStart || at != 30 {
		t.Fatalf("frühestens 30 s nach dem letzten Wechsel, bekam %v nach %d s", a, at)
	}
	// und umgekehrt: kurz nach dem Start nicht gleich wieder pausieren
	s = newSim()
	s.in.on, s.in.usable = true, 1
	s.in.lastSwitch = s.now
	if a, at := s.run(60); a != actPause || at != 30 {
		t.Fatalf("Pause frühestens 30 s nach dem Start, bekam %v nach %d s", a, at)
	}
}

func TestTurnOnWaitsForSecondLine(t *testing.T) {
	s := newSim()
	s.in.usable = 1 // eingeschaltet, aber nur eine Leitung
	if a, _ := s.run(1); a != actWait {
		t.Fatalf("soll als pausiert warten: %v", a)
	}
	s.in.usable = 2 // nicht pausiert: sofort starten
	if a, at := s.run(5); a != actStart || at != 1 {
		t.Fatalf("mit zwei Leitungen sofort starten, bekam %v nach %d s", a, at)
	}
	s = newSim()
	s.in.usable, s.in.auto = 1, false // ohne automatische Pause reicht eine Leitung
	if a, _ := s.run(1); a != actStart {
		t.Fatalf("ohne Pause-Funktion mit einer Leitung starten: %v", a)
	}
}

func TestNothingWhenOffOrBusy(t *testing.T) {
	s := newSim()
	s.in.want = false
	if a, _ := s.run(60); a != actNone {
		t.Fatalf("aus: keine Aktion, bekam %v", a)
	}
	s.in.want, s.in.busy, s.in.usable = true, true, 2
	if a, _ := s.run(60); a != actNone {
		t.Fatalf("während des Verbindens keine Aktion, bekam %v", a)
	}
}
