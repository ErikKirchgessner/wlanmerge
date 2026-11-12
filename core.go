package main

// Der Hintergrunddienst ("--tray-run"): Bündelung, Tray-Symbol,
// Benachrichtigungen und die Browser-Erweiterung – ganz ohne Fenster und
// Browser-Technik, deshalb nur wenige MB Arbeitsspeicher und keine Grafiklast.
// Das Fenster ist ein eigenes Programm (siehe app.go), das nur läuft, solange
// es offen ist, und über remote mit diesem Dienst spricht.

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
	"github.com/energye/systray"

	"wlanmerge/internal/balancer"
	"wlanmerge/internal/i18n"
	"wlanmerge/internal/nativehost"
	"wlanmerge/internal/remote"
	"wlanmerge/internal/service"
	"wlanmerge/internal/sound"
	"wlanmerge/internal/trayicon"
)

const (
	trayLines  = 4               // so viele Leitungen zeigt das Tray-Menü
	trayEvery  = 2 * time.Second // so oft wird das Tray-Menü aufgefrischt
	toastAppID = "WlanMerge"
	toastGUID  = "{6E3B8D2A-91C4-4F0E-A7B5-3C2D1E0F9A84}"
)

type core struct {
	s      *service.Service
	ctx    context.Context
	cancel context.CancelFunc

	quitOnce     sync.Once
	lastNotified time.Time

	// Tray-Menü (nur im Tray-Thread bzw. unter mu benutzen)
	mu                   sync.Mutex
	ready, iconOn        bool
	status, open, toggle *systray.MenuItem
	upd, quit            *systray.MenuItem
	lines                [trayLines]*systray.MenuItem
	titles               map[*systray.MenuItem]string
	hidden               map[*systray.MenuItem]bool
	tip, updURL, lang    string
	tipAt                time.Time
}

// runCore startet den Dienst und läuft, bis "Beenden" gewählt wird.
func runCore() {
	l, err := remote.Listen()
	if err != nil {
		return // es läuft schon ein WlanMerge-Dienst
	}
	c := &core{s: service.New(), lastNotified: time.Now(), titles: map[*systray.MenuItem]string{}, hidden: map[*systray.MenuItem]bool{}}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	go nativehost.Serve(c.ctx, func(o nativehost.Offer) nativehost.Reply {
		if o.Type == "open" { // Desktop-Symbol oder Klick auf eine Benachrichtigung
			openWindow()
			return nativehost.Reply{Accept: true}
		}
		return c.s.HandleOffer(o) // Angebote der Browser-Erweiterung
	})
	go remote.Serve(c.ctx, l, &remote.API{S: c.s, WindowClosed: c.windowClosed})
	setupNotifications()
	if cfg := c.s.Config(); cfg.AutoConnect && cfg.Onboarded {
		c.s.SetOn(true) // der Autopilot wartet, bis eine Leitung da ist
	}
	go c.loop()
	c.runTray() // blockiert bis zum Beenden
	c.shutdown()
}

func (c *core) shutdown() {
	c.quitOnce.Do(func() {
		c.cancel()
		c.s.Close() // Netzwerk sauber zurücksetzen, alles speichern
	})
}

// loop frischt Tray und Benachrichtigungen auf.
func (c *core) loop() {
	t := time.NewTicker(trayEvery)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
		}
		st := c.s.Snapshot()
		c.updateTray(st)
		c.notifyLimits(st.Events)
	}
}

// ---------- Tray ----------

func (c *core) runTray() {
	runtime.LockOSThread() // der Tray braucht einen festen Thread
	afterReady := trayPrepare()
	systray.Run(func() {
		systray.SetIcon(trayicon.Off)
		systray.SetTooltip("WlanMerge")
		systray.SetOnClick(func(systray.IMenu) { openWindow() })
		systray.SetOnDClick(func(systray.IMenu) { openWindow() })
		c.mu.Lock()
		c.status = systray.AddMenuItem("WlanMerge", "")
		c.status.Disable()
		for i := range c.lines {
			c.lines[i] = systray.AddMenuItem("", "")
			c.lines[i].Disable()
			c.lines[i].Hide()
			c.hidden[c.lines[i]] = true
		}
		systray.AddSeparator()
		c.open = systray.AddMenuItem(i18n.T("Öffnen"), "")
		c.toggle = systray.AddMenuItem(i18n.T("Einschalten"), "")
		c.upd = systray.AddMenuItem("", "")
		c.upd.Hide()
		c.hidden[c.upd] = true
		systray.AddSeparator()
		c.quit = systray.AddMenuItem(i18n.T("Beenden"), i18n.T("WlanMerge ausschalten und beenden"))
		c.lang = i18n.Lang()
		c.ready = true
		c.mu.Unlock()
		c.open.Click(openWindow)
		c.toggle.Click(func() {
			st := c.s.Snapshot()
			c.s.SetOn(!st.Want)
			c.updateTray(c.s.Snapshot())
		})
		c.upd.Click(func() {
			c.mu.Lock()
			u := c.updURL
			c.mu.Unlock()
			openURL(u)
		})
		c.quit.Click(func() {
			c.shutdown()
			systray.Quit()
		})
		go c.updateTray(c.s.Snapshot())
		go afterReady()
	}, c.shutdown) // auch beim Abmelden/Herunterfahren sauber zurücksetzen
}

// setTitle / show ändern Menüeinträge nur, wenn sich wirklich etwas ändert.
func (c *core) setTitle(m *systray.MenuItem, s string) {
	if c.titles[m] != s {
		c.titles[m] = s
		m.SetTitle(s)
	}
}

func (c *core) show(m *systray.MenuItem, on bool) {
	if c.hidden[m] == !on {
		return
	}
	c.hidden[m] = !on
	if on {
		m.Show()
	} else {
		m.Hide()
	}
}

func (c *core) updateTray(st service.State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return
	}
	if l := i18n.Lang(); l != c.lang {
		c.lang = l
		c.quit.SetTooltip(i18n.T("WlanMerge ausschalten und beenden"))
	}
	c.setTitle(c.status, trayStatus(st))
	c.setTitle(c.open, i18n.T("Öffnen"))
	c.setTitle(c.quit, i18n.T("Beenden"))
	if st.Want {
		c.setTitle(c.toggle, i18n.T("Ausschalten"))
	} else {
		c.setTitle(c.toggle, i18n.T("Einschalten"))
	}
	for i, m := range c.lines {
		if i >= len(st.Links) {
			c.show(m, false)
			continue
		}
		c.setTitle(m, trayLine(st.Links[i], st.On))
		c.show(m, true)
	}
	if st.Update != nil {
		c.updURL = st.Update.URL
		if st.Update.Download != "" {
			c.updURL = st.Update.Download // lädt gleich den neuen Installer
		}
		c.setTitle(c.upd, i18n.Tf("Neue Version %s herunterladen", st.Update.Version))
	}
	c.show(c.upd, st.Update != nil)

	if st.On != c.iconOn {
		c.iconOn = st.On
		if st.On {
			systray.SetIcon(trayicon.On)
		} else {
			systray.SetIcon(trayicon.Off)
		}
	}
	// Tooltip: nur bei Änderung, die Geschwindigkeit höchstens alle 5 s.
	tip := trayTooltip(st)
	if tip != c.tip && (strings.SplitN(tip, "·", 2)[0] != strings.SplitN(c.tip, "·", 2)[0] || time.Since(c.tipAt) >= 5*time.Second) {
		c.tip, c.tipAt = tip, time.Now()
		systray.SetTooltip(tip)
	}
}

// mbit: Bytes/s als "12,3 Mbit/s" (ab 100 ohne Nachkommastelle).
func mbit(bytesPerSec int64) string {
	v := float64(bytesPerSec) * 8 / 1e6
	digits := 1
	if v >= 100 {
		digits = 0
	}
	s := strconv.FormatFloat(v, 'f', digits, 64)
	if i18n.Lang() == "de" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s + " Mbit/s"
}

// trayStatus: oberste Zeile im Tray-Menü.
func trayStatus(st service.State) string {
	switch {
	case st.Busy:
		return i18n.T("Verbinde …")
	case st.On:
		return i18n.Tf("Aktiv · ↓ %s", mbit(st.RxRate))
	case st.Paused:
		return i18n.T("Pausiert · wartet auf zweite Leitung")
	}
	return i18n.T("Aus")
}

// trayLine: eine Leitung mit ihrer Geschwindigkeit.
func trayLine(l balancer.Stat, on bool) string {
	name := l.Name
	if len([]rune(name)) > 22 {
		name = string([]rune(name)[:21]) + "…"
	}
	switch l.State {
	case balancer.StateLimit:
		return "   " + name + " · " + i18n.T("Limit erreicht")
	case balancer.StateOff:
		return "   " + name + " · " + i18n.T("ausgeschaltet")
	case balancer.StateDown:
		return "   " + name + " · " + i18n.T("kein Internet")
	}
	if !on {
		return "   " + name
	}
	return "   " + name + " · " + mbit(l.RxRate+l.TxRate)
}

// trayTooltip: kurzer Zustand beim Darüberfahren mit der Maus.
func trayTooltip(st service.State) string {
	switch {
	case st.On:
		return i18n.Tf("WlanMerge – aktiv · ↓ %s", mbit(st.RxRate))
	case st.Paused:
		return i18n.T("WlanMerge – pausiert (nur eine Leitung)")
	}
	return i18n.T("WlanMerge – aus")
}

// ---------- Fenster ----------

var (
	openMu   sync.Mutex
	openLast time.Time
)

// openWindow startet das Fenster-Programm (läuft es schon, holt Windows es nach
// vorn). Ein Doppelklick aufs Symbol meldet Klick und Doppelklick – nur einmal starten.
func openWindow() {
	openMu.Lock()
	if time.Since(openLast) < 1500*time.Millisecond {
		openMu.Unlock()
		return
	}
	openLast = time.Now()
	openMu.Unlock()
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := exec.Command(exe).Start(); err != nil {
		log.Print("Fenster: ", err)
	}
}

// windowClosed: Beim ersten Schließen einmal erklären, wo WlanMerge weiterläuft.
func (c *core) windowClosed() {
	if c.s.Config().HideHintShown {
		return
	}
	c.s.MarkHideHintShown()
	notify(i18n.T("WlanMerge läuft im Hintergrund weiter"), i18n.T(trayHint), false)
}

// ---------- Benachrichtigungen ----------

var iconPath string

// setupNotifications meldet WlanMerge bei Windows für Benachrichtigungen an
// und legt das Logo ab, das in der Meldung angezeigt wird.
func setupNotifications() {
	p := filepath.Join(os.TempDir(), "wlanmerge-logo.png")
	if os.WriteFile(p, trayicon.Logo, 0o644) == nil {
		iconPath = p
	}
	if err := toast.SetAppData(toast.AppData{AppID: toastAppID, GUID: toastGUID, IconPath: iconPath}); err != nil {
		log.Print("Benachrichtigungen: ", err)
	}
}

// notify zeigt eine Windows-Benachrichtigung. Die Meldung selbst ist stumm;
// mit withSound spielt stattdessen unser eigener Klang. Ein Klick darauf
// öffnet das Fenster (Link "wlanmerge:open", angemeldet vom Installer).
func notify(title, body string, withSound bool) {
	if withSound {
		sound.Play()
	}
	n := toast.Notification{AppID: toastAppID, Title: title, Body: body, Icon: iconPath, Audio: toast.Silent,
		ActivationType: toast.Protocol, ActivationArguments: "wlanmerge:open"}
	if err := n.Push(); err != nil {
		log.Print("Benachrichtigung: ", err)
	}
}

// notifyLimits zeigt für jede neue Limit-Meldung eine Benachrichtigung.
func (c *core) notifyLimits(events []balancer.Event) {
	for _, e := range events {
		if e.Kind != balancer.EventLimit || !e.Time.After(c.lastNotified) {
			continue
		}
		c.lastNotified = e.Time
		notify(i18n.T("Datenlimit erreicht"), i18n.Tf("%s. Das WLAN läuft normal weiter.", e.Text), true)
	}
}
