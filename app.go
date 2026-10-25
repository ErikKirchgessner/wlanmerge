package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"wlanmerge/internal/autostart"
	"wlanmerge/internal/balancer"
	"wlanmerge/internal/i18n"
	"wlanmerge/internal/service"
)

// UIState ist das, was die Oberfläche jede Sekunde bekommt.
type UIState struct {
	State     service.State `json:"state"`
	Autostart bool          `json:"autostart"`
	OS        string        `json:"os"` // "Windows", "macOS" oder "Linux" (für Texte)
}

// App verbindet Fenster und Tray mit der Steuerzentrale.
// Alle großgeschriebenen Methoden sind aus JavaScript aufrufbar.
type App struct {
	ctx       context.Context
	svc       Backend
	quitting  atomic.Bool
	autostart atomic.Bool
	visible   atomic.Bool // Fenster sichtbar? Sonst sparen wir uns die Anzeige-Updates

	tray trayState // Tray-Symbol im selben Programm (Windows, Linux)

	notifyMu     sync.Mutex
	lastNotified time.Time
}

func NewApp(startHidden bool, b Backend) *App {
	a := &App{svc: b, lastNotified: time.Now()}
	a.visible.Store(!startHidden)
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go func() { a.autostart.Store(autostart.IsEnabled()) }() // kann ~0,1 s dauern – nicht den Start bremsen
	platformStartup(a)
	go a.loop()
}

// autoConnect schaltet beim Programmstart ein. Direkt nach dem Hochfahren ist
// das WLAN oft noch nicht verbunden – dann bis zu 5 Minuten geduldig warten.
func autoConnect(b Backend) {
	for deadline := time.Now().Add(5 * time.Minute); time.Now().Before(deadline); time.Sleep(5 * time.Second) {
		if b.Snapshot().On {
			return // inzwischen von Hand eingeschaltet
		}
		if !b.HasLinks() {
			continue
		}
		// Genau ein Versuch: bei einem echten Fehler nicht ständig neu einschalten
		// (das würde jedes Mal kurz alle Verbindungen unterbrechen).
		b.Start()
		return
	}
}

func (a *App) domReady(ctx context.Context) { a.emit() }

// beforeClose: Schließen-Knopf versteckt nur (Tray bzw. Menüleiste).
// Beenden geht übers Tray-Menü.
func (a *App) beforeClose(ctx context.Context) bool {
	if a.quitting.Load() || closeQuits {
		return false
	}
	a.Hide()
	return true
}

func (a *App) shutdown(ctx context.Context) {
	a.svc.Close() // Windows/Linux: Netzwerk sauber zurücksetzen; macOS: nur Verbindung trennen
	a.stopTray()
}

// loop schickt jede Sekunde den Zustand an Fenster und Tray.
func (a *App) loop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		a.emit()
	}
}

func (a *App) emit() {
	st := a.svc.Snapshot()
	if st.Lang != "" && st.Lang != i18n.Lang() {
		i18n.Set(st.Lang) // macOS: Sprache kommt vom Dienst
	}
	// Versteckt oder minimiert: niemand sieht hin, also nichts zeichnen lassen.
	if a.visible.Load() && !wr.WindowIsMinimised(a.ctx) {
		wr.EventsEmit(a.ctx, "state", a.uiState(st))
	}
	a.updateTray(st)
	if notifyHere {
		a.notifyLimits(st.Events)
	}
}

func (a *App) uiState(st service.State) UIState {
	return UIState{State: st, Autostart: a.autostart.Load(), OS: osName}
}

// notifyLimits zeigt für jede neue Limit-Meldung eine Benachrichtigung mit
// unserem eigenen Klang.
func (a *App) notifyLimits(events []balancer.Event) {
	a.notifyMu.Lock()
	defer a.notifyMu.Unlock()
	for _, e := range events {
		if e.Kind != balancer.EventLimit || !e.Time.After(a.lastNotified) {
			continue
		}
		a.lastNotified = e.Time
		notifyLimit(e.Text)
	}
}

// ---------- Aus JavaScript aufrufbar ----------

func (a *App) Show() {
	a.visible.Store(true)
	wr.WindowShow(a.ctx)
	wr.WindowUnminimise(a.ctx)
	a.emit()
}

// Hide versteckt das Fenster – WlanMerge läuft im Hintergrund weiter. Beim
// ersten Mal gibt es einen Hinweis, wo man es wiederfindet.
func (a *App) Hide() {
	a.visible.Store(false)
	if !a.svc.Config().HideHintShown {
		a.svc.MarkHideHintShown()
		go notify(i18n.T("WlanMerge läuft im Hintergrund weiter"), i18n.T(trayHint), false)
	}
	if closeQuits { // macOS: Menüleisten-Symbol und Dienst laufen weiter, das Fenster darf ganz zu
		a.Quit()
		return
	}
	wr.WindowHide(a.ctx)
}

func (a *App) Minimise() { wr.WindowMinimise(a.ctx) }

func (a *App) Quit() {
	a.quitting.Store(true)
	wr.Quit(a.ctx)
}

func (a *App) GetState() UIState { return a.uiState(a.svc.Snapshot()) }

// Toggle schaltet ein oder aus. Fehler stehen danach in state.error.
func (a *App) Toggle() {
	if a.svc.Snapshot().On {
		a.svc.Stop()
	} else {
		a.emit() // "verbinde …" sofort anzeigen
		go func() {
			a.svc.Start()
			a.emit()
		}()
		return
	}
	a.emit()
}

func (a *App) after(err error) string {
	a.emit()
	if err != nil {
		return err.Error()
	}
	return ""
}

func (a *App) SetLimitGB(name string, gb float64) string {
	return a.after(a.svc.SetLimit(name, int64(gb*1e9)))
}
func (a *App) ResetUsage(name string) string { return a.after(a.svc.ResetUsage(name)) }
func (a *App) SetEnabled(name string, on bool) string {
	return a.after(a.svc.SetDisabled(name, !on))
}
func (a *App) SetAutoConnect(on bool) string { return a.after(a.svc.SetAutoConnect(on)) }
func (a *App) SetResetDay(day int) string    { return a.after(a.svc.SetResetDay(day)) }
func (a *App) SetRebalance(on bool) string   { return a.after(a.svc.SetRebalance(on)) }
func (a *App) SetTheme(theme string) string  { return a.after(a.svc.SetTheme(theme)) }

// SetLanguage stellt die Sprache um ("de" oder "en").
func (a *App) SetLanguage(lang string) string {
	i18n.Set(lang)
	return a.after(a.svc.SetLang(lang))
}
func (a *App) Emergency() []string { defer a.emit(); return a.svc.Emergency() }

func (a *App) SetAutostart(on bool) string {
	var err error
	if on {
		err = autostart.Enable()
	} else {
		err = autostart.Disable()
	}
	a.autostart.Store(autostart.IsEnabled())
	return a.after(err)
}

func (a *App) SetDownloadsEnabled(on bool) string { return a.after(a.svc.SetDownloadsEnabled(on)) }

// SpeedTest startet den Speedtest; den Fortschritt liefert das "state"-Ereignis.
func (a *App) SpeedTest() string { return a.after(a.svc.StartSpeedTest()) }

// SaveSpeedTest speichert das letzte Ergebnis zum Vergleichen.
func (a *App) SaveSpeedTest() string { return a.after(a.svc.SaveSpeedTest()) }

// OpenExtensionFolder öffnet den Ordner der Browser-Erweiterung (zum Laden in Chrome/Edge).
func (a *App) OpenExtensionFolder() { openFolder(extensionDir()) }

// trayTooltip: kurzer Zustand fürs Tray- bzw. Menüleisten-Symbol.
func trayTooltip(st service.State) string {
	if st.On {
		return i18n.Tf("WlanMerge – aktiv · ↓ %.1f Mbit/s", float64(st.RxRate)*8/1e6)
	}
	return i18n.T("WlanMerge – aus")
}
