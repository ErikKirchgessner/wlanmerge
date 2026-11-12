package nativehost

import (
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"wlanmerge/internal/config"
)

// PipeName ist eine Variable, damit Tests eine eigene Pipe nutzen können.
// Im Testmodus (WM_DATA_DIR gesetzt) bekommt sie einen eigenen Anhang.
var PipeName = `\\.\pipe\WlanMerge-Downloads` + config.Sandbox()

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
	if err := registerProtocol(installDir); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Link "wlanmerge:open": Ein Klick auf eine WlanMerge-Benachrichtigung öffnet
// darüber das Fenster (über den Helfer, ohne Admin-Abfrage). Der Link kann
// nichts anderes als das Fenster öffnen.
const protoKey = `SOFTWARE\Classes\wlanmerge`

func registerProtocol(installDir string) error {
	host := filepath.Join(installDir, "wlanmerge-host.exe")
	set := func(path, name, value string) error {
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer k.Close()
		return k.SetStringValue(name, value)
	}
	for _, v := range [][3]string{
		{protoKey, "", "URL:WlanMerge"},
		{protoKey, "URL Protocol", ""},
		{protoKey + `\DefaultIcon`, "", filepath.Join(installDir, "WlanMerge.exe") + ",0"},
		{protoKey + `\shell\open\command`, "", `"` + host + `" --open`},
	} {
		if err := set(v[0], v[1], v[2]); err != nil {
			return err
		}
	}
	return nil
}

// Uninstall entfernt die Anmeldungen wieder.
func Uninstall() {
	for _, p := range regPaths {
		registry.DeleteKey(registry.LOCAL_MACHINE, p)
	}
	for _, p := range []string{`\shell\open\command`, `\shell\open`, `\shell`, `\DefaultIcon`, ""} {
		registry.DeleteKey(registry.LOCAL_MACHINE, protoKey+p)
	}
}
