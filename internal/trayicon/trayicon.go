// Package trayicon enthält die Tray-Symbole.
package trayicon

import _ "embed"

//go:embed on.ico
var On []byte

//go:embed off.ico
var Off []byte

// Logo für Windows-Benachrichtigungen.
//
//go:embed logo.png
var Logo []byte
