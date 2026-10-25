// Package i18n übersetzt alle Texte, die WlanMerge anzeigt (Deutsch/Englisch).
//
// Der deutsche Text ist der Schlüssel: T("WLAN verbunden") liefert auf Englisch
// die Übersetzung aus en.go, sonst den deutschen Text selbst. So bleibt der Code
// lesbar, und fehlt eine Übersetzung, erscheint nie ein leerer Text.
package i18n

import (
	"fmt"
	"sync/atomic"
)

var lang atomic.Value // "de" oder "en"

func init() { lang.Store("de") }

// Set stellt die Sprache um ("de", "en"; "" = wie das Betriebssystem).
func Set(l string) {
	if l != "de" && l != "en" {
		l = Detect()
	}
	lang.Store(l)
}

// Lang ist die aktuelle Sprache ("de" oder "en").
func Lang() string { return lang.Load().(string) }

// T übersetzt einen festen Text.
func T(de string) string {
	if Lang() == "en" {
		if s, ok := en[de]; ok {
			return s
		}
	}
	return de
}

// Tf übersetzt eine Vorlage und setzt die Werte ein (wie fmt.Sprintf).
func Tf(format string, args ...any) string { return fmt.Sprintf(T(format), args...) }

// Errorf wie fmt.Errorf, aber mit übersetzter Vorlage (%w funktioniert weiter).
func Errorf(format string, args ...any) error { return fmt.Errorf(T(format), args...) }
