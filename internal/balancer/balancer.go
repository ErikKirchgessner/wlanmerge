// Package balancer verteilt neue Verbindungen auf alle echten Adapter.
//
//   - Misst laufend, wie viel jede Leitung schafft, und verteilt im passenden
//     Verhältnis (doppelt so schnell → ca. doppelt so viel).
//   - Datenlimit: Leitung wird beim Erreichen abgeschaltet.
//   - Gesundheitsprüfung: hängt oder fällt eine Leitung aus, werden ihre
//     Verbindungen geschlossen und Programme verbinden sich über die anderen neu.
//   - Login-Server von Steam, Battle.net und Epic bleiben auf der Hauptleitung.
//   - UDP (Spiele, Sprachchat) bleibt auf der Hauptleitung.
//   - Nur IPv4: IPv6-Verbindungen werden sofort abgelehnt, Programme
//     weichen dann automatisch auf IPv4 aus.
//
// Grenze: Eine laufende TCP-Verbindung kann ohne eigenen Server nicht auf eine
// andere Leitung umziehen (der Server sähe eine andere IP). Neue Verbindungen
// werden deshalb gezielt verteilt, und rebalance.go schichtet Downloads mit
// vielen Verbindungen bei Bedarf um.
package balancer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"wlanmerge/internal/i18n"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	"wlanmerge/internal/config"
	"wlanmerge/internal/netif"
	"wlanmerge/internal/rules"
	"wlanmerge/internal/stats"
	"wlanmerge/internal/usage"
)

const (
	refreshEvery   = 3 * time.Second
	healthEvery    = 4 * time.Second
	healthTimeout  = 2500 * time.Millisecond
	healthFailsMax = 2 // so viele Prüfungen hintereinander fehlgeschlagen → gestört
	// Leitung ohne eigene Verbindungen: seltener prüfen (spart Handy-Daten)
	idleHealthEvery = 30 * time.Second
	firstDialLimit  = 2500 * time.Millisecond
	dialFailsMax    = 3
	dialDownFor     = 15 * time.Second
	capacityDecay   = 0.995 // gemessene Spitze verblasst langsam (Halbwertszeit ~2 Min.)
	ewmaAlpha       = 0.2   // Glättung der "Ist"-Anteile
	minShare        = 0.10  // jede Leitung bekommt mind. 10 %, damit sie weiter gemessen wird
	minBusyRate     = 60_000
	defaultCap      = 125_000 // 1 Mbit/s, solange noch nichts gemessen wurde

	// Surfen zuerst über die Leitung mit dem besten Ping (siehe candidates):
	interactiveConns = 2                // so viele Verbindungen zum selben Ziel gelten noch als "Surfen"
	hotBytes         = 4 << 20          // ab 4 MB über eine Verbindung ist das Ziel ein Download-Server
	hotFor           = 2 * time.Minute  // so lange werden neue Verbindungen dorthin sofort verteilt
	bulkRate         = 250_000          // Bytes/s (2 Mbit/s): darunter ist die Surf-Leitung nicht ausgelastet
	verifiedFor      = 20 * time.Second // so frisch muss eine Prüfung fürs automatische Fortsetzen sein
	probeEvery       = 8 * time.Second  // pausiert: so oft jede Leitung prüfen
	histLen          = 60               // Sekunden Verlauf für die Kurve
)

// Zustände einer Leitung.
const (
	StateOK    = "ok"
	StateDown  = "gestört"
	StateLimit = "limit"
	StateOff   = "aus"
)

// Arten von Meldungen (die Oberfläche benachrichtigt bei "limit").
const (
	EventInfo  = ""
	EventLimit = "limit"
	EventQuiet = "quiet" // nur fürs Protokoll (Diagnose), die Oberfläche blendet es nicht ein
)

var (
	healthTargets = []string{"1.1.1.1:443", "8.8.8.8:443", "9.9.9.9:443"}
	errNoIPv6     = errors.New("IPv6 wird nicht gebündelt")
	loginRules    = rules.Compile(config.Rules)
)

// Link ist eine echte Leitung mit ihren Live-Zählern.
type Link struct {
	adapter atomic.Pointer[netif.Adapter]

	activeTCP atomic.Int64
	activeUDP atomic.Int64
	rx, tx    atomic.Int64 // Bytes seit Programmstart
	dialFails atomic.Int32
	downUntil atomic.Int64 // UnixNano – nach Verbindungsfehlern kurz meiden
	healthy   atomic.Bool
	rttMs     atomic.Int64
	lastOK    atomic.Int64 // UnixNano: zuletzt nachweislich Internet (Prüfung oder Datenfluss)

	conns sync.Map // io.Closer → struct{}: offene Verbindungen, um sie schließen zu können

	// Nur unter mu:
	mu             sync.Mutex
	lastRx, lastTx int64
	rateRx, rateTx int64
	capacity       float64 // geschätzte Leistung (Bytes/s)
	ewma           float64 // geglättete aktuelle Rate (Bytes/s)
	target         float64 // Soll-Anteil 0..1
	share          float64 // Ist-Anteil 0..1
	state          string
	hcFails        int
	hcRx           int64     // empfangene Bytes bei der letzten Prüfung
	hcLast         time.Time // letzte echte Test-Verbindung
}

// Adapter liefert die aktuellen Daten des Adapters (IP usw.).
func (l *Link) Adapter() netif.Adapter { return *l.adapter.Load() }
func (l *Link) name() string           { return l.adapter.Load().Name }

func (l *Link) getState() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

func (l *Link) dialBlocked() bool { return time.Now().UnixNano() < l.downUntil.Load() }

func (l *Link) noteDial(err error) {
	if err == nil {
		l.dialFails.Store(0)
		return
	}
	if l.dialFails.Add(1) >= dialFailsMax {
		l.downUntil.Store(time.Now().Add(dialDownFor).UnixNano())
		l.dialFails.Store(0)
	}
}

func (l *Link) closeAll() int {
	n := 0
	l.conns.Range(func(k, _ any) bool {
		k.(io.Closer).Close()
		n++
		return true
	})
	return n
}

// Stat ist eine Momentaufnahme einer Leitung für die Anzeige.
type Stat struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	State    string  `json:"state"`
	Primary  bool    `json:"primary"`
	Target   float64 `json:"target"` // Soll-Anteil in %
	Share    float64 `json:"share"`  // Ist-Anteil in %
	RxRate   int64   `json:"rxRate"` // Bytes/s
	TxRate   int64   `json:"txRate"`
	Capacity int64   `json:"capacity"`
	RTTms    int64   `json:"rttMs"`
	Conns    int64   `json:"conns"`
	Used     int64   `json:"used"`  // Verbrauch seit letztem Zurücksetzen (Bytes)
	Limit    int64   `json:"limit"` // 0 = kein Limit
	Disabled bool    `json:"disabled"`
}

// Event ist eine Meldung für die Anzeige ("Handy: Limit erreicht").
type Event struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
	Kind string    `json:"kind"`
}

type Balancer struct {
	cfg    atomic.Pointer[config.Config]
	usage  *usage.Store
	dns    *rules.DNSCache
	active atomic.Bool // nur wenn WlanMerge an ist: Gesundheitsprüfung + Zählen

	mu    sync.RWMutex
	links []*Link

	evMu   sync.Mutex
	events []Event

	reb rebalancer

	probe atomic.Bool // pausiert: Leitungen weiter prüfen, um von selbst fortzusetzen

	// Daily (optional): Tagesstatistik. AppOf (optional): welches Programm hat
	// die Verbindung aufgebaut? Beides setzt die Steuerzentrale.
	Daily *stats.Store
	AppOf func(udp bool, srcPort uint16, dst netip.AddrPort) string

	dstMu sync.Mutex
	dstN  map[netip.Addr]int       // offene TCP-Verbindungen je Ziel
	hot   map[netip.Addr]time.Time // Download-Server: bis wann sofort verteilen

	appMu sync.Mutex
	apps  map[string]*appStat

	histMu   sync.Mutex
	hist     [histLen]HistPoint
	histNext int
}

// HistPoint ist eine Sekunde im Verlauf (Download-Raten in Bytes/s).
type HistPoint struct {
	Total int64 `json:"total"` // alle Leitungen zusammen
	Main  int64 `json:"main"`  // davon die Hauptleitung
}

func New(cfg *config.Config, u *usage.Store, dns *rules.DNSCache) *Balancer {
	b := &Balancer{usage: u, dns: dns}
	b.SetConfig(cfg)
	return b
}

// SetConfig übernimmt neue Einstellungen sofort.
func (b *Balancer) SetConfig(cfg *config.Config) {
	b.cfg.Store(cfg)
	b.monthlyReset()
	b.refresh()
	b.recompute()
}

// SetActive: an = Gesundheitsprüfung läuft, aus = alle Verbindungen zählen nicht mehr.
func (b *Balancer) SetActive(on bool) {
	b.active.Store(on)
	if on {
		for _, l := range b.Links() {
			l.healthy.Store(true)
			l.mu.Lock()
			l.hcFails = 0
			l.mu.Unlock()
		}
	}
}

func (b *Balancer) Events() []Event {
	b.evMu.Lock()
	defer b.evMu.Unlock()
	return append([]Event(nil), b.events...)
}

func (b *Balancer) event(kind, format string, args ...any) {
	b.evMu.Lock()
	b.events = append(b.events, Event{Time: time.Now(), Text: i18n.Tf(format, args...), Kind: kind})
	if len(b.events) > 30 {
		b.events = b.events[len(b.events)-30:]
	}
	b.evMu.Unlock()
}

// Note fügt eine Info-Meldung für die Anzeige hinzu.
func (b *Balancer) Note(text string) { b.event(EventInfo, "%s", text) }

// Log merkt sich eine Meldung nur fürs Protokoll (ohne Einblendung).
func (b *Balancer) Log(text string) { b.event(EventQuiet, "%s", text) }

// Run hält alles aktuell, bis ctx endet.
func (b *Balancer) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	refresh := time.NewTicker(refreshEvery)
	defer refresh.Stop()
	health := time.NewTicker(healthEvery)
	defer health.Stop()
	slow := time.NewTicker(10 * time.Second)
	defer slow.Stop()
	reb := time.NewTicker(rebalanceEvery)
	defer reb.Stop()
	defer b.flushUsage()
	defer b.usage.Save()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			b.measure()
			b.flushUsage()
			b.recompute()
		case <-refresh.C:
			b.refresh()
		case <-health.C:
			if b.active.Load() || b.probe.Load() {
				b.checkHealth(ctx)
			}
		case <-reb.C:
			if b.active.Load() {
				b.rebalance()
			}
		case <-slow.C:
			b.monthlyReset()
			b.usage.Save()
			if b.Daily != nil {
				b.Daily.Save()
			}
			b.forgetHot()
		}
	}
}

// monthlyReset setzt am eingestellten Tag im Monat den Verbrauch zurück.
func (b *Balancer) monthlyReset() {
	cfg := b.cfg.Load()
	if names := b.usage.ResetBefore(cfg.LastReset(time.Now())); len(names) > 0 {
		b.usage.Save()
		b.event(EventInfo, "Neuer Monat: Datenverbrauch zurückgesetzt (%s)", strings.Join(names, ", "))
	}
}

func (b *Balancer) Links() []*Link {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]*Link(nil), b.links...)
}

// refresh gleicht unsere Leitungen mit den aktuell verbundenen Adaptern ab.
func (b *Balancer) refresh() {
	adapters, err := netif.Usable()
	if err != nil {
		return
	}
	b.mu.Lock()
	old := map[string]*Link{}
	for _, l := range b.links {
		old[l.name()] = l
	}
	var links []*Link
	for _, a := range adapters {
		l := old[a.Name]
		if l == nil {
			l = &Link{}
			l.healthy.Store(true)
			l.state = StateOK
			if b.active.Load() {
				defer b.event(EventInfo, "%s verbunden", a.Name)
			}
		}
		delete(old, a.Name)
		l.adapter.Store(&a)
		links = append(links, l)
	}
	// Feste Reihenfolge: kein Handy zuerst (Datenvolumen), dann nach Name.
	// Die erste funktionierende Leitung ist die Hauptleitung.
	sort.SliceStable(links, func(i, j int) bool {
		pi, pj := links[i].Adapter().Mobile(), links[j].Adapter().Mobile()
		if pi != pj {
			return !pi
		}
		return links[i].name() < links[j].name()
	})
	b.links = links
	b.mu.Unlock()

	for name, l := range old { // Adapter verschwunden (z. B. USB abgezogen)
		if n := l.closeAll(); n > 0 || b.active.Load() {
			b.event(EventInfo, "%s getrennt – Verbindungen werden über die anderen Leitungen neu aufgebaut", name)
		}
	}
}

// measure berechnet Raten, geschätzte Leistung und Ist-Anteile.
func (b *Balancer) measure() {
	links := b.Links()
	var sum float64
	for _, l := range links {
		l.mu.Lock()
		rx, tx := l.rx.Load(), l.tx.Load()
		l.rateRx, l.rateTx = rx-l.lastRx, tx-l.lastTx
		l.lastRx, l.lastTx = rx, tx
		r := float64(l.rateRx + l.rateTx)
		l.capacity = max(l.capacity*capacityDecay, r)
		l.ewma = l.ewma*(1-ewmaAlpha) + r*ewmaAlpha
		sum += l.ewma
		l.mu.Unlock()
	}
	// Ist-Anteile, Verlauf (für die Kurve) und Tagesstatistik
	prim := b.primary()
	var p HistPoint
	var perLine map[string]int64
	var primName string
	var primRx, primCap float64
	for _, l := range links {
		l.mu.Lock()
		if sum > 0 {
			l.share = l.ewma / sum
		} else {
			l.share = 0
		}
		p.Total += l.rateRx
		if n := l.rateRx + l.rateTx; n > 0 {
			if perLine == nil {
				perLine = make(map[string]int64, len(links))
			}
			perLine[l.name()] += n
		}
		if l == prim {
			p.Main = l.rateRx
			primName = l.name()
			primRx, primCap = float64(l.rateRx), l.capacity
		}
		l.mu.Unlock()
	}
	b.histMu.Lock()
	b.hist[b.histNext%histLen] = p
	b.histNext++
	b.histMu.Unlock()
	if b.Daily != nil {
		b.Daily.Add(perLine, primName, savedSeconds(primRx, primCap, float64(p.Total-p.Main)))
	}
}

// savedSeconds schätzt, wie viel Download-Zeit das Bündeln in dieser Sekunde
// gespart hat: Ist die Hauptleitung ausgelastet und liefern die anderen
// zusätzlich r, hätte die Hauptleitung allein für dieselbe Menge r/Hauptrate
// Sekunden länger gebraucht. Ist sie nicht ausgelastet, wurde nichts gespart.
func savedSeconds(mainRate, mainCap, extraRate float64) float64 {
	if extraRate <= 0 || mainRate < bulkRate || mainRate < 0.6*mainCap {
		return 0
	}
	return min(4, extraRate/mainRate)
}

// History liefert den Verlauf der letzten Minute, ältester Wert zuerst.
func (b *Balancer) History() []HistPoint {
	b.histMu.Lock()
	defer b.histMu.Unlock()
	out := make([]HistPoint, 0, histLen)
	for i := 0; i < histLen; i++ {
		out = append(out, b.hist[(b.histNext+i)%histLen])
	}
	return out
}

// TotalRate: Bytes/s, die gerade durch WlanMerge laufen (beide Richtungen).
func (b *Balancer) TotalRate() int64 {
	var sum int64
	for _, l := range b.Links() {
		l.mu.Lock()
		sum += l.rateRx + l.rateTx
		l.mu.Unlock()
	}
	return sum
}

// SetProbe: Im pausierten Zustand weiter prüfen, welche Leitungen Internet haben.
func (b *Balancer) SetProbe(on bool) {
	if on && !b.probe.Load() {
		for _, l := range b.Links() {
			l.mu.Lock()
			l.hcLast, l.hcFails = time.Time{}, 0
			l.mu.Unlock()
			l.lastOK.Store(0)
			l.healthy.Store(true)
		}
	}
	b.probe.Store(on)
}

// UsableLinks zählt die Leitungen, über die gebündelt werden kann. verified:
// nur solche, deren Internet in den letzten 20 s nachgewiesen wurde (damit
// WlanMerge nach einer Pause nicht wegen einer toten Leitung fortsetzt).
func (b *Balancer) UsableLinks(verified bool) int {
	n := 0
	for _, l := range b.Links() {
		if l.getState() != StateOK {
			continue
		}
		if verified && time.Since(time.Unix(0, l.lastOK.Load())) > verifiedFor {
			continue
		}
		n++
	}
	return n
}

// Guarding: Gibt es eine Leitung, die absichtlich nicht benutzt wird (Limit
// erreicht oder abgeschaltet)? Dann schützt WlanMerge sie – ohne WlanMerge
// würde Windows sie womöglich als Standardweg nehmen und das Limit sprengen.
// In diesem Fall darf nicht pausiert werden.
func (b *Balancer) Guarding() bool {
	for _, l := range b.Links() {
		if st := l.getState(); st == StateLimit || st == StateOff {
			return true
		}
	}
	return false
}

// flushUsage übernimmt den Verbrauch aus den Zählern des Betriebssystems:
// aller Verkehr über den Adapter, auch wenn WlanMerge gerade aus ist.
func (b *Balancer) flushUsage() {
	for _, l := range b.Links() {
		if n, ok := netif.Counters(l.Adapter()); ok {
			b.usage.Observe(l.name(), n)
		}
	}
}

// recompute bestimmt Zustand und Soll-Anteil jeder Leitung.
func (b *Balancer) recompute() {
	cfg := b.cfg.Load()
	links := b.Links()

	var okLinks []*Link
	for _, l := range links {
		a := l.Adapter()
		st := StateOK
		limit := cfg.LimitFor(a.Name, a.Mobile())
		switch {
		case cfg.IsDisabled(a.Name):
			st = StateOff
		case limit > 0 && b.usage.Get(a.Name).Bytes >= limit:
			st = StateLimit
		case !l.healthy.Load():
			st = StateDown
		}
		l.mu.Lock()
		prev := l.state
		l.state = st
		l.mu.Unlock()
		if st == StateOK {
			okLinks = append(okLinks, l)
		}
		if prev != st && prev != "" {
			b.onStateChange(l, prev, st, limit)
		}
	}

	// Soll-Anteile nach gemessener Leistung
	weights := map[*Link]float64{}
	var sum float64
	for _, l := range okLinks {
		l.mu.Lock()
		weights[l] = max(l.capacity, defaultCap)
		l.mu.Unlock()
		sum += weights[l]
	}
	if len(okLinks) > 1 && float64(len(okLinks))*minShare < 1 {
		// Mindestanteil: zu kleine Leitungen bekommen genau minShare,
		// die übrigen teilen sich den Rest im alten Verhältnis.
		var floored int
		var restSum float64
		for _, l := range okLinks {
			weights[l] /= sum
			if weights[l] < minShare {
				floored++
			} else {
				restSum += weights[l]
			}
		}
		rest := 1 - float64(floored)*minShare
		for _, l := range okLinks {
			if weights[l] < minShare {
				weights[l] = minShare
			} else {
				weights[l] = weights[l] / restSum * rest
			}
		}
		sum = 1
	}
	for _, l := range links {
		l.mu.Lock()
		if w, ok := weights[l]; ok && sum > 0 {
			l.target = w / sum
		} else {
			l.target = 0
		}
		l.mu.Unlock()
	}
}

func (b *Balancer) onStateChange(l *Link, from, to string, limit int64) {
	name := l.name()
	switch to {
	case StateLimit:
		n := l.closeAll()
		b.event(EventLimit, "%s: Datenlimit %s erreicht – Leitung abgeschaltet (%d Verbindungen umgeleitet)", name, HumanBytes(limit), n)
	case StateOff:
		l.closeAll()
		b.event(EventInfo, "%s ausgeschaltet", name)
	case StateDown:
		if !b.active.Load() {
			return // pausiert: nur mitzählen, nichts melden
		}
		// Nur schließen, wenn es eine andere funktionierende Leitung gibt –
		// sonst würden wir bei einem Fehlalarm alles abbrechen.
		if b.anotherOK(l) {
			n := l.closeAll()
			b.event(EventInfo, "%s gestört – %d Verbindungen auf andere Leitungen umgeleitet", name, n)
		} else {
			b.event(EventInfo, "%s gestört – keine andere Leitung verfügbar", name)
		}
	case StateOK:
		if !b.active.Load() {
			return
		}
		if from == StateDown {
			b.event(EventInfo, "%s funktioniert wieder", name)
		} else {
			b.event(EventInfo, "%s wieder aktiv", name)
		}
	}
}

func (b *Balancer) anotherOK(skip *Link) bool {
	for _, l := range b.Links() {
		if l != skip && l.getState() == StateOK {
			return true
		}
	}
	return false
}

// checkHealth baut über jede Leitung eine kurze Test-Verbindung auf
// (nur Verbindungsaufbau, kaum Daten) und misst dabei den Ping.
//
// Sparsam: Kommen über eine Leitung gerade Daten an, funktioniert sie
// offensichtlich – dann keine Test-Verbindung. Hat sie gar keine Verbindungen
// zu tragen, reicht eine Prüfung alle 30 s (das spart am Handy etwa 8 MB am Tag).
func (b *Balancer) checkHealth(ctx context.Context) {
	var wg sync.WaitGroup
	now := time.Now()
	for _, l := range b.Links() {
		if l.getState() == StateOff || l.getState() == StateLimit {
			continue
		}
		rx := l.rx.Load()
		l.mu.Lock()
		flowing := rx > l.hcRx
		l.hcRx = rx
		idle := l.activeTCP.Load() == 0 && now.Sub(l.hcLast) < idleHealthEvery
		if b.probe.Load() && !b.active.Load() {
			// pausiert: seltener prüfen (spart Datenvolumen am Handy), aber oft genug,
			// damit das Fortsetzen einen frischen Nachweis hat (verifiedFor)
			idle = now.Sub(l.hcLast) < probeEvery
		}
		if flowing {
			l.hcFails = 0
			l.lastOK.Store(now.UnixNano())
		}
		skip := flowing || idle
		if !skip {
			l.hcLast = now
		}
		l.mu.Unlock()
		if flowing {
			l.healthy.Store(true)
		}
		if skip {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok := false
			for _, t := range healthTargets {
				c, cancel := context.WithTimeout(ctx, healthTimeout)
				start := time.Now()
				conn, err := l.Adapter().DialContext(c, "tcp", t)
				cancel()
				if err == nil {
					l.rttMs.Store(time.Since(start).Milliseconds())
					l.lastOK.Store(time.Now().UnixNano())
					conn.Close()
					ok = true
					break
				}
			}
			l.mu.Lock()
			if ok {
				l.hcFails = 0
			} else {
				l.hcFails++
			}
			bad := l.hcFails >= healthFailsMax
			l.mu.Unlock()
			l.healthy.Store(!bad)
		}()
	}
	wg.Wait()
	b.recompute()
}

// primary ist die Hauptleitung: die erste funktionierende (WLAN/LAN vor Handy).
func (b *Balancer) primary() *Link {
	for _, l := range b.Links() {
		if l.getState() == StateOK {
			return l
		}
	}
	return nil
}

// candidates liefert die Leitungen in der Reihenfolge, in der wir sie probieren.
// pinned = Login-Server oder Spiele-UDP: bleibt auf der Hauptleitung und wird
// nie umverteilt.
//
// app ist das Programm, das die Verbindung aufbaut ("" = unbekannt). Ist der
// Programm-Filter an und das Programm nicht freigegeben, bleiben Handy-Leitungen
// außen vor – außer es gibt gar keine andere (nie ohne Internet).
func (b *Balancer) candidates(m *M.Metadata, udp bool, app string) (cands []*Link, pinned bool) {
	var ok, down []*Link
	for _, l := range b.Links() {
		switch l.getState() {
		case StateOK:
			if l.dialBlocked() {
				down = append(down, l)
			} else {
				ok = append(ok, l)
			}
		case StateDown:
			down = append(down, l) // nur als letzte Notlösung
		}
	}
	if !b.cfg.Load().MayUsePhone(app) {
		if fok, fdown := noMobile(ok), noMobile(down); len(fok)+len(fdown) > 0 {
			ok, down = fok, fdown
		}
	}

	// Login-Server, DNS und Spiele (UDP außer Port 443 = QUIC) → Hauptleitung.
	_, login := loginRules.Match(m.DstIP, b.dns.Names(m.DstIP))
	if login || (udp && m.DstPort != 443) {
		if p := b.primary(); p != nil {
			rest := []*Link{p}
			for _, l := range append(ok, down...) {
				if l != p {
					rest = append(rest, l)
				}
			}
			return rest, true
		}
	}

	// Sonst: die Leitung mit dem größten "Nachholbedarf" zuerst.
	type scored struct {
		l *Link
		s float64
	}
	var sc []scored
	var totalRate float64
	for _, l := range ok {
		l.mu.Lock()
		totalRate += l.ewma
		l.mu.Unlock()
	}
	for _, l := range ok {
		l.mu.Lock()
		t, share := l.target, l.share
		l.mu.Unlock()
		if t <= 0 {
			t = 0.001
		}
		if totalRate < minBusyRate {
			share = t // kaum Verkehr: Ist-Anteil nicht aussagekräftig
		}
		// offene Verbindungen im Verhältnis zum Soll, verstärkt, wenn die Leitung
		// schon mehr (oder weniger) Daten trägt als sie soll.
		s := float64(l.activeTCP.Load()+1) / t * (1 + max(-0.5, min(0.5, share-t)))
		sc = append(sc, scored{l, s})
	}
	sort.SliceStable(sc, func(i, j int) bool { return sc[i].s < sc[j].s })
	front := func(p *Link) {
		for i, x := range sc {
			if x.l == p {
				copy(sc[1:i+1], sc[:i]) // p nach vorn, Reihenfolge der anderen bleibt
				sc[0] = x
				return
			}
		}
	}
	if p := b.reb.preferred(); p != nil {
		// Direkt nach einer Umverteilung: die neue Verbindung auf die freie Leitung.
		front(p)
	} else if il := interactiveLine(ok); il != nil && len(ok) > 1 && !b.bulk(m.DstIP.Unmap(), il) {
		// Surfen: einzelne Verbindungen zuerst über die Leitung mit dem besten Ping
		// (und nicht übers Handy). Erst Downloads – mehrere Verbindungen zum selben
		// Server oder eine ausgelastete Leitung – werden auf alle verteilt.
		front(il)
	}
	out := make([]*Link, 0, len(sc)+len(down))
	for _, x := range sc {
		out = append(out, x.l)
	}
	return append(out, down...), false
}

func noMobile(links []*Link) []*Link {
	var out []*Link
	for _, l := range links {
		if !l.Adapter().Mobile() {
			out = append(out, l)
		}
	}
	return out
}

// interactiveLine ist die Leitung fürs Surfen: unter den festen Leitungen
// (WLAN/LAN) die mit dem besten Ping. Gibt es nur Handy-Leitungen, die erste.
func interactiveLine(ok []*Link) *Link {
	var best *Link
	for _, l := range ok {
		if l.Adapter().Mobile() {
			continue
		}
		if best == nil {
			best = l
		} else if a, c := l.rttMs.Load(), best.rttMs.Load(); a > 0 && (c == 0 || a < c) {
			best = l
		}
	}
	if best == nil && len(ok) > 0 {
		best = ok[0]
	}
	return best
}

// bulk: Sieht diese neue Verbindung nach Download aus? Ja, wenn zum selben Ziel
// schon mehrere Verbindungen offen sind (Steam & Co., Speedtests), das Ziel
// gerade viel geliefert hat oder die Surf-Leitung ohnehin ausgelastet ist.
func (b *Balancer) bulk(dst netip.Addr, il *Link) bool {
	b.dstMu.Lock()
	n := b.dstN[dst]
	until, isHot := b.hot[dst]
	b.dstMu.Unlock()
	if n >= interactiveConns || (isHot && time.Now().Before(until)) {
		return true
	}
	il.mu.Lock()
	defer il.mu.Unlock()
	return il.ewma >= bulkRate && il.ewma >= 0.6*il.capacity
}

func (b *Balancer) dstAdd(dst netip.Addr, d int) {
	b.dstMu.Lock()
	if b.dstN == nil {
		b.dstN = map[netip.Addr]int{}
	}
	if n := b.dstN[dst] + d; n > 0 {
		b.dstN[dst] = n
	} else {
		delete(b.dstN, dst)
	}
	b.dstMu.Unlock()
}

func (b *Balancer) markHot(dst netip.Addr) {
	b.dstMu.Lock()
	if b.hot == nil {
		b.hot = map[netip.Addr]time.Time{}
	}
	b.hot[dst] = time.Now().Add(hotFor)
	b.dstMu.Unlock()
}

func (b *Balancer) forgetHot() {
	now := time.Now()
	b.dstMu.Lock()
	for k, until := range b.hot {
		if now.After(until) {
			delete(b.hot, k)
		}
	}
	b.dstMu.Unlock()
}

// ---------- Programme ----------

// appStat zählt, was ein Programm über WlanMerge übertragen hat.
type appStat struct {
	bytes, phone atomic.Int64 // gesamt / davon über Handy-Leitungen
	last         atomic.Int64 // UnixNano der letzten Verbindung
}

// AppInfo ist ein Programm für die Liste in den Einstellungen.
type AppInfo struct {
	Name    string   `json:"name"`            // Dateiname klein ("steam.exe") oder config.AppDownloads
	Bytes   int64    `json:"bytes"`           // seit Programmstart über WlanMerge
	Phone   int64    `json:"phone"`           // davon über Handy-Leitungen
	Allowed bool     `json:"allowed"`         // darf das Handy nutzen (nur wichtig, wenn der Filter an ist)
	Label   string   `json:"label,omitempty"` // Anzeigename bekannter Programme ("Battle.net")
	Files   []string `json:"files,omitempty"` // Dateien bekannter Programme
}

// app findet das Programm zu einer neuen Verbindung und liefert seinen Zähler.
func (b *Balancer) app(m *M.Metadata, udp bool) (string, *appStat) {
	if b.AppOf == nil || (udp && m.DstPort == 53) { // DNS fragt immer Windows selbst
		return "", nil
	}
	name := b.AppOf(udp, m.SrcPort, m.DestinationAddrPort())
	if name == "" {
		return "", nil
	}
	return name, b.appStat(name)
}

func (b *Balancer) appStat(name string) *appStat {
	b.appMu.Lock()
	defer b.appMu.Unlock()
	if b.apps == nil {
		b.apps = map[string]*appStat{}
	}
	s := b.apps[name]
	if s == nil {
		if len(b.apps) >= 200 { // lange Laufzeit: alte Einträge ohne Verkehr vergessen
			old := time.Now().Add(-time.Hour).UnixNano()
			for k, v := range b.apps {
				if v.last.Load() < old {
					delete(b.apps, k)
				}
			}
		}
		s = &appStat{}
		b.apps[name] = s
	}
	s.last.Store(time.Now().UnixNano())
	return s
}

// Apps liefert die Auswahl "wer darf das Handy nutzen": zuerst Programme mit
// Verkehr (Handy zuerst, höchstens 20), dann Browser-Downloads, freigegebene
// Programme ohne Verkehr und die bekannten Programme. Bekannte Programme mit
// mehreren Dateien (Battle.net + agent.exe) erscheinen als ein Eintrag.
func (b *Balancer) Apps() []AppInfo {
	cfg := b.cfg.Load()
	raw := map[string][2]int64{}
	b.appMu.Lock()
	for name, s := range b.apps {
		raw[name] = [2]int64{s.bytes.Load(), s.phone.Load()}
	}
	b.appMu.Unlock()

	var used, known []AppInfo
	grouped := map[string]bool{}
	for _, k := range config.KnownApps {
		a := AppInfo{Name: k.Exes[0], Label: k.Label, Files: k.Exes, Allowed: cfg.PhoneApps[k.Exes[0]]}
		for _, e := range k.Exes {
			grouped[e] = true
			a.Bytes += raw[e][0]
			a.Phone += raw[e][1]
		}
		if a.Bytes > 0 {
			used = append(used, a)
		} else {
			known = append(known, a)
		}
	}
	for name, v := range raw {
		if !grouped[name] && v[0] > 0 {
			used = append(used, AppInfo{Name: name, Bytes: v[0], Phone: v[1], Allowed: cfg.PhoneApps[name]})
		}
	}
	sort.Slice(used, func(i, j int) bool {
		if used[i].Phone != used[j].Phone {
			return used[i].Phone > used[j].Phone
		}
		if used[i].Bytes != used[j].Bytes {
			return used[i].Bytes > used[j].Bytes
		}
		return used[i].Name < used[j].Name
	})
	var out []AppInfo
	seen := map[string]bool{}
	for _, a := range used {
		if len(out) < 20 || a.Allowed { // freigegebene nie wegkürzen
			out = append(out, a)
			seen[a.Name] = true
		}
	}
	if !seen[config.AppDownloads] {
		out = append(out, AppInfo{Name: config.AppDownloads, Allowed: cfg.PhoneApps[config.AppDownloads]})
		seen[config.AppDownloads] = true
	}
	var allowed []string
	for name, on := range cfg.PhoneApps {
		if on && !seen[name] && !grouped[name] {
			allowed = append(allowed, name)
		}
	}
	sort.Strings(allowed)
	for _, name := range allowed {
		out = append(out, AppInfo{Name: name, Allowed: true})
	}
	return append(out, known...)
}
func isV6(a netip.Addr) bool { return a.Is6() && !a.Is4In6() }

// DialContext wird von tun2socks für jede neue TCP-Verbindung aufgerufen.
func (b *Balancer) DialContext(ctx context.Context, m *M.Metadata) (net.Conn, error) {
	if isV6(m.DstIP) {
		return nil, errNoIPv6 // sofort ablehnen → das Programm nimmt IPv4
	}
	app, as := b.app(m, false)
	cands, pinned := b.candidates(m, false, app)
	if len(cands) == 0 {
		return nil, errors.New("keine passende Leitung")
	}
	dst := m.DstIP.Unmap()
	var lastErr error
	for i, l := range cands {
		dctx, cancel := ctx, context.CancelFunc(func() {})
		if i < len(cands)-1 { // nicht die letzte Chance: kürzer warten
			dctx, cancel = context.WithTimeout(ctx, firstDialLimit)
		}
		c, err := l.Adapter().DialContext(dctx, "tcp", m.DestinationAddress())
		cancel()
		if ctx.Err() == nil {
			l.noteDial(err)
		}
		if err == nil {
			cc := &countedConn{Conn: c, l: l, b: b, dst: dst, born: time.Now(), pinned: pinned, app: as, mobile: l.Adapter().Mobile()}
			l.activeTCP.Add(1)
			l.conns.Store(cc, struct{}{})
			b.dstAdd(dst, 1)
			return cc, nil
		}
		lastErr = fmt.Errorf("%s: %w", l.name(), err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

// DialUDP wird von tun2socks für jede neue UDP-Sitzung aufgerufen.
func (b *Balancer) DialUDP(m *M.Metadata) (net.PacketConn, error) {
	if isV6(m.DstIP) {
		return nil, errNoIPv6
	}
	var lastErr error = errors.New("keine passende Leitung")
	app, as := b.app(m, true)
	cands, _ := b.candidates(m, true, app)
	for _, l := range cands {
		pc, err := l.Adapter().ListenUDP()
		if err != nil {
			lastErr = err
			continue
		}
		cp := &countedPacketConn{PacketConn: pc, l: l, app: as, mobile: l.Adapter().Mobile()}
		if m.DstPort == 53 {
			cp.dns = b.dns // DNS-Antworten mitlesen (für die Login-Regeln)
		}
		if m.DstPort == 443 { // QUIC zählt wie TCP: mehrere zum selben Ziel = Download
			cp.b, cp.dst = b, m.DstIP.Unmap()
			b.dstAdd(cp.dst, 1)
		}
		l.activeUDP.Add(1)
		l.conns.Store(cp, struct{}{})
		return cp, nil
	}
	return nil, lastErr
}

// DownloadLine ist eine Leitung für den Download-Manager.
type DownloadLine struct {
	Name    string
	Primary bool
	Dial    func(ctx context.Context, network, addr string) (net.Conn, error)
}

// DownloadLines liefert die funktionierenden Leitungen (Hauptleitung zuerst).
// Ihre Verbindungen werden wie alle anderen gezählt (Verbrauch, Datenlimit,
// Anzeige) und bei Limit/Störung geschlossen. Umverteilt werden sie nicht –
// das erledigt der Download-Manager selbst.
//
// Programm-Filter: Browser-Downloads zählen als eigenes "Programm"
// (config.AppDownloads). Ist es nicht freigegeben, bleibt das Handy außen vor –
// außer es ist die einzige Leitung.
func (b *Balancer) DownloadLines() []DownloadLine {
	var links []*Link
	for _, l := range b.Links() {
		if l.getState() == StateOK {
			links = append(links, l)
		}
	}
	if !b.cfg.Load().MayUsePhone(config.AppDownloads) {
		if fixed := noMobile(links); len(fixed) > 0 {
			links = fixed
		}
	}
	as := b.appStat(config.AppDownloads)
	var out []DownloadLine
	for _, l := range links {
		l := l
		out = append(out, DownloadLine{
			Name:    l.name(),
			Primary: len(out) == 0,
			Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if l.getState() != StateOK {
					return nil, errors.New("Leitung nicht verfügbar")
				}
				c, err := l.Adapter().DialContext(ctx, "tcp", addr)
				if err != nil {
					return nil, err
				}
				dst, _ := netip.ParseAddrPort(addr)
				cc := &countedConn{Conn: c, l: l, dst: dst.Addr().Unmap(), born: time.Now(), pinned: true, app: as, mobile: l.Adapter().Mobile()}
				l.activeTCP.Add(1)
				l.conns.Store(cc, struct{}{})
				return cc, nil
			},
		})
	}
	return out
}

// CloseAll schließt alle Verbindungen (beim Ausschalten).
func (b *Balancer) CloseAll() {
	for _, l := range b.Links() {
		l.closeAll()
	}
}

// Stats liefert eine Momentaufnahme aller Leitungen.
func (b *Balancer) Stats() []Stat {
	cfg := b.cfg.Load()
	prim := b.primary()
	var out []Stat
	for _, l := range b.Links() {
		a := l.Adapter()
		l.mu.Lock()
		s := Stat{
			Name: a.Name, Kind: a.Kind, State: l.state, Primary: l == prim,
			Target: l.target * 100, Share: l.share * 100,
			RxRate: l.rateRx, TxRate: l.rateTx, Capacity: int64(l.capacity),
		}
		l.mu.Unlock()
		s.RTTms = l.rttMs.Load()
		s.Conns = l.activeTCP.Load() + l.activeUDP.Load()
		s.Used = b.usage.Get(a.Name).Bytes
		s.Limit = cfg.LimitFor(a.Name, a.Mobile())
		s.Disabled = cfg.IsDisabled(a.Name)
		out = append(out, s)
	}
	return out
}

// countedConn zählt die Bytes einer TCP-Verbindung für ihre Leitung.
type countedConn struct {
	net.Conn
	l      *Link
	b      *Balancer // nil bei Verbindungen des Download-Managers (zählen nicht als "Ziel")
	once   sync.Once
	dst    netip.Addr
	born   time.Time
	pinned bool
	app    *appStat     // Zähler des Programms (kann nil sein)
	mobile bool         // läuft über eine Handy-Leitung
	hot    bool         // Ziel schon als Download-Server gemerkt (nur von Read benutzt)
	rx     atomic.Int64 // Bytes dieser Verbindung (für die Umverteilung)
	lastRx int64        // nur vom Umverteiler benutzt
}

// count bucht n Bytes beim Programm.
func count(app *appStat, mobile bool, n int) {
	if app == nil || n <= 0 {
		return
	}
	app.bytes.Add(int64(n))
	if mobile {
		app.phone.Add(int64(n))
	}
}

func (c *countedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.l.rx.Add(int64(n))
	count(c.app, c.mobile, n)
	if c.rx.Add(int64(n)) >= hotBytes && !c.hot && c.b != nil {
		c.hot = true
		c.b.markHot(c.dst) // hier läuft ein Download: weitere Verbindungen sofort verteilen
	}
	return n, err
}

func (c *countedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.l.tx.Add(int64(n))
	count(c.app, c.mobile, n)
	return n, err
}

func (c *countedConn) Close() error {
	c.once.Do(func() {
		c.l.activeTCP.Add(-1)
		c.l.conns.Delete(c)
		if c.b != nil {
			c.b.dstAdd(c.dst, -1)
		}
	})
	return c.Conn.Close()
}

// Halb-Schließen weiterreichen (wichtig für manche Protokolle).
func (c *countedConn) CloseRead() error {
	if cr, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return cr.CloseRead()
	}
	return nil
}

func (c *countedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

type countedPacketConn struct {
	net.PacketConn
	l      *Link
	dns    *rules.DNSCache
	once   sync.Once
	app    *appStat
	mobile bool
	b      *Balancer // nur bei QUIC gesetzt (zählt dann als Verbindung zum Ziel)
	dst    netip.Addr
}

func (c *countedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, a, err := c.PacketConn.ReadFrom(p)
	c.l.rx.Add(int64(n))
	count(c.app, c.mobile, n)
	if c.dns != nil && n > 0 {
		c.dns.Learn(p[:n])
	}
	return n, a, err
}

func (c *countedPacketConn) WriteTo(p []byte, a net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(p, a)
	c.l.tx.Add(int64(n))
	count(c.app, c.mobile, n)
	return n, err
}

func (c *countedPacketConn) Close() error {
	c.once.Do(func() {
		c.l.activeUDP.Add(-1)
		c.l.conns.Delete(c)
		if c.b != nil {
			c.b.dstAdd(c.dst, -1)
		}
	})
	return c.PacketConn.Close()
}

// HumanBytes: 1234567 → "1.2 MB".
func HumanBytes(b int64) string {
	switch {
	case b >= 1e9:
		return fmt.Sprintf("%.2f GB", float64(b)/1e9)
	case b >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(b)/1e6)
	default:
		return fmt.Sprintf("%.0f KB", float64(b)/1e3)
	}
}
