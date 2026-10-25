// wlanmerge-host ist der kleine Helfer, den Chrome/Edge für die
// WlanMerge-Erweiterung startet (Native Messaging). Er läuft OHNE Admin-Rechte,
// liest genau eine Nachricht, reicht sie über die geschützte Pipe an die
// laufende WlanMerge-App weiter und gibt die Antwort zurück.
package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"os"

	"wlanmerge/internal/nativehost"
)

func main() {
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
	if json.Unmarshal(buf, &o) != nil || o.Validate() != nil {
		reply(nativehost.Reply{Reason: "ungültige Nachricht"})
		return
	}
	reply(nativehost.Forward(o))
}

func reply(r nativehost.Reply) {
	b, _ := json.Marshal(r)
	binary.Write(os.Stdout, binary.LittleEndian, uint32(len(b)))
	os.Stdout.Write(b)
}
