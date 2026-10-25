package balancer

import (
	"net/netip"
	"sync"
	"time"
)

// Umverteilen laufender Downloads.
//
// Eine laufende TCP-Verbindung kann ohne eigenen Server nicht die Leitung
// wechseln. Download-Programme wie Steam, Battle.net und Epic öffnen aber viele
// Verbindungen zum selben Server und bauen eine geschlossene sofort neu auf.
// Das nutzen wir: Trägt eine Leitung deutlich weniger als ihr Soll, schließen
// wir ab und zu EINE solche Verbindung auf der vollen Leitung – die neue landet
// auf der freien Leitung. Einzelne Verbindungen (z. B. ein Browser-Download)
// werden nie angefasst.
const (
	rebalanceEvery    = 2 * time.Second
	rebalanceCooldown = 6 * time.Second // Zeit, damit die neue Verbindung anläuft
	minGroup          = 3               // so viele Verbindungen zum selben Server = Download-Programm
	minConnAge        = 5 * time.Second
	minConnRate       = 64_000  // Bytes/s – darunter lohnt das Verschieben nicht
	minGroupRate      = 250_000 // Bytes/s (2 Mbit/s) – darunter gibt es nichts zu verteilen
	stallAfter        = 20 * time.Second
	preferFor         = 5 * time.Second
)

type rebalancer struct {
	mu            sync.Mutex
	last          time.Time
	cooldownUntil time.Time
	lastActive    map[*countedConn]time.Time
	lastEvent     time.Time

	preferMu    sync.Mutex
	prefer      *Link // die nächste Verbindung bevorzugt hierhin
	preferUntil time.Time
}

func (r *rebalancer) preferred() *Link {
	r.preferMu.Lock()
	defer r.preferMu.Unlock()
	if time.Now().After(r.preferUntil) {
		return nil
	}
	return r.prefer
}

func (b *Balancer) rebalance() {
	r := &b.reb
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	dt := now.Sub(r.last).Seconds()
	r.last = now
	if dt <= 0 || dt > 10 {
		dt = rebalanceEvery.Seconds()
	}
	if r.lastActive == nil {
		r.lastActive = map[*countedConn]time.Time{}
	}

	type conn struct {
		c    *countedConn
		l    *Link
		rate float64
	}
	var all []conn
	group := map[netip.Addr]int{}
	busyDst := map[netip.Addr]bool{}
	alive := map[*countedConn]bool{}
	var ok []*Link
	for _, l := range b.Links() {
		isOK := l.getState() == StateOK
		if isOK {
			ok = append(ok, l)
		}
		l.conns.Range(func(k, _ any) bool {
			cc, isTCP := k.(*countedConn)
			if !isTCP {
				return true
			}
			rx := cc.rx.Load()
			rate := float64(rx-cc.lastRx) / dt
			cc.lastRx = rx
			alive[cc] = true
			if rate > 0 {
				r.lastActive[cc] = now
			} else if _, seen := r.lastActive[cc]; !seen {
				r.lastActive[cc] = cc.born
			}
			all = append(all, conn{cc, l, rate})
			group[cc.dst]++
			if rate >= minConnRate {
				busyDst[cc.dst] = true
			}
			return true
		})
	}
	for cc := range r.lastActive { // Speicher geschlossener Verbindungen freigeben
		if !alive[cc] {
			delete(r.lastActive, cc)
		}
	}

	// 1. Hängende Verbindungen: steht eine Download-Verbindung still, während
	//    ihre Geschwister zum selben Server laufen, ist ihr Weg kaputt.
	closed := 0
	for _, x := range all {
		if closed >= 2 {
			break
		}
		if group[x.c.dst] >= minGroup && busyDst[x.c.dst] && x.rate == 0 &&
			now.Sub(x.c.born) > stallAfter && now.Sub(r.lastActive[x.c]) > stallAfter {
			x.c.Close()
			closed++
		}
	}

	// 2. Verteilung korrigieren (nicht in derselben Runde wie das Aufräumen).
	if closed > 0 || len(ok) < 2 || now.Before(r.cooldownUntil) || !b.cfg.Load().Rebalance {
		return
	}
	rate := map[*Link]float64{}
	count := map[*Link]int{}
	var total float64
	var totalCount int
	for _, x := range all {
		if group[x.c.dst] < minGroup {
			continue
		}
		rate[x.l] += x.rate
		count[x.l]++
		total += x.rate
		totalCount++
	}
	if total < minGroupRate {
		return
	}
	var under, over *Link
	var underGap, overGap float64
	for _, l := range ok {
		l.mu.Lock()
		t := l.target
		l.mu.Unlock()
		share := rate[l] / total
		connShare := float64(count[l]) / float64(totalCount)
		// Zu wenig Daten, obwohl die Leitung nicht schon genug Verbindungen hat
		// (hat sie die, ist sie einfach am Limit – dann bringt Verschieben nichts).
		if t >= 0.05 && share < t*0.6 && connShare < t {
			if g := t - share; g > underGap {
				under, underGap = l, g
			}
		}
		if g := share - t; g > overGap {
			over, overGap = l, g
		}
	}
	if under == nil || over == nil || under == over {
		return
	}
	var victim *conn
	for i := range all {
		x := &all[i]
		if x.l != over || x.c.pinned || group[x.c.dst] < minGroup ||
			now.Sub(x.c.born) < minConnAge || x.rate < minConnRate {
			continue
		}
		if victim == nil || x.rate > victim.rate {
			victim = x
		}
	}
	if victim == nil {
		return
	}
	r.preferMu.Lock()
	r.prefer, r.preferUntil = under, now.Add(preferFor)
	r.preferMu.Unlock()
	victim.c.Close()
	r.cooldownUntil = now.Add(rebalanceCooldown)
	if now.Sub(r.lastEvent) > time.Minute {
		r.lastEvent = now
		b.event(EventInfo, "Laufende Downloads werden umverteilt: %s → %s", over.name(), under.name())
	}
}
