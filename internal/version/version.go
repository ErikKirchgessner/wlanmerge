// Package version nennt die Version von WlanMerge (muss zu wails.json und zum
// Git-Tag der Veröffentlichung passen) und vergleicht Versionsnummern.
package version

import (
	"strconv"
	"strings"
)

// Current ist die Version dieses Programms.
const Current = "1.2.0"

// Repo ist das GitHub-Projekt, aus dem neue Versionen kommen.
const Repo = "ErikKirchgessner/wlanmerge"

// Newer: Ist a neuer als b? ("v1.10.0" > "1.9.3"; Unlesbares gilt als 0.)
func Newer(a, b string) bool {
	x, y := parse(a), parse(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func parse(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i] // "1.2.0-beta" → "1.2.0"
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
