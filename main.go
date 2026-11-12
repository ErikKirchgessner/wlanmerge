// WlanMerge – bündelt mehrere Internetverbindungen (WLAN, Handy-Hotspot, LAN).
//
// Ein Programm, zwei Rollen:
//   - der Hintergrunddienst (core.go): Bündelung, Tray-Symbol, Meldungen
//   - das Fenster (app.go): läuft nur, solange es offen ist
//
// Welche Aufrufe es gibt, steht in platform_windows.go.
package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wopts "github.com/wailsapp/wails/v2/pkg/options/windows"

	"wlanmerge/internal/config"
	"wlanmerge/internal/remote"
)

//go:embed all:frontend/dist
var assets embed.FS

var logFile *os.File

func main() {
	logFile = openLog()
	platformMain(os.Args[1:])
}

// runGUI öffnet das Fenster und läuft, bis es geschlossen wird.
func runGUI(c *remote.Client) {
	app := NewApp(c)
	opts := &options.App{
		Title:            "WlanMerge",
		Width:            660,
		Height:           720,
		MinWidth:         420,
		MinHeight:        480,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 21, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "wlanmerge-9f3c2a7e" + config.Sandbox(),
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.Show() },
		},
		Windows: &wopts.Options{Theme: wopts.SystemDefault},
		Bind:    []interface{}{app},
	}
	if err := wails.Run(opts); err != nil {
		log.Print("Fenster: ", err)
		os.Exit(1)
	}
}
