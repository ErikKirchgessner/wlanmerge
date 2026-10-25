// Package wintundll bringt den Wintun-Treiber (wintun.dll, signiert von
// WireGuard LLC) im Programm mit und legt ihn bei Bedarf neben die .exe.
// So reicht eine einzige Datei.
package wintundll

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed wintun.dll
var dll []byte

// Ensure stellt sicher, dass wintun.dll neben dem Programm liegt.
func Ensure() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	p := filepath.Join(filepath.Dir(exe), "wintun.dll")
	if cur, err := os.ReadFile(p); err == nil && bytes.Equal(cur, dll) {
		return nil
	}
	return os.WriteFile(p, dll, 0o644)
}
