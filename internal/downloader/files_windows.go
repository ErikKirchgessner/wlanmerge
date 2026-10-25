package downloader

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DefaultDir ist der Download-Ordner des angemeldeten Benutzers.
func DefaultDir() string {
	if p, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0); err == nil && p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// setSparse: beim Schreiben weit hinten in der Datei muss Windows den Bereich
// davor sonst erst mit Nullen füllen (bremst große Downloads).
func setSparse(f *os.File) {
	const fsctlSetSparse = 0x000900c4
	var ret uint32
	windows.DeviceIoControl(windows.Handle(f.Fd()), fsctlSetSparse, nil, 0, nil, 0, &ret, nil)
}

// markFromInternet setzt die "aus dem Internet"-Markierung wie ein Browser,
// damit Windows/SmartScreen die Datei richtig behandeln.
func markFromInternet(p, hostURL, referrer string) {
	s := "[ZoneTransfer]\r\nZoneId=3\r\n"
	if referrer != "" {
		s += "ReferrerUrl=" + referrer + "\r\n"
	}
	if hostURL != "" {
		s += "HostUrl=" + hostURL + "\r\n"
	}
	os.WriteFile(p+":Zone.Identifier", []byte(s), 0o644)
}

// giveToUser: WlanMerge läuft mit Admin-Rechten; ohne diesen Schritt würde die
// Datei der Gruppe "Administratoren" gehören statt dem Benutzer.
func giveToUser(p string) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return
	}
	windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, u.User.Sid, nil, nil, nil)
}
