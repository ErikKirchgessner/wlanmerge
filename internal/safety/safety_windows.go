package safety

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"wlanmerge/internal/i18n"
	"wlanmerge/internal/netif"
)

const doneText = "Fertig: Windows-Netzwerk ist im Originalzustand"

func restoreNetwork(_ string, say func(string)) {
	if iface, err := net.InterfaceByName(netif.TunName); err == nil {
		if luid, err := winipcfg.LUIDFromIndex(uint32(iface.Index)); err == nil {
			luid.FlushRoutes(windows.AF_INET)
			luid.FlushRoutes(windows.AF_INET6)
			luid.FlushIPAddresses(windows.AF_INET)
			luid.FlushIPAddresses(windows.AF_INET6)
			say(i18n.T("Routen vom virtuellen Adapter entfernt"))
		}
		// Adapter ist noch da, obwohl kein Programm mehr läuft: Gerät entfernen.
		if removeDevice() {
			say(i18n.T("Virtuellen Adapter entfernt"))
		} else {
			say(i18n.T("WARNUNG: Virtueller Adapter konnte nicht entfernt werden (hat aber keine Routen mehr)"))
		}
	}
	hidden(exec.Command("ipconfig", "/flushdns")).Run()
	say(i18n.T("DNS-Zwischenspeicher geleert"))
}

// killProcess beendet ein hängendes WlanMerge. Nach einem Neustart kann
// Windows dieselbe Prozessnummer an ein anderes Programm vergeben haben –
// deshalb beenden wir nur, wenn der Prozess wirklich WlanMerge ist.
func killProcess(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false // läuft nicht mehr
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) != nil {
		return false
	}
	exe := strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:size])))
	if exe != "wlanmerge.exe" {
		return false
	}
	if windows.TerminateProcess(h, 1) != nil {
		return false
	}
	windows.WaitForSingleObject(h, 5000)
	return true
}

func removeDevice() bool {
	ps := fmt.Sprintf(`$a = Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue; if ($a) { pnputil /remove-device "$($a.PnPDeviceID)" | Out-Null }; Start-Sleep -Milliseconds 500; if (Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue) { exit 1 }`, netif.TunName, netif.TunName)
	out, err := hidden(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)).CombinedOutput()
	return err == nil && !strings.Contains(string(out), "Error")
}

// hidden verhindert, dass beim Aufräumen kurz schwarze Konsolenfenster aufblitzen.
func hidden(c *exec.Cmd) *exec.Cmd {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return c
}
