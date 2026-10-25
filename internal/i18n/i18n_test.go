package i18n

import (
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	defer Set("de")
	Set("de")
	if got := Tf("%s verbunden", "WLAN"); got != "WLAN verbunden" {
		t.Fatalf("de: %q", got)
	}
	Set("en")
	if got := Tf("%s verbunden", "WLAN"); got != "WLAN connected" {
		t.Fatalf("en: %q", got)
	}
	if got := T("gibt es nicht"); got != "gibt es nicht" {
		t.Fatalf("fehlende Übersetzung muss den deutschen Text liefern: %q", got)
	}
}

// Jede englische Vorlage muss dieselben Platzhalter haben wie die deutsche,
// sonst stünde in der Meldung z. B. "%!d(MISSING)".
func TestPlaceholdersMatch(t *testing.T) {
	verbs := func(s string) string {
		var out []string
		for i := 0; i < len(s)-1; i++ {
			if s[i] == '%' {
				j := i + 1
				for j < len(s) && strings.ContainsRune(".0123456789", rune(s[j])) {
					j++
				}
				if j < len(s) {
					out = append(out, string(s[j]))
				}
				i = j
			}
		}
		return strings.Join(out, ",")
	}
	for de, e := range en {
		if verbs(de) != verbs(e) {
			t.Errorf("Platzhalter passen nicht: %q → %q", de, e)
		}
	}
}
