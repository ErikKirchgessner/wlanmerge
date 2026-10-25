package sound

import (
	"math"
	"testing"
)

func TestSynthIsCleanAndShort(t *testing.T) {
	s := synth()
	if got := float64(len(s)) / sampleRate; got < 0.8 || got > 1.5 {
		t.Fatalf("Klang sollte ca. 1 s lang sein, ist %.2f s", got)
	}
	peak := 0.0
	for _, v := range s {
		if math.IsNaN(v) {
			t.Fatal("ungültiger Wert")
		}
		peak = max(peak, math.Abs(v))
	}
	if peak > 0.56 || peak < 0.5 {
		t.Fatalf("Lautstärke falsch normiert: %.2f", peak)
	}
	if math.Abs(s[len(s)-1]) > 1e-3 {
		t.Fatal("Klang muss sanft ausklingen (kein Knacken am Ende)")
	}
	if w := encodeWAV(s); string(w[:4]) != "RIFF" || len(w) != 44+2*len(s) {
		t.Fatal("WAV-Datei fehlerhaft")
	}
}
