// Package tuning stellt die Go-Laufzeit für WlanMerge ein.
package tuning

import (
	"os"
	"runtime/debug"
)

func init() {
	// Speicherbereinigung seltener laufen lassen (Standard 100). Beim Weiterleiten
	// entsteht viel kurzlebiger Speicher; gemessen ~30 % weniger CPU pro MB,
	// dafür etwas mehr Arbeitsspeicher (wenige MB). GOGC=... überschreibt das.
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(200)
	}
}
