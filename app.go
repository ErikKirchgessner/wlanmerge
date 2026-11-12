package main

// Das Fenster. Es läuft nur, solange es offen ist, und spricht über remote mit
// dem Hintergrunddienst (core.go). Schließen beendet das Fenster-Programm
// ganz – WlanMerge selbst läuft im Tray weiter.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"wlanmerge/internal/autostart"
	"wlanmerge/internal/config"
	"wlanmerge/internal/i18n"
	"wlanmerge/internal/remote"
	"wlanmerge/internal/service"
)

// UIState ist das, was die Oberfläche jede Sekunde bekommt.
type UIState struct {
	State     service.State `json:"state"`
	Autostart bool          `json:"autostart"`
	OS        string        `json:"os"`
}

// App ist die Brücke zwischen Oberfläche und Dienst.
// Alle großgeschriebenen Methoden sind aus JavaScript aufrufbar.
type App struct {
	ctx       context.Context
	c         *remote.Client
	closing   atomic.Bool
	autostart atomic.Bool

	emitMu  sync.Mutex
	lastKey string
	fails   int
	asleep  bool // minimiert: Oberfläche hält Animationen an
}

func NewApp(c *remote.Client) *App { return &App{c: c} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go func() { a.autostart.Store(autostart.IsEnabled()) }() // kann ~0,1 s dauern
	go a.loop()
}

func (a *App) domReady(ctx context.Context) { a.emit(true) }

// beforeClose: Schließen beendet das Fenster; der Dienst erfährt es (für den
// einmaligen Hinweis "läuft im Hintergrund weiter").
func (a *App) beforeClose(ctx context.Context) bool {
	if !a.closing.Swap(true) {
		a.c.WindowClosed()
	}
	return false
}

func (a *App) shutdown(ctx context.Context) { a.c.Close() }

// loop schickt jede Sekunde den Zustand an die Oberfläche.
func (a *App) loop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		a.emit(false)
	}
}

// emit holt den Zustand vom Dienst und zeigt ihn an. Minimiert sieht niemand
// hin – dann nur, wenn sich am Ein/Aus-Zustand etwas ändert (damit nach dem
// Wiederherstellen nie ein veralteter Zustand zu sehen ist). Ist der Dienst
// weg (z. B. über das Tray beendet), schließt sich das Fenster.
func (a *App) emit(force bool) {
	if a.closing.Load() {
		return // wird gerade geschlossen: nichts mehr anzeigen
	}
	st, ok := a.c.Snapshot()
	a.emitMu.Lock()
	defer a.emitMu.Unlock()
	if !ok {
		if a.fails++; a.fails >= 3 && !a.closing.Swap(true) {
			wr.Quit(a.ctx)
		}
		return
	}
	a.fails = 0
	if st.Lang != "" && st.Lang != i18n.Lang() {
		i18n.Set(st.Lang)
	}
	key := fmt.Sprint(st.On, st.Want, st.Paused, st.Busy, st.Error, st.Lang, st.Theme)
	min := wr.WindowIsMinimised(a.ctx)
	if min != a.asleep {
		a.asleep = min
		wr.EventsEmit(a.ctx, "sleep", min)
		force = force || !min // wiederhergestellt: sofort den aktuellen Stand
	}
	if !force && key == a.lastKey && min {
		return
	}
	a.lastKey = key
	wr.EventsEmit(a.ctx, "state", a.uiState(st))
}

func (a *App) uiState(st service.State) UIState {
	return UIState{State: st, Autostart: a.autostart.Load(), OS: "Windows"}
}

func (a *App) after(err error) string {
	a.emit(true)
	if err != nil {
		return err.Error()
	}
	return ""
}

// ---------- Aus JavaScript aufrufbar ----------

// Show holt das Fenster nach vorn (z. B. wenn es ein zweites Mal geöffnet wird).
func (a *App) Show() {
	wr.WindowUnminimise(a.ctx)
	wr.WindowShow(a.ctx)
	a.emit(true)
}

// Hide (Schließen-Knopf) beendet das Fenster – WlanMerge läuft im Tray weiter.
func (a *App) Hide() {
	if !a.closing.Swap(true) {
		a.c.WindowClosed()
	}
	wr.Quit(a.ctx)
}

func (a *App) Minimise() { wr.WindowMinimise(a.ctx) }

func (a *App) GetState() UIState {
	st, _ := a.c.Snapshot()
	return a.uiState(st)
}

// Toggle schaltet ein oder aus. Fehler stehen danach in state.error.
func (a *App) Toggle() string {
	st, _ := a.c.Snapshot()
	return a.after(a.c.SetOn(!st.Want))
}

func (a *App) SetLimitGB(name string, gb float64) string {
	return a.after(a.c.SetLimit(name, int64(gb*1e9)))
}
func (a *App) ResetUsage(name string) string { return a.after(a.c.ResetUsage(name)) }
func (a *App) SetEnabled(name string, on bool) string {
	return a.after(a.c.SetDisabled(name, !on))
}
func (a *App) SetAutoConnect(on bool) string      { return a.after(a.c.SetAutoConnect(on)) }
func (a *App) SetAutoPause(on bool) string        { return a.after(a.c.SetAutoPause(on)) }
func (a *App) SetResetDay(day int) string         { return a.after(a.c.SetResetDay(day)) }
func (a *App) SetRebalance(on bool) string        { return a.after(a.c.SetRebalance(on)) }
func (a *App) SetTheme(theme string) string       { return a.after(a.c.SetTheme(theme)) }
func (a *App) SetDownloadsEnabled(on bool) string { return a.after(a.c.SetDownloadsEnabled(on)) }
func (a *App) SetAppFilter(on bool) string        { return a.after(a.c.SetAppFilter(on)) }
func (a *App) SetUpdateCheck(on bool) string      { return a.after(a.c.SetUpdateCheck(on)) }

// SetPhoneApp erlaubt oder verbietet einem Programm (z. B. "steam.exe") das Handy.
func (a *App) SetPhoneApp(name string, allowed bool) string {
	return a.after(a.c.SetPhoneApp(name, allowed))
}

// FinishOnboarding schließt die Einrichtung ab und schaltet auf Wunsch ein.
func (a *App) FinishOnboarding(turnOn bool) string {
	if err := a.c.SetOnboarded(); err != nil {
		return a.after(err)
	}
	if turnOn {
		return a.after(a.c.SetOn(true))
	}
	return a.after(nil)
}

// SetLanguage stellt die Sprache um ("de" oder "en").
func (a *App) SetLanguage(lang string) string {
	i18n.Set(lang)
	return a.after(a.c.SetLang(lang))
}

func (a *App) Emergency() []string { defer a.emit(true); return a.c.Emergency() }

func (a *App) SetAutostart(on bool) string {
	if config.Sandbox() != "" {
		return i18n.T("Testmodus: Das Netzwerk wird nicht verändert")
	}
	var err error
	if on {
		err = autostart.Enable()
	} else {
		err = autostart.Disable()
	}
	a.autostart.Store(autostart.IsEnabled())
	return a.after(err)
}

// SpeedTest startet den Speedtest; das Ergebnis liefert das "state"-Ereignis.
func (a *App) SpeedTest() string { return a.after(a.c.StartSpeedTest()) }

// SaveSpeedTest speichert das letzte Ergebnis zum Vergleichen.
func (a *App) SaveSpeedTest() string { return a.after(a.c.SaveSpeedTest()) }

// CopyDiagnose legt einen technischen Bericht (ohne IP-Adressen und Namen) in
// die Zwischenablage – zum Einfügen in eine Fehlermeldung.
func (a *App) CopyDiagnose() string {
	text := a.c.Diagnose()
	if text == "" {
		return i18n.T("WlanMerge läuft nicht")
	}
	if err := wr.ClipboardSetText(a.ctx, text); err != nil {
		return err.Error()
	}
	return ""
}

// OpenURL öffnet einen Link im Browser – nur Seiten auf GitHub (Update, Hilfe).
func (a *App) OpenURL(u string) string {
	if !strings.HasPrefix(u, "https://github.com/") {
		return errors.New("blocked").Error()
	}
	openURL(u)
	return ""
}

// OpenExtensionFolder öffnet den Ordner der Browser-Erweiterung (zum Laden in Chrome/Edge).
func (a *App) OpenExtensionFolder() { openFolder(extensionDir()) }
