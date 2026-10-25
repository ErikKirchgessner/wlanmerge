// WlanMerge – bündelt mehrere Internetverbindungen (WLAN, Handy, LAN).
//
// Läuft unter Windows, macOS und Linux mit derselben Oberfläche und denselben
// Funktionen. Welche Aufrufe es gibt, steht je Betriebssystem in
// platform_windows.go, platform_darwin.go und platform_linux.go.
package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"wlanmerge/internal/config"
	"wlanmerge/internal/service"
)

//go:embed all:frontend/dist
var assets embed.FS

var logFile *os.File

func main() {
	logFile = openLog()
	platformMain(os.Args[1:])
}

// Backend ist die Steuerzentrale, mit der Fenster und Tray sprechen:
// direkt *service.Service (Windows, Linux) oder über den Dienst (macOS).
type Backend interface {
	Snapshot() service.State
	Start() error
	Stop()
	Emergency() []string
	Config() *config.Config
	HasLinks() bool
	SetLimit(name string, bytes int64) error
	ResetUsage(name string) error
	SetDisabled(name string, off bool) error
	SetAutoConnect(on bool) error
	SetRebalance(on bool) error
	SetTheme(theme string) error
	SetResetDay(day int) error
	MarkHideHintShown() error
	SetDownloadsEnabled(on bool) error
	StartSpeedTest() error
	SaveSpeedTest() error
	SetLang(lang string) error
	Close()
}

// runGUI öffnet das Fenster (bei tray=true versteckt) und läuft, bis die App endet.
func runGUI(tray bool, b Backend) {
	app := NewApp(tray, b)
	opts := &options.App{
		Title:            "WlanMerge",
		Width:            660,
		Height:           720,
		MinWidth:         420,
		MinHeight:        480,
		Frameless:        true,
		StartHidden:      tray,
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 21, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "wlanmerge-9f3c2a7e",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.Show() },
		},
		Bind: []interface{}{app},
	}
	platformOptions(opts)
	if err := wails.Run(opts); err != nil {
		log.Print("Fehler: ", err)
		os.Exit(1)
	}
}
