package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
		t.Fatalf("fehlende Ãœbersetzung muss den deutschen Text liefern: %q", got)
	}
}

// Jede englische Vorlage muss dieselben Platzhalter haben wie die deutsche,
// sonst stÃ¼nde in der Meldung z. B. "%!d(MISSING)".
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
			t.Errorf("Platzhalter passen nicht: %q â†’ %q", de, e)
		}
	}
}

// Jeder Text, der im Go-Code Ã¼bersetzt wird, braucht einen englischen Eintrag.
func TestAllStringsTranslated(t *testing.T) {
	re := regexp.MustCompile(`i18n\.(?:T|Tf|Errorf)\("((?:[^"\\]|\\.)*)"`)
	filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != "../.." && (d.Name() == "node_modules" || d.Name() == "third_party" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, _ := os.ReadFile(p)
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			s, err := strconv.Unquote(`"` + m[1] + `"`)
			if err != nil {
				continue
			}
			if _, ok := en[s]; !ok {
				t.Errorf("%s: keine Ãœbersetzung fÃ¼r %q", p, s)
			}
		}
		return nil
	})
}
