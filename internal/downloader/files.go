package downloader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// uniquePath sucht einen freien Namen ("Datei (1).zip"), auch gegenüber
// laufenden Teil-Dateien.
func uniquePath(dir, name string, taken func(string) bool) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if _, err := os.Stat(p + partExt); err == nil {
			continue
		}
		if taken != nil && taken(p) {
			continue
		}
		return p
	}
}
