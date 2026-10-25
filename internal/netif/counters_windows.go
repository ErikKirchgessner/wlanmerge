package netif

import "golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

// Counters liefert die Bytes, die Windows für diesen Adapter gezählt hat
// (empfangen + gesendet, seit der Adapter verbunden ist) – aller Verkehr,
// nicht nur der von WlanMerge. ok=false, wenn Windows nichts liefert.
func Counters(a Adapter) (total uint64, ok bool) {
	luid, err := winipcfg.LUIDFromIndex(a.Index)
	if err != nil {
		return 0, false
	}
	row, err := luid.Interface()
	if err != nil {
		return 0, false
	}
	return row.InOctets + row.OutOctets, true
}
