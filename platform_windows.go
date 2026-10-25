package main

// Windows: Ein Programm mit Admin-Rechten (Manifest) – Fenster, Tray-Symbol und
// Steuerzentrale laufen zusammen.
//
// Aufrufe:
//
//	WlanMerge.exe              Fenster öffnen
//	WlanMerge.exe --tray       Autostart: Wächter, wartet auf die Taskleiste und startet --tray-run
//	WlanMerge.exe --tray-run   nur im Tray starten
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

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	wopts "github.com/wailsapp/wails/v2/pkg/options/windows"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"

	"wlanmerge/internal/autostart"
	"wlanmerge/internal/config"
	"wlanmerge/internal/i18n"
	"wlanmerge/internal/nativehost"
	"wlanmerge/internal/safety"
	"wlanmerge/internal/service"
	"wlanmerge/internal/sound"
	"wlanmerge/internal/trayicon"
)

const (
	osName     = "Windows"
	trayHint   = "Du findest es unten rechts in der Taskleiste (evtl. unter ^). Beenden: Rechtsklick auf das Symbol → Beenden."
	closeQuits = false // Schließen versteckt ins Tray
	notifyHere = true  // Limit-Meldungen zeigt dieses Programm
)

func platformMain(args []string) {
	tray := false
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
			supervise(logFile)
			return
		case "--tray-run":
			tray = true
		}
	}
	runGUI(tray, service.New())
}

func platformOptions(o *options.App) {
	o.Windows = &wopts.Options{Theme: wopts.SystemDefault}
}

func platformStartup(a *App) {
	s := a.svc.(*service.Service)
	go nativehost.Serve(a.ctx, s.HandleOffer) // Angebote der Browser-Erweiterung
	setupNotifications(a)
	go a.runTray()
	if s.Config().AutoConnect {
		go func() { autoConnect(s); a.emit() }()
	}
}

func exeDir() string {
	exe, _ := os.Executable()
	return filepath.Dir(exe)
}

func extensionDir() string { return filepath.Join(exeDir(), "extension") }

func openFolder(p string) { exec.Command("explorer.exe", p).Start() }

// ---------- Benachrichtigungen ----------

var iconPath string

// setupNotifications meldet WlanMerge bei Windows für Benachrichtigungen an
// und legt das Logo ab, das in der Meldung angezeigt wird.
func setupNotifications(a *App) {
	wr.InitializeNotifications(a.ctx)
	p := filepath.Join(os.TempDir(), "wlanmerge-logo.png")
	if os.WriteFile(p, trayicon.Logo, 0o644) == nil {
		iconPath = p
	}
}

// notify zeigt eine Windows-Benachrichtigung. Die Meldung selbst ist stumm;
// mit withSound spielt stattdessen unser eigener Klang.
func notify(title, body string, withSound bool) {
	if withSound {
		sound.Play()
	}
	exe, _ := os.Executable()
	n := toast.Notification{
		AppID: filepath.Base(exe), // so hat Wails die App bei Windows angemeldet
		Title: title,
		Body:  body,
		Icon:  iconPath,
		Audio: toast.Silent,
	}
	n.Push()
}

func notifyLimit(text string) {
	notify(i18n.T("Datenlimit erreicht"), i18n.Tf("%s. Das WLAN läuft normal weiter.", text), true)
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
