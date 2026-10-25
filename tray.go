package main

// Tray-Symbol unten rechts in der Taskleiste.

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/energye/systray"

	"wlanmerge/internal/i18n"
	"wlanmerge/internal/service"
	"wlanmerge/internal/trayicon"
)

type trayState struct {
	mu                 sync.Mutex
	open, toggle, quit *systray.MenuItem
	on                 bool
	lang, tip          string
	tipOn              bool
	tipAt              time.Time
	ready              atomic.Bool
}

func (a *App) runTray() {
	if !trayAvailable() {
		return // keine Leiste für Tray-Symbole (z. B. GNOME ohne Erweiterung)
	}
	runtime.LockOSThread() // der Tray braucht einen eigenen, festen Thread
	afterReady := trayPrepare()
	systray.Run(func() {
		systray.SetIcon(trayicon.Off)
		systray.SetTooltip("WlanMerge")
		systray.SetOnClick(func(systray.IMenu) { a.Show() })
		systray.SetOnDClick(func(systray.IMenu) { a.Show() })
		t := &a.tray
		t.mu.Lock()
		t.open = systray.AddMenuItem(i18n.T("Öffnen"), "")
		t.toggle = systray.AddMenuItem(i18n.T("Einschalten"), "")
		systray.AddSeparator()
		t.quit = systray.AddMenuItem(i18n.T("Beenden"), i18n.T("WlanMerge ausschalten und beenden"))
		t.lang = i18n.Lang()
		t.mu.Unlock()
		t.open.Click(a.Show)
		t.toggle.Click(func() { a.Toggle() })
		t.quit.Click(a.Quit)
		t.ready.Store(true)
		go afterReady()
	}, nil)
}

func (a *App) updateTray(st service.State) {
	t := &a.tray
	if !t.ready.Load() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	langChanged := t.lang != i18n.Lang()
	if langChanged {
		t.lang = i18n.Lang()
		t.open.SetTitle(i18n.T("Öffnen"))
		t.quit.SetTitle(i18n.T("Beenden"))
		t.quit.SetTooltip(i18n.T("WlanMerge ausschalten und beenden"))
	}
	if st.On != t.on || langChanged {
		t.on = st.On
		if st.On {
			systray.SetIcon(trayicon.On)
			t.toggle.SetTitle(i18n.T("Ausschalten"))
		} else {
			systray.SetIcon(trayicon.Off)
			t.toggle.SetTitle(i18n.T("Einschalten"))
		}
	}
	// Tooltip: nur bei Änderung, und die Geschwindigkeit höchstens alle 5 s
	// (sonst müsste die Taskleiste jede Sekunde neu zeichnen).
	tip := trayTooltip(st)
	if tip != t.tip && (langChanged || st.On != t.tipOn || time.Since(t.tipAt) >= 5*time.Second) {
		t.tip, t.tipOn, t.tipAt = tip, st.On, time.Now()
		systray.SetTooltip(tip)
	}
}

func (a *App) stopTray() {
	if a.tray.ready.Load() {
		systray.Quit()
	}
}
