package config

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func dataDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "WlanMerge")
}

// harden: Nur Administratoren und SYSTEM dürfen schreiben, normale Benutzer
// nur lesen. Sonst könnte ein Programm ohne Admin-Rechte z. B. den Status
// unterschieben.
func harden() {
	sd, err := windows.SecurityDescriptorFromString("D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)")
	if err != nil {
		return
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return
	}
	windows.SetNamedSecurityInfo(Dir(), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
