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
			if b.active.Load() {
				b.checkHealth(ctx)
			}
		case <-reb.C:
			if b.active.Load() {
				b.rebalance()
			}
		case <-slow.C:
			b.monthlyReset()
			b.usage.Save()
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
	for _, l := range links {
		l.mu.Lock()
		if sum > 0 {
			l.share = l.ewma / sum
		} else {
			l.share = 0
		}
		l.mu.Unlock()
	}
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
		// Nur schließen, wenn es eine andere funktionierende Leitung gibt –
		// sonst würden wir bei einem Fehlalarm alles abbrechen.
		if b.anotherOK(l) {
			n := l.closeAll()
			b.event(EventInfo, "%s gestört – %d Verbindungen auf andere Leitungen umgeleitet", name, n)
		} else {
			b.event(EventInfo, "%s gestört – keine andere Leitung verfügbar", name)
		}
	case StateOK:
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
		if flowing {
			l.hcFails = 0
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
func (b *Balancer) candidates(m *M.Metadata, udp bool) (cands []*Link, pinned bool) {
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
	// Direkt nach einer Umverteilung: die neue Verbindung auf die freie Leitung.
	if p := b.reb.preferred(); p != nil {
		for i, x := range sc {
			if x.l == p {
				sc[0], sc[i] = sc[i], sc[0]
				break
			}
		}
	}
	out := make([]*Link, 0, len(sc)+len(down))
	for _, x := range sc {
		out = append(out, x.l)
	}
	return append(out, down...), false
}

func isV6(a netip.Addr) bool { return a.Is6() && !a.Is4In6() }

// DialContext wird von tun2socks für jede neue TCP-Verbindung aufgerufen.
func (b *Balancer) DialContext(ctx context.Context, m *M.Metadata) (net.Conn, error) {
	if isV6(m.DstIP) {
		return nil, errNoIPv6 // sofort ablehnen → das Programm nimmt IPv4
	}
	cands, pinned := b.candidates(m, false)
	if len(cands) == 0 {
		return nil, errors.New("keine passende Leitung")
	}
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
			cc := &countedConn{Conn: c, l: l, dst: m.DstIP.Unmap(), born: time.Now(), pinned: pinned}
			l.activeTCP.Add(1)
			l.conns.Store(cc, struct{}{})
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
	cands, _ := b.candidates(m, true)
	for _, l := range cands {
		pc, err := l.Adapter().ListenUDP()
		if err != nil {
			lastErr = err
			continue
		}
		cp := &countedPacketConn{PacketConn: pc, l: l}
		if m.DstPort == 53 {
			cp.dns = b.dns // DNS-Antworten mitlesen (für die Login-Regeln)
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
func (b *Balancer) DownloadLines() []DownloadLine {
	var out []DownloadLine
	for _, l := range b.Links() {
		if l.getState() != StateOK {
			continue
		}
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
				cc := &countedConn{Conn: c, l: l, dst: dst.Addr().Unmap(), born: time.Now(), pinned: true}
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
	once   sync.Once
	dst    netip.Addr
	born   time.Time
	pinned bool
	rx     atomic.Int64 // Bytes dieser Verbindung (für die Umverteilung)
	lastRx int64        // nur vom Umverteiler benutzt
}

func (c *countedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.l.rx.Add(int64(n))
	c.rx.Add(int64(n))
	return n, err
}

func (c *countedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.l.tx.Add(int64(n))
	return n, err
}

func (c *countedConn) Close() error {
	c.once.Do(func() {
		c.l.activeTCP.Add(-1)
		c.l.conns.Delete(c)
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
	l    *Link
	dns  *rules.DNSCache
	once sync.Once
}

func (c *countedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, a, err := c.PacketConn.ReadFrom(p)
	c.l.rx.Add(int64(n))
	if c.dns != nil && n > 0 {
		c.dns.Learn(p[:n])
	}
	return n, a, err
}

func (c *countedPacketConn) WriteTo(p []byte, a net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(p, a)
	c.l.tx.Add(int64(n))
	return n, err
}

func (c *countedPacketConn) Close() error {
	c.once.Do(func() {
		c.l.activeUDP.Add(-1)
		c.l.conns.Delete(c)
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
