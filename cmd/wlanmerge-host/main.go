// wlanmerge-host ist der kleine Helfer ohne Admin-Rechte. Zwei Aufgaben:
//
//  1. Chrome/Edge startet ihn für die WlanMerge-Erweiterung (Native Messaging):
//     Er liest genau eine Nachricht, reicht sie über die geschützte Pipe an den
//     laufenden WlanMerge-Dienst weiter und gibt die Antwort zurück.
//  2. "--open" (Desktop-Symbol, Startmenü, Klick auf eine Benachrichtigung):
//     Er bittet den laufenden Dienst, das Fenster zu öffnen – so fragt Windows
//     nicht jedes Mal nach Admin-Rechten. Läuft der Dienst nicht, startet er
//     WlanMerge ganz normal (dann einmal mit Nachfrage).
package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"wlanmerge/internal/nativehost"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "--open" {
		open()
		return
	}
	// Chrome übergibt die Herkunft der aufrufenden Erweiterung.
	if len(os.Args) < 2 || os.Args[1] != nativehost.Origin {
		reply(nativehost.Reply{Reason: "unbekannte Erweiterung"})
		return
	}
	var n uint32
	if binary.Read(os.Stdin, binary.LittleEndian, &n) != nil || n == 0 || n > 64<<10 {
		reply(nativehost.Reply{Reason: "ungültige Nachricht"})
		return
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(os.Stdin, buf); err != nil {
		reply(nativehost.Reply{Reason: "ungültige Nachricht"})
		return
	}
	var o nativehost.Offer
	if json.Unmarshal(buf, &o) != nil || o.Type == "open" || o.Validate() != nil {
		reply(nativehost.Reply{Reason: "ungültige Nachricht"})
		return
	}
	reply(nativehost.Forward(o))
}

// open: Fenster über den Dienst öffnen, sonst WlanMerge selbst starten.
func open() {
	if nativehost.OpenWindow() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	app := filepath.Join(filepath.Dir(exe), "WlanMerge.exe")
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(app)
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(app))
	windows.ShellExecute(0, verb, file, nil, dir, windows.SW_SHOWNORMAL) // fragt nach Admin-Rechten
}

func reply(r nativehost.Reply) {
	b, _ := json.Marshal(r)
	binary.Write(os.Stdout, binary.LittleEndian, uint32(len(b)))
	os.Stdout.Write(b)
}
