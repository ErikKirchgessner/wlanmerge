// Package service ist die Steuerzentrale von WlanMerge. Fenster und Tray
// benutzen nur diese Schnittstelle.
package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"wlanmerge/internal/balancer"
	"wlanmerge/internal/config"
	"wlanmerge/internal/downloader"
	"wlanmerge/internal/engine"
	"wlanmerge/internal/i18n"
	"wlanmerge/internal/nativehost"
	"wlanmerge/internal/procinfo"
	"wlanmerge/internal/rules"
	"wlanmerge/internal/safety"
	"wlanmerge/internal/speedtest"
	"wlanmerge/internal/stats"
	"wlanmerge/internal/update"
	"wlanmerge/internal/usage"
	"wlanmerge/internal/version"
)

const (
	watchEvery     = 30 * time.Second
	watchFailsMax  = 3
	probeURL       = "http://1.1.1.1/cdn-cgi/trace"                   // winzige Antwort, enthält die öffentliche IP
	probeFallback  = "http://www.msftconnecttest.com/connecttest.txt" // Windows' eigener Internet-Test
	selfTestTimout = 8 * time.Second
)

// Automatische Pause (nur eine Leitung hat Internet → nichts zu bündeln).
// Ein- und Ausschalten trennt jeweils die gerade offenen Verbindungen, deshalb
// wird nur in ruhigen Momenten und nie in schneller Folge umgeschaltet.
const (
	pauseAfter    = 10 * time.Second // so lange muss es mit nur einer Leitung ruhig sein
	pauseIdleRate = 50_000           // Bytes/s – darunter gilt "ruhig" (kein Download läuft)
	resumeAfter   = 3 * time.Second  // so lange muss die zweite Leitung stabil da sein
	switchGap     = 30 * time.Second // Mindestabstand zwischen zwei automatischen Wechseln
	updateEvery   = 6 * time.Hour
)

// State ist alles, was die Oberfläche anzeigen muss.
type State struct {
	On          bool                 `json:"on"`     // es wird gebündelt
	Want        bool                 `json:"want"`   // eingeschaltet (kann trotzdem pausiert sein)
	Paused      bool                 `json:"paused"` // eingeschaltet, wartet aber auf eine zweite Leitung
	Busy        bool                 `json:"busy"`
	Error       string               `json:"error"`
	PublicIP    string               `json:"publicIp"`
	AutoConnect bool                 `json:"autoConnect"`
	AutoPause   bool                 `json:"autoPause"`
	Rebalance   bool                 `json:"rebalance"`
	ResetDay    int                  `json:"resetDay"`
	DLEnabled   bool                 `json:"dlEnabled"`
	Downloads   []downloader.Info    `json:"downloads"`
	Theme       string               `json:"theme"`
	Lang        string               `json:"lang"` // tatsächliche Sprache ("de"/"en")
	Links       []balancer.Stat      `json:"links"`
	RxRate      int64                `json:"rxRate"`
	TxRate      int64                `json:"txRate"`
	Events      []balancer.Event     `json:"events"`
	SpeedTest   *speedtest.Result    `json:"speedTest"`
	SpeedSaved  *config.SavedSpeed   `json:"speedSaved"`
	History     []balancer.HistPoint `json:"history"` // letzte 60 s, für die Kurve
	Stats       stats.Summary        `json:"stats"`
	AppFilter   bool                 `json:"appFilter"`
	Apps        []balancer.AppInfo   `json:"apps"`
	UpdateCheck bool                 `json:"updateCheck"`
	Update      *update.Info         `json:"update"` // neuere Version verfügbar (sonst nil)
	Version     string               `json:"version"`
	Onboarded   bool                 `json:"onboarded"`
}

type Service struct {
	mu     sync.Mutex
	cfg    *config.Config
	usage  *usage.Store
	daily  *stats.Store
	dns    *rules.DNSCache
	bal    *balancer.Balancer
	dl     *downloader.Manager
	eng    *engine.Engine
	cancel context.CancelFunc
	kick   chan struct{} // weckt den Autopiloten sofort

	want, paused bool      // Wunsch des Nutzers / wartet auf zweite Leitung
	on, busy     bool      // Tunnel läuft / wird gerade aufgebaut
	lastSwitch   time.Time // letzter automatischer Wechsel
	lastErr      string
	publicIP     string
	runCancel    context.CancelFunc // Wächter, solange an
	speed        *speedtest.Result  // letzter bzw. laufender Speedtest
	upd          *update.Info
}

// New lädt Einstellungen und Verbrauch und beginnt, die Adapter zu beobachten.
func New() *Service {
	s := &Service{cfg: config.Load(), usage: usage.Load(), daily: stats.Load(), dns: rules.NewDNSCache(), kick: make(chan struct{}, 1)}
	i18n.Set(s.cfg.Lang)
	s.bal = balancer.New(s.cfg, s.usage, s.dns)
	s.bal.Daily = s.daily
	s.bal.AppOf = procinfo.Lookup
	config.EnsureDir()
	s.dl = downloader.New(s.downloadLines, downloader.Options{
		DataDir:  config.Dir(),
		AutoTune: true, // Verbindungen je Leitung regelt WlanMerge selbst
		OnStream: func(in downloader.Info) {
			s.bal.Note(i18n.Tf("Browser-Download beschleunigt: %s", in.Name))
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.dl.StartServer(ctx) // lokale Weitergabe an den Browser
	go s.bal.Run(ctx)
	go s.autopilot(ctx)
	go s.watchUpdates(ctx)
	return s
}

// Close schaltet aus und speichert alles (beim Beenden des Programms).
func (s *Service) Close() {
	s.dl.Close() // laufende Downloads anhalten und Stand speichern
	s.Stop()
	s.cancel()
	s.usage.Save()
	s.daily.Save()
}

func (s *Service) Config() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// HasLinks: gibt es überhaupt eine Internetverbindung?
func (s *Service) HasLinks() bool { return len(s.bal.Links()) > 0 }

// SetOn ist der Ein/Aus-Knopf. Einschalten heißt: WlanMerge soll bündeln, sobald
// es etwas zu bündeln gibt – den Rest erledigt der Autopilot. Ausschalten
// stellt sofort den Originalzustand her.
func (s *Service) SetOn(on bool) error {
	if !on {
		s.Stop()
		return nil
	}
	s.mu.Lock()
	s.want, s.lastErr = true, ""
	s.mu.Unlock()
	s.wake()
	return nil
}

// wake lässt den Autopiloten sofort nachsehen (statt erst beim nächsten Takt).
func (s *Service) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// autopilot sorgt dafür, dass gebündelt wird, wenn es gewünscht und sinnvoll ist:
//   - eingeschaltet und genug Leitungen → Tunnel aufbauen
//   - nur eine Leitung hat Internet → in einem ruhigen Moment pausieren
//     (dann läuft alles direkt über Windows, ohne Umweg über WlanMerge)
//   - zweite Leitung wieder da → von selbst weitermachen
//
// Die Entscheidung selbst trifft pilot.step (ohne Seiteneffekte, damit testbar).
func (s *Service) autopilot(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var p pilot
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
		s.mu.Lock()
		in := pilotIn{want: s.want, on: s.on, busy: s.busy, paused: s.paused, auto: s.cfg.AutoPause, lastSwitch: s.lastSwitch}
		s.mu.Unlock()
		if in.want && !in.busy {
			in.guard = s.bal.Guarding()
			in.usable = s.bal.UsableLinks(false)
			in.verified = s.bal.UsableLinks(true)
			in.rate = s.bal.TotalRate()
		}
		switch p.step(in, time.Now()) {
		case actPause:
			s.pause()
		case actWait:
			s.setPaused(true) // wartet auf die zweite Leitung
		case actStart:
			if err := s.Start(); err != nil {
				// Genau ein Versuch: bei einem echten Fehler nicht ständig neu einschalten
				// (das würde jedes Mal kurz alle Verbindungen unterbrechen).
				s.mu.Lock()
				s.want = false
				s.mu.Unlock()
				s.setPaused(false)
			} else if in.paused {
				s.bal.Log(i18n.T("Zweite Leitung ist da – es wird wieder gebündelt"))
			}
		}
	}
}

// pilotIn ist alles, was der Autopilot für eine Entscheidung wissen muss.
type pilotIn struct {
	want, on, busy, paused bool
	auto                   bool  // automatische Pause eingeschaltet
	guard                  bool  // eine Leitung ist am Limit oder abgeschaltet → Tunnel schützt sie
	usable                 int   // nutzbare Leitungen
	verified               int   // davon in den letzten 20 s nachweislich online
	rate                   int64 // Bytes/s durch WlanMerge
	lastSwitch             time.Time
}

type pilotAct int

const (
	actNone  pilotAct = iota
	actPause          // Tunnel abbauen, eingeschaltet bleiben
	actWait           // nicht genug Leitungen: als "pausiert" anzeigen
	actStart          // Tunnel aufbauen
)

// pilot merkt sich, seit wann ein Zustand anhält.
type pilot struct{ fewSince, enoughSince time.Time }

func (p *pilot) step(in pilotIn, now time.Time) pilotAct {
	if !in.want || in.busy {
		p.fewSince, p.enoughSince = time.Time{}, time.Time{}
		return actNone
	}
	if in.on {
		p.enoughSince = time.Time{}
		if !in.auto || in.guard || in.usable >= 2 || in.rate >= pauseIdleRate {
			p.fewSince = time.Time{}
			return actNone
		}
		if p.fewSince.IsZero() {
			p.fewSince = now
		}
		if now.Sub(p.fewSince) >= pauseAfter && now.Sub(in.lastSwitch) >= switchGap {
			p.fewSince = time.Time{}
			return actPause
		}
		return actNone
	}
	// Gewünscht, aber der Tunnel läuft nicht.
	p.fewSince = time.Time{}
	need := 2
	if !in.auto || in.guard {
		need = 1
	}
	n := in.usable
	if in.paused {
		n = in.verified // nach einer Pause nur bei nachweislich funktionierenden Leitungen weitermachen
	}
	if n < need {
		p.enoughSince = time.Time{}
		if !in.paused {
			return actWait
		}
		return actNone
	}
	if p.enoughSince.IsZero() {
		p.enoughSince = now
	}
	if in.paused && (now.Sub(p.enoughSince) < resumeAfter || now.Sub(in.lastSwitch) < switchGap) {
		return actNone
	}
	p.enoughSince = time.Time{}
	return actStart
}
func (s *Service) setPaused(p bool) {
	s.mu.Lock()
	s.paused = p
	s.mu.Unlock()
	s.bal.SetProbe(p)
}

// pause baut den Tunnel ab, bleibt aber eingeschaltet (wartet auf eine zweite Leitung).
func (s *Service) pause() {
	s.stopEngine()
	s.mu.Lock()
	s.lastSwitch = time.Now()
	s.mu.Unlock()
	s.setPaused(true)
	s.bal.Log(i18n.T("Pausiert: Nur eine Leitung hat Internet – es gibt nichts zu bündeln"))
}

// Start baut den Tunnel auf. Klappt der Selbsttest nicht, wird sofort wieder
// alles zurückgesetzt.
func (s *Service) Start() error {
	s.mu.Lock()
	if s.on || s.busy {
		s.mu.Unlock()
		return nil
	}
	s.busy, s.lastErr = true, ""
	s.mu.Unlock()

	err := s.start()

	s.mu.Lock()
	s.busy = false
	if err != nil {
		s.lastErr = err.Error()
	} else {
		s.want, s.lastSwitch = true, time.Now()
	}
	s.mu.Unlock()
	if err == nil {
		s.setPaused(false)
	}
	return err
}

func (s *Service) start() error {
	if config.Sandbox() != "" {
		return errors.New(i18n.T("Testmodus: Das Netzwerk wird nicht verändert"))
	}
	if _, active := safety.Load(); active {
		safety.Restore(func(string) {}) // Reste eines Absturzes oder anderes WlanMerge
	}
	if len(s.bal.Links()) == 0 {
		return errors.New(i18n.T("Keine Internetverbindung gefunden"))
	}

	s.bal.SetActive(true)
	eng, err := engine.Start("WlanMerge", s.bal)
	if err != nil {
		s.bal.SetActive(false)
		return err
	}
	ip, err := probe(selfTestTimout)
	if err != nil {
		eng.Stop()
		s.bal.SetActive(false)
		s.bal.CloseAll()
		return i18n.Errorf("Selbsttest fehlgeschlagen, Netzwerk zurückgesetzt (%v)", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.eng, s.on, s.publicIP, s.runCancel = eng, true, ip, cancel
	s.mu.Unlock()

	go s.dns.Resolve(ctx, rules.Compile(config.Rules).ExactDomains())
	go s.watchdog(ctx)
	return nil
}

// Stop schaltet aus (auch den Wunsch "an") und stellt den Originalzustand her.
func (s *Service) Stop() {
	s.mu.Lock()
	s.want = false
	s.mu.Unlock()
	s.setPaused(false)
	s.stopEngine()
}

// stopEngine baut nur den Tunnel ab.
func (s *Service) stopEngine() {
	s.mu.Lock()
	eng, cancel := s.eng, s.runCancel
	s.eng, s.runCancel, s.on, s.publicIP = nil, nil, false, ""
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.bal.SetActive(false)
	if eng != nil {
		eng.Stop()
	}
	s.bal.CloseAll()
	s.usage.Save()
}

// Emergency ist der Notausschalter: ausschalten und alles zurücksetzen.
func (s *Service) Emergency() []string {
	s.Stop()
	var out []string
	safety.Restore(func(m string) { out = append(out, m) })
	return out
}

// watchdog prüft regelmäßig, ob das Internet durch WlanMerge geht. Wenn nicht,
// obwohl mindestens eine Leitung direkt funktioniert, schaltet er ab.
func (s *Service) watchdog(ctx context.Context) {
	fails := 0
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ip, err := probe(10 * time.Second)
		if err == nil {
			fails = 0
			s.mu.Lock()
			s.publicIP = ip
			s.mu.Unlock()
			continue
		}
		if !s.anyLinkOK() {
			fails = 0 // alle Leitungen tot – dann hilft Abschalten auch nicht
			continue
		}
		if fails++; fails >= watchFailsMax {
			s.Stop()
			s.mu.Lock()
			s.lastErr = i18n.T("Sicherheitsabschaltung: Internet ging nicht mehr durch WlanMerge. Normales Netzwerk ist wiederhergestellt.")
			s.mu.Unlock()
			return
		}
	}
}

func (s *Service) anyLinkOK() bool {
	for _, l := range s.bal.Stats() {
		if l.State == balancer.StateOK {
			return true
		}
	}
	return false
}

// probe ruft über den normalen Windows-Weg (also durch WlanMerge) eine winzige
// Seite ab und liefert die öffentliche IP. Ist Cloudflare gesperrt, reicht
// auch Microsofts Test-Seite (dann ohne IP-Anzeige).
func probe(timeout time.Duration) (string, error) {
	ip, err := probeOne(probeURL, timeout)
	if err == nil {
		return ip, nil
	}
	if _, err2 := probeOne(probeFallback, timeout); err2 == nil {
		return "", nil
	}
	return "", err
}

func probeOne(url string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil}}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if url == probeFallback {
		return "", nil
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if ip, ok := strings.CutPrefix(sc.Text(), "ip="); ok {
			return ip, nil
		}
	}
	return "", errors.New("unerwartete Antwort")
}

// Snapshot liefert den aktuellen Zustand für die Anzeige.
func (s *Service) Snapshot() State {
	s.mu.Lock()
	st := State{
		On: s.on, Want: s.want, Paused: s.paused && s.want && !s.on, Busy: s.busy,
		Error: s.lastErr, PublicIP: s.publicIP,
		AutoConnect: s.cfg.AutoConnect, AutoPause: s.cfg.AutoPause, Rebalance: s.cfg.Rebalance,
		ResetDay: s.cfg.ResetDay, Theme: s.cfg.Theme, Lang: i18n.Lang(),
		DLEnabled: s.cfg.DLEnabled,
		SpeedTest: s.speed, SpeedSaved: s.cfg.SpeedSaved,
		AppFilter: s.cfg.AppFilter, UpdateCheck: s.cfg.UpdateCheck, Update: s.upd,
		Version: version.Current, Onboarded: s.cfg.Onboarded,
	}
	since := s.cfg.LastReset(time.Now()) // Statistik-Zeitraum wie beim Datenlimit
	s.mu.Unlock()
	st.History = s.bal.History()
	st.Stats = s.daily.Summary(since)
	st.Apps = s.bal.Apps()
	st.Links = s.bal.Stats()
	for _, l := range st.Links {
		st.RxRate += l.RxRate
		st.TxRate += l.TxRate
	}
	st.Events = s.bal.Events()
	for _, d := range s.dl.List() { // nur laufende Browser-Downloads anzeigen
		if d.State == downloader.StateRunning {
			st.Downloads = append(st.Downloads, d)
		}
	}
	return st
}

// update ändert die Einstellungen (an einer Kopie), speichert und übernimmt sie.
func (s *Service) update(f func(c *config.Config)) error {
	s.mu.Lock()
	c := s.cfg.Clone()
	f(c)
	err := c.Save()
	s.cfg = c
	s.mu.Unlock()
	s.bal.SetConfig(c)
	return err
}

// SetLimit setzt das Datenlimit in Bytes (0 = kein Limit).
func (s *Service) SetLimit(name string, bytes int64) error {
	return s.update(func(c *config.Config) { c.Limits[name] = max(0, bytes) })
}

func (s *Service) ResetUsage(name string) error {
	s.usage.Reset(name)
	err := s.usage.Save()
	s.bal.SetConfig(s.Config()) // Zustand sofort neu berechnen (Limit evtl. wieder frei)
	return err
}

func (s *Service) SetDisabled(name string, off bool) error {
	return s.update(func(c *config.Config) {
		if off {
			c.Disabled[name] = true
		} else {
			delete(c.Disabled, name)
		}
	})
}

func (s *Service) SetAutoConnect(on bool) error {
	return s.update(func(c *config.Config) { c.AutoConnect = on })
}

func (s *Service) SetRebalance(on bool) error {
	return s.update(func(c *config.Config) { c.Rebalance = on })
}

func (s *Service) SetTheme(theme string) error {
	return s.update(func(c *config.Config) { c.Theme = theme })
}

// SetResetDay: an diesem Tag im Monat (1–28) beginnt der Verbrauch bei 0; 0 = nie.
func (s *Service) SetResetDay(day int) error {
	return s.update(func(c *config.Config) { c.ResetDay = max(0, min(28, day)) })
}

// SetAutoPause: bei nur einer Leitung von selbst pausieren.
func (s *Service) SetAutoPause(on bool) error {
	err := s.update(func(c *config.Config) { c.AutoPause = on })
	s.wake()
	return err
}

// SetAppFilter: nur ausgewählte Programme dürfen das Handy benutzen.
func (s *Service) SetAppFilter(on bool) error {
	return s.update(func(c *config.Config) { c.AppFilter = on })
}

// SetPhoneApp erlaubt oder verbietet einem Programm (z. B. "steam.exe") das Handy.
func (s *Service) SetPhoneApp(name string, allowed bool) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	names := []string{name}
	if g := config.KnownGroup(name); g != nil {
		names = g.Exes // bekanntes Programm: alle seine Dateien
	}
	return s.update(func(c *config.Config) {
		for _, n := range names {
			if allowed {
				c.PhoneApps[n] = true
			} else {
				delete(c.PhoneApps, n)
			}
		}
	})
}

func (s *Service) SetUpdateCheck(on bool) error {
	if !on {
		s.mu.Lock()
		s.upd = nil
		s.mu.Unlock()
	}
	return s.update(func(c *config.Config) { c.UpdateCheck = on })
}

// SetOnboarded: Die Einrichtung beim ersten Start ist abgeschlossen.
func (s *Service) SetOnboarded() error {
	return s.update(func(c *config.Config) { c.Onboarded = true })
}

// watchUpdates sieht kurz nach dem Start und dann alle paar Stunden nach, ob
// es eine neuere Version gibt. Es wird nichts heruntergeladen.
func (s *Service) watchUpdates(ctx context.Context) {
	var last time.Time
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !s.Config().UpdateCheck || time.Since(last) < updateEvery || !s.HasLinks() {
			continue
		}
		last = time.Now()
		in, err := update.Check(ctx)
		if err != nil {
			last = last.Add(time.Hour - updateEvery) // in einer Stunde nochmal
			continue
		}
		s.mu.Lock()
		s.upd = in
		s.mu.Unlock()
	}
}

// MarkHideHintShown merkt sich, dass der Hintergrund-Hinweis gezeigt wurde.
func (s *Service) MarkHideHintShown() error {
	return s.update(func(c *config.Config) { c.HideHintShown = true })
}

// ---------- Download-Manager ----------

func (s *Service) downloadLines() []downloader.Line {
	var out []downloader.Line
	for _, l := range s.bal.DownloadLines() {
		out = append(out, downloader.Line{Name: l.Name, Primary: l.Primary, Dial: l.Dial})
	}
	return out
}

// OfferDownload: Die Browser-Erweiterung bietet einen Download an. Bei Zusage
// liefert WlanMerge die lokale Adresse, die der Browser stattdessen lädt.
func (s *Service) OfferDownload(r downloader.Request) (url, token string, ok bool, reason string) {
	if !s.Config().DLEnabled {
		return "", "", false, "Browser-Downloads sind ausgeschaltet"
	}
	return s.dl.OfferStream(r)
}

// DropDownload: Der Browser war schneller fertig – Angebot verwerfen.
func (s *Service) DropDownload(token string) { s.dl.Drop(token) }

func (s *Service) SetDownloadsEnabled(on bool) error {
	return s.update(func(c *config.Config) { c.DLEnabled = on })
}

// StartSpeedTest misst im Hintergrund alle Leitungen gleichzeitig (ca. 10 s).
func (s *Service) StartSpeedTest() error {
	s.mu.Lock()
	if s.speed != nil && s.speed.Running {
		s.mu.Unlock()
		return nil
	}
	s.speed = &speedtest.Result{Running: true}
	s.mu.Unlock()

	var lines []speedtest.Line
	for _, l := range s.bal.DownloadLines() {
		lines = append(lines, speedtest.Line{Name: l.Name, Dial: l.Dial})
	}
	set := func(r speedtest.Result) {
		s.mu.Lock()
		s.speed = &r
		s.mu.Unlock()
	}
	go func() {
		// Keine Zwischenstände: das Fenster zeigt während des Tests nur den
		// animierten Balken, das Ergebnis kommt einmal am Ende.
		set(speedtest.Run(context.Background(), lines, nil))
	}()
	return nil
}

// SaveSpeedTest merkt sich das letzte fertige Ergebnis zum Vergleichen.
func (s *Service) SaveSpeedTest() error {
	s.mu.Lock()
	r := s.speed
	s.mu.Unlock()
	if r == nil || r.Running || r.Error != "" {
		return errors.New(i18n.T("Kein fertiges Ergebnis zum Speichern"))
	}
	sv := &config.SavedSpeed{Total: r.Total, Lines: map[string]int64{}, At: r.At}
	for _, l := range r.Lines {
		sv.Lines[l.Name] = l.Rate
	}
	return s.update(func(c *config.Config) { c.SpeedSaved = sv })
}

// SetLang stellt die Sprache um ("de", "en"; "" = wie das Betriebssystem).
func (s *Service) SetLang(l string) error {
	if l != "de" && l != "en" {
		l = ""
	}
	i18n.Set(l)
	return s.update(func(c *config.Config) { c.Lang = l })
}

// HandleOffer: Die Browser-Erweiterung meldet einen neuen Download (oder
// verwirft ein Angebot, weil der Browser schon fertig war).
func (s *Service) HandleOffer(o nativehost.Offer) nativehost.Reply {
	if o.Type == "drop" {
		s.DropDownload(o.Token)
		return nativehost.Reply{}
	}
	url, token, ok, reason := s.OfferDownload(downloader.Request{
		URL: o.URL, Referrer: o.Referrer, UserAgent: o.UserAgent, Cookies: o.Cookies, Filename: o.Filename,
	})
	return nativehost.Reply{Accept: ok, URL: url, Token: token, Reason: reason}
}
