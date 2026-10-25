// Package autostart richtet die Windows-Aufgaben ein:
//   - "WlanMerge": startet das Programm beim Anmelden (im Tray, ohne UAC-Abfrage)
//   - "WlanMerge Reset": setzt beim Hochfahren das Netzwerk sicherheitshalber zurück
//
// Wir benutzen XML statt einfacher schtasks-Parameter, weil Windows sonst
// standardmäßig "nur am Netzteil starten" und "nach 3 Tagen beenden" einstellt.
package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

const (
	TaskStart = "WlanMerge"
	TaskReset = "WlanMerge Reset"
)

const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>%s</Description></RegistrationInfo>
  <Triggers>%s</Triggers>
  <Principals><Principal id="Author">%s<RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <Enabled>true</Enabled>
    <Priority>5</Priority>
  </Settings>
  <Actions Context="Author"><Exec><Command>%s</Command><Arguments>%s</Arguments></Exec></Actions>
</Task>`

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func schtasks(args ...string) error {
	cmd := exec.Command("schtasks", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // kein Konsolenfenster
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func create(name, xml string) error {
	f, err := os.CreateTemp("", "wlanmerge-task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	// schtasks erwartet UTF-16 mit BOM
	u := utf16.Encode(append([]rune{0xFEFF}, []rune(xml)...))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[i*2], b[i*2+1] = byte(v), byte(v>>8)
	}
	f.Write(b)
	f.Close()
	return schtasks("/Create", "/TN", name, "/XML", f.Name(), "/F")
}

func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}

// Enable: WlanMerge startet beim Anmelden des aktuellen Benutzers im Tray.
func Enable() error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	who := xmlEscape(u.Username)
	trigger := "<LogonTrigger><Enabled>true</Enabled><UserId>" + who + "</UserId><Delay>PT5S</Delay></LogonTrigger>"
	principal := "<UserId>" + who + "</UserId><LogonType>InteractiveToken</LogonType>"
	return create(TaskStart, fmt.Sprintf(taskXML, "WlanMerge beim Anmelden starten", trigger, principal, xmlEscape(exe), "--tray"))
}

func Disable() error {
	if !IsEnabled() {
		return nil
	}
	return schtasks("/Delete", "/TN", TaskStart, "/F")
}

func IsEnabled() bool { return schtasks("/Query", "/TN", TaskStart) == nil }

// InstallReset richtet das Zurücksetzen beim Hochfahren ein (läuft als SYSTEM).
func InstallReset() error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	trigger := "<BootTrigger><Enabled>true</Enabled></BootTrigger>"
	principal := "<UserId>S-1-5-18</UserId>"
	return create(TaskReset, fmt.Sprintf(taskXML, "WlanMerge: Netzwerk nach Neustart sicher zurücksetzen", trigger, principal, xmlEscape(exe), "--restore"))
}

// RemoveAll entfernt beide Aufgaben (beim Deinstallieren).
func RemoveAll() {
	schtasks("/Delete", "/TN", TaskStart, "/F")
	schtasks("/Delete", "/TN", TaskReset, "/F")
}
