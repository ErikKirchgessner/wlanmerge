package main

// Windows: Ein Programm mit Admin-Rechten (Manifest) in zwei Rollen –
// Hintergrunddienst und Fenster (siehe main.go).
//
// Aufrufe:
//
//	WlanMerge.exe              Fenster öffnen (startet den Dienst, falls er nicht läuft)
//	WlanMerge.exe --tray       Autostart: Wächter, wartet auf die Taskleiste und startet den Dienst
//	WlanMerge.exe --tray-start wie --tray, aber ohne zu warten (vom Fenster aus)
//	WlanMerge.exe --tray-run   der Dienst selbst
//	WlanMerge.exe --restore    Notausschalter: Netzwerk zurücksetzen und beenden
//	WlanMerge.exe --setup      Windows-Aufgaben einrichten (macht der Installer)
//	WlanMerge.exe --uninstall  alles entfernen (macht der Deinstaller)

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"wlanmerge/internal/autostart"
	"wlanmerge/internal/config"
	"wlanmerge/internal/i18n"
	"wlanmerge/internal/nativehost"
	"wlanmerge/internal/remote"
	"wlanmerge/internal/safety"
)

const trayHint = "Du findest es unten rechts in der Taskleiste (evtl. unter ^). Beenden: Rechtsklick auf das Symbol → Beenden."

func platformMain(args []string) {
	for _, a := range args {
		switch a {
		case "--restore":
			safety.Restore(func(string) {})
			return
		case "--setup":
			autostart.InstallReset()
			autostart.Enable()
			nativehost.Install(exeDir()) // Browser-Erweiterung anmelden
			return
		case "--uninstall":
			safety.Restore(func(string) {})
			autostart.RemoveAll()
			nativehost.Uninstall()
			os.RemoveAll(config.Dir())
			os.Remove(filepath.Join(exeDir(), "wintun.dll"))
			return
		case "--tray":
			supervise(logFile, true)
			return
		case "--tray-start":
			supervise(logFile, false)
			return
		case "--tray-run":
			runCore()
			return
		}
	}
	runWindow()
}

// runWindow öffnet das Fenster. Läuft der Dienst noch nicht (z. B. Autostart
// aus oder vorher beendet), wird er zuerst gestartet.
func runWindow() {
	c := &remote.Client{}
	if !c.Alive() {
		if err := startService(); err != nil {
			log.Print("Dienst starten: ", err)
		}
		for deadline := time.Now().Add(20 * time.Second); !c.Alive(); time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				messageBox("WlanMerge", i18n.T("WlanMerge konnte nicht gestartet werden. Details stehen in wlanmerge.log im Ordner C:\\ProgramData\\WlanMerge."))
				return
			}
		}
	}
	runGUI(c)
}

// startService startet den Wächter samt Dienst als eigenständigen Prozess, der
// weiterläuft, wenn das Fenster geschlossen wird.
func startService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	const flags = windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS
	cmd := exec.Command(exe, "--tray-start")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB}
	if err := cmd.Start(); err == nil {
		return nil
	}
	cmd = exec.Command(exe, "--tray-start") // Job erlaubt kein Ausbrechen – dann eben so
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	return cmd.Start()
}

func messageBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	windows.MessageBox(0, m, t, windows.MB_OK|windows.MB_ICONWARNING)
}

func exeDir() string {
	exe, _ := os.Executable()
	return filepath.Dir(exe)
}

func extensionDir() string { return filepath.Join(exeDir(), "extension") }

func openFolder(p string) { exec.Command("explorer.exe", p).Start() }

// openURL öffnet einen Link im normalen Browser. Über explorer.exe läuft der
// Browser mit normalen Rechten, nicht mit den Admin-Rechten von WlanMerge.
func openURL(u string) {
	if u != "" {
		exec.Command("explorer.exe", u).Start()
	}
}

// ---------- Tray-Symbol nach dem Anmelden ----------

// Beim Anmelden startet WlanMerge oft, bevor die Taskleiste bereit ist – dann
// lehnt Windows das Symbol ab. Normalerweise meldet die Taskleiste später
// "TaskbarCreated" und das Symbol wird nachgetragen. Windows blockiert diese
// Meldung aber für Programme mit Admin-Rechten (Explorer hat weniger Rechte).
// Deshalb: Meldung ausdrücklich erlauben …
var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	procRegisterWindowMsg = user32.NewProc("RegisterWindowMessageW")
	procChangeMsgFilter   = user32.NewProc("ChangeWindowMessageFilter")
	procEnumThreadWindows = user32.NewProc("EnumThreadWindows")
	procPostMessage       = user32.NewProc("PostMessageW")
	findWindowW           = user32.NewProc("FindWindowW")
	taskbarCreatedMsg     uintptr
)

// trayPrepare läuft im Tray-Thread vor systray.Run; die zurückgegebene
// Funktion trägt das Symbol danach zur Sicherheit noch ein paar Mal nach.
func trayPrepare() func() {
	name, _ := windows.UTF16PtrFromString("TaskbarCreated")
	taskbarCreatedMsg, _, _ = procRegisterWindowMsg.Call(uintptr(unsafe.Pointer(name)))
	procChangeMsgFilter.Call(taskbarCreatedMsg, 1) // MSGFLT_ADD
	thread := windows.GetCurrentThreadId()
	return func() { reAddTrayIcon(thread) }
}

// … und zur Sicherheit nach dem Start ein paar Mal selbst nachtragen lassen.
// (Ist das Symbol schon da, passiert dabei nichts.)
func reAddTrayIcon(thread uint32) {
	cb := windows.NewCallback(func(hwnd, _ uintptr) uintptr {
		procPostMessage.Call(hwnd, taskbarCreatedMsg, 0, 0)
		return 1
	})
	for _, wait := range []time.Duration{3, 7, 20, 30} {
		time.Sleep(wait * time.Second)
		procEnumThreadWindows.Call(uintptr(thread), cb, 0)
	}
}

// waitForTaskbar wartet, bis die Taskleiste da ist (höchstens 90 s), plus kurze Ruhe.
func waitForTaskbar() {
	name, _ := syscall.UTF16PtrFromString("Shell_TrayWnd")
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if h, _, _ := findWindowW.Call(uintptr(unsafe.Pointer(name)), 0); h != 0 {
			time.Sleep(2 * time.Second) // Explorer braucht noch einen Moment für den Infobereich
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	log.Print("Autostart: Taskleiste nach 90 s nicht gefunden, starte trotzdem")
}
