package nativehost

import (
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// PipeName ist eine Variable, damit Tests eine eigene Pipe nutzen können.
var PipeName = `\\.\pipe\WlanMerge-Downloads`

// listen öffnet die Pipe – nur der angemeldete Benutzer (und SYSTEM) darf sie
// öffnen, Verbindungen aus dem Netzwerk lehnt winio ab.
func listen() (net.Listener, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sddl := "D:P(A;;GA;;;" + u.User.Sid.String() + ")(A;;GA;;;SY)"
	return winio.ListenPipe(PipeName, &winio.PipeConfig{SecurityDescriptor: sddl, InputBufferSize: maxMessage, OutputBufferSize: 4096})
}

func dial(timeout time.Duration) (net.Conn, error) { return winio.DialPipe(PipeName, &timeout) }

var regPaths = []string{
	`SOFTWARE\Google\Chrome\NativeMessagingHosts\` + HostName,
	`SOFTWARE\Microsoft\Edge\NativeMessagingHosts\` + HostName,
	`SOFTWARE\Chromium\NativeMessagingHosts\` + HostName,
	`SOFTWARE\BraveSoftware\Brave-Browser\NativeMessagingHosts\` + HostName,
}

// Install schreibt das Host-Manifest und meldet es bei Chrome, Edge, Chromium und Brave an.
func Install(installDir string) error {
	mp := filepath.Join(installDir, "wlanmerge-host.json")
	if err := os.WriteFile(mp, manifest(filepath.Join(installDir, "wlanmerge-host.exe")), 0o644); err != nil {
		return err
	}
	var firstErr error
	for _, p := range regPaths {
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, p, registry.SET_VALUE)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		k.SetStringValue("", mp)
		k.Close()
	}
	return firstErr
}

// Uninstall entfernt die Anmeldungen wieder.
func Uninstall() {
	for _, p := range regPaths {
		registry.DeleteKey(registry.LOCAL_MACHINE, p)
	}
}
