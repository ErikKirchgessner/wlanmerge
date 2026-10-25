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
	"wlanmerge/internal/i18n"
	"wlanmerge/internal/nativehost"

	"wlanmerge/internal/balancer"
	"wlanmerge/internal/config"
	"wlanmerge/internal/downloader"
	"wlanmerge/internal/engine"
	"wlanmerge/internal/rules"
	"wlanmerge/internal/safety"
	"wlanmerge/internal/speedtest"
	"wlanmerge/internal/usage"
)

const (
	watchEvery     = 30 * time.Second
	watchFailsMax  = 3
	probeURL       = "http://1.1.1.1/cdn-cgi/trace"                   // winzige Antwort, enthält die öffentliche IP
	probeFallback  = "http://www.msftconnecttest.com/connecttest.txt" // Windows' eigener Internet-Test
	selfTestTimout = 8 * time.Second
)

// State ist alles, was die Oberfläche anzeigen muss.
type State struct {
	On          bool               `json:"on"`
	Busy        bool               `json:"busy"`
	Error       string             `json:"error"`
	PublicIP    string             `json:"publicIp"`
	AutoConnect bool               `json:"autoConnect"`
	Rebalance   bool               `json:"rebalance"`
	ResetDay    int                `json:"resetDay"`
	DLEnabled   bool               `json:"dlEnabled"`
	Downloads   []downloader.Info  `json:"downloads"`
	Theme       string             `json:"theme"`
	Lang        string             `json:"lang"` // tatsächliche Sprache ("de"/"en")
	Links       []balancer.Stat    `json:"links"`
	RxRate      int64              `json:"rxRate"`
	TxRate      int64              `json:"txRate"`
	Events      []balancer.Event   `json:"events"`
	SpeedTest   *speedtest.Result  `json:"speedTest"`
	SpeedSaved  *config.SavedSpeed `json:"speedSaved"`
}

type Service struct {
	mu     sync.Mutex
	cfg    *config.Config
	usage  *usage.Store
	dns    *rules.DNSCache
	bal    *balancer.Balancer
	dl     *downloader.Manager
	eng    *engine.Engine
	cancel context.CancelFunc

	on, busy  bool
	lastErr   string
	publicIP  string
	runCancel context.CancelFunc // Wächter, solange an
	speed     *speedtest.Result  // letzter bzw. laufender Speedtest
}

// New lädt Einstellungen und Verbrauch und beginnt, die Adapter zu beobachten.
func New() *Service {
	s := &Service{cfg: config.Load(), usage: usage.Load(), dns: rules.NewDNSCache()}
	i18n.Set(s.cfg.Lang)
	s.bal = balancer.New(s.cfg, s.usage, s.dns)
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
	return s
}

// Close schaltet aus und speichert alles (beim Beenden des Programms).
func (s *Service) Close() {
	s.dl.Close() // laufende Downloads anhalten und Stand speichern
	s.Stop()
	s.cancel()
	s.usage.Save()
}

func (s *Service) Config() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// HasLinks: gibt es überhaupt eine Internetverbindung?
func (s *Service) HasLinks() bool { return len(s.bal.Links()) > 0 }

// Start schaltet WlanMerge ein. Klappt der Selbsttest nicht, wird sofort
// wieder alles zurückgesetzt.
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
	}
	s.mu.Unlock()
	return err
}

func (s *Service) start() error {
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

// Stop schaltet aus und stellt den Originalzustand her.
func (s *Service) Stop() {
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
		On: s.on, Busy: s.busy, Error: s.lastErr, PublicIP: s.publicIP,
		AutoConnect: s.cfg.AutoConnect, Rebalance: s.cfg.Rebalance,
		ResetDay: s.cfg.ResetDay, Theme: s.cfg.Theme, Lang: i18n.Lang(),
		DLEnabled: s.cfg.DLEnabled,
		SpeedTest: s.speed, SpeedSaved: s.cfg.SpeedSaved,
	}
	s.mu.Unlock()
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
