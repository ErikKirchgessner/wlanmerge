// Package rules entscheidet, ob ein Ziel immer über eine feste Leitung geht.
//
// Problem: Im Tunnel sehen wir nur IP-Adressen, Regeln sind aber Namen wie
// "*.battle.net". Lösung: Wir lesen die DNS-Antworten mit, die durch den
// Tunnel laufen, und merken uns, welche IP zu welchem Namen gehört.
package rules

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"wlanmerge/internal/config"
)

type compiled struct {
	exact  string // "battle.net" passt auf battle.net und alle Subdomains
	suffix string // "*.battle.net" passt nur auf Subdomains
	prefix netip.Prefix
	link   string
}

// Matcher prüft Ziele gegen die Regelliste.
type Matcher struct{ rules []compiled }

func Compile(rs []config.Rule) *Matcher {
	m := &Matcher{}
	for _, r := range rs {
		match := strings.ToLower(strings.TrimSpace(r.Match))
		c := compiled{link: r.Link}
		switch {
		case match == "":
			continue
		case strings.HasPrefix(match, "*."):
			c.suffix = match[1:] // ".battle.net"
		default:
			if p, err := netip.ParsePrefix(match); err == nil {
				c.prefix = p
			} else if a, err := netip.ParseAddr(match); err == nil {
				c.prefix = netip.PrefixFrom(a, a.BitLen())
			} else {
				c.exact = strings.TrimSuffix(match, ".")
			}
		}
		m.rules = append(m.rules, c)
	}
	return m
}

// Match liefert die feste Leitung für ein Ziel, falls eine Regel passt.
//
// Bei Namen gilt: ALLE bekannten Namen der IP müssen zu einer Regel passen.
// Große CDNs (Akamai, Cloudflare …) benutzen dieselbe IP für viele Seiten –
// sonst würde z. B. ein Steam-Download auf der IP der Steam-Shopseite mit
// festgehalten und nicht mehr gebündelt.
func (m *Matcher) Match(ip netip.Addr, names []string) (string, bool) {
	ip = ip.Unmap()
	for _, r := range m.rules {
		if r.prefix.IsValid() && r.prefix.Contains(ip) {
			return r.link, true
		}
	}
	if len(names) == 0 {
		return "", false
	}
	link := ""
	for _, n := range names {
		l, ok := m.matchName(n)
		if !ok {
			return "", false
		}
		if link == "" {
			link = l
		}
	}
	return link, true
}

func (m *Matcher) matchName(n string) (string, bool) {
	for _, r := range m.rules {
		if (r.suffix != "" && strings.HasSuffix(n, r.suffix)) ||
			(r.exact != "" && (n == r.exact || strings.HasSuffix(n, "."+r.exact))) {
			return r.link, true
		}
	}
	return "", false
}

// ExactDomains: Namen ohne Platzhalter – die können wir selbst auflösen.
func (m *Matcher) ExactDomains() []string {
	var out []string
	for _, r := range m.rules {
		if r.exact != "" {
			out = append(out, r.exact)
		}
	}
	return out
}

// DNSCache merkt sich IP → Namen aus mitgelesenen DNS-Antworten.
type DNSCache struct {
	mu sync.Mutex
	m  map[netip.Addr]dnsEntry
}

type dnsEntry struct {
	names []string
	exp   time.Time
}

const (
	minKeep    = 10 * time.Minute // Verbindungen starten oft erst nach Ablauf der DNS-TTL
	maxEntries = 50000
)

func NewDNSCache() *DNSCache { return &DNSCache{m: map[netip.Addr]dnsEntry{}} }

// Names liefert die bekannten Namen zu einer IP.
func (c *DNSCache) Names(ip netip.Addr) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[ip.Unmap()]
	if !ok || time.Now().After(e.exp) {
		return nil
	}
	return e.names
}

func (c *DNSCache) add(ip netip.Addr, names []string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= maxEntries {
		now := time.Now()
		for k, e := range c.m {
			if now.After(e.exp) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= maxEntries {
			c.m = map[netip.Addr]dnsEntry{} // Notbremse gegen Speicherwachstum
		}
	}
	ip = ip.Unmap()
	exp := time.Now().Add(max(ttl, minKeep))
	// Namen sammeln statt überschreiben: eine IP kann zu vielen Seiten gehören.
	if old, ok := c.m[ip]; ok && time.Now().Before(old.exp) {
		merged := append([]string(nil), old.names...)
		for _, n := range names {
			if !slices.Contains(merged, n) && len(merged) < 12 {
				merged = append(merged, n)
			}
		}
		names = merged
		exp = later(exp, old.exp)
	}
	c.m[ip] = dnsEntry{names: names, exp: exp}
}

// Learn liest eine rohe DNS-Antwort. Fehlerhafte Pakete werden ignoriert.
func (c *DNSCache) Learn(msg []byte) {
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil || !h.Response {
		return
	}
	q, err := p.Question()
	if err != nil {
		return
	}
	names := []string{normName(q.Name.String())}
	p.SkipAllQuestions()
	for {
		ah, err := p.AnswerHeader()
		if err != nil {
			return
		}
		ttl := time.Duration(ah.TTL) * time.Second
		switch ah.Type {
		case dnsmessage.TypeA:
			r, err := p.AResource()
			if err != nil {
				return
			}
			c.add(netip.AddrFrom4(r.A), names, ttl)
		case dnsmessage.TypeAAAA:
			r, err := p.AAAAResource()
			if err != nil {
				return
			}
			c.add(netip.AddrFrom16(r.AAAA), names, ttl)
		// CNAME-Ziele (z. B. edge.akamai…) sind nur Aliase des angefragten Namens –
		// für Regeln zählt der Name, den das Programm wirklich angefragt hat.
		default:
			if p.SkipAnswer() != nil {
				return
			}
		}
	}
}

// Resolve löst Namen selbst auf und trägt sie ein (für Regeln ohne Platzhalter).
func (c *DNSCache) Resolve(ctx context.Context, domains []string) {
	for _, d := range domains {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", d)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			c.add(ip, []string{d}, time.Hour)
		}
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func normName(s string) string { return strings.TrimSuffix(strings.ToLower(s), ".") }
