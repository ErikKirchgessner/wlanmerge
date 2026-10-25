// Package sound erzeugt und spielt den WlanMerge-Benachrichtigungsklang:
// ein Wassertropfen, der in eine Klangschale fällt – kurzes "Plipp" mit
// steigender Tonhöhe, danach klingt ein weicher G-Dur-Akkord (G–H–D) nach,
// mit einem kleinen zweiten Tropfen und etwas Hall.
//
// Der Klang wird beim Start einmal berechnet (keine Datei nötig).
package sound

import (
	"bytes"
	"encoding/binary"
	"math"
	"sync"
)

const (
	sampleRate = 44100
	length     = 1.15 // Sekunden
)

var (
	once sync.Once
	wav  []byte // muss leben bleiben, solange das System abspielt
)

func theWAV() []byte {
	once.Do(func() { wav = encodeWAV(synth()) })
	return wav
}

// Play spielt den Klang im Hintergrund ab.
func Play() { play(false) }

// PlaySync spielt den Klang und wartet, bis er fertig ist.
func PlaySync() { play(true) }

// synth berechnet den Klang als Folge von Werten zwischen -1 und 1.
func synth() []float64 {
	n := int(length * sampleRate)
	buf := make([]float64, n)

	// Tropfen: Sinus, dessen Tonhöhe schnell ansteigt (so klingt ein "Plipp").
	drop := func(start, f0, f1, sweep, decay, amp float64) {
		phase := 0.0
		for i := int(start * sampleRate); i < n; i++ {
			t := float64(i)/sampleRate - start
			if t > decay*8 {
				break
			}
			f := f0 * math.Pow(f1/f0, min(t/sweep, 1))
			phase += 2 * math.Pi * f / sampleRate
			env := (1 - math.Exp(-t/0.0015)) * math.Exp(-t/decay)
			buf[i] += amp * env * math.Sin(phase)
		}
	}
	// Klangschale: zwei minimal verstimmte Sinustöne (sanftes Schweben)
	// plus leise Oktave, weicher Einsatz, langes Ausklingen.
	bell := func(start, freq, amp, decay float64) {
		for i := int(start * sampleRate); i < n; i++ {
			t := float64(i)/sampleRate - start
			env := (1 - math.Exp(-t/0.006)) * math.Exp(-t/decay)
			w := 2 * math.Pi * t
			s := math.Sin(w*freq) + 0.6*math.Sin(w*freq*1.0013) + 0.18*math.Sin(w*freq*2)*math.Exp(-t/(decay*0.4))
			buf[i] += amp * env * s / 1.78
		}
	}

	drop(0.000, 600, 1500, 0.050, 0.035, 0.60)
	bell(0.040, 783.99, 0.30, 0.45)  // G5
	bell(0.040, 1174.66, 0.16, 0.35) // D6
	drop(0.150, 900, 2000, 0.040, 0.025, 0.28)
	bell(0.170, 987.77, 0.14, 0.40) // H5

	// Etwas Hall: zwei leise Echos.
	out := make([]float64, n)
	copy(out, buf)
	for _, e := range []struct{ delay, gain float64 }{{0.09, 0.22}, {0.17, 0.12}} {
		d := int(e.delay * sampleRate)
		for i := d; i < n; i++ {
			out[i] += e.gain * buf[i-d]
		}
	}
	// Weicher machen (Höhen leicht dämpfen), Lautstärke angleichen, sanft ausblenden.
	prev, peak := 0.0, 0.0
	for i := range out {
		prev += 0.4 * (out[i] - prev)
		out[i] = prev
		peak = max(peak, math.Abs(prev))
	}
	fade := int(0.08 * sampleRate)
	for i := range out {
		out[i] *= 0.55 / peak
		if r := n - i; r < fade {
			out[i] *= float64(r) / float64(fade)
		}
	}
	return out
}

// encodeWAV verpackt die Werte als 16-Bit-Mono-WAV.
func encodeWAV(s []float64) []byte {
	var b bytes.Buffer
	data := len(s) * 2
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(sampleRate), uint32(sampleRate * 2), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v) // Format: PCM, mono, 44,1 kHz, 16 Bit
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(data))
	for _, v := range s {
		binary.Write(&b, binary.LittleEndian, int16(max(-1, min(1, v))*32767))
	}
	return b.Bytes()
}
