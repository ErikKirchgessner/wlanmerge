package balancer

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	"wlanmerge/internal/config"
	"wlanmerge/internal/netif"
	"wlanmerge/internal/rules"
	"wlanmerge/internal/usage"
)

// testBalancer baut einen Balancer mit zwei erfundenen Leitungen.
func testBalancer(t *testing.T, cfg *config.Config) (*Balancer, *Link, *Link) {
	t.Setenv("WM_DATA_DIR", t.TempDir())
	b := &Balancer{usage: usage.Load(), dns: rules.NewDNSCache()}
	b.cfg.Store(cfg)
	mk := func(name, kind string) *Link {
		l := &Link{state: StateOK}
		l.healthy.Store(true)
		a := netif.Adapter{Name: name, Kind: kind, IPv4: net.IPv4(10, 0, 0, 1)}
		l.adapter.Store(&a)
		return l
	}
	wlan, phone := mk("WLAN", netif.KindWLAN), mk("Handy", netif.KindPhone)
	b.links = []*Link{wlan, phone}
	b.recompute()
	return b, wlan, phone
}

func cands(b *Balancer, m *M.Metadata, udp bool) []*Link { c, _ := b.candidates(m, udp); return c }
func first(b *Balancer, m *M.Metadata, udp bool) *Link   { return cands(b, m, udp)[0] }

var meta = &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("93.184.216.34"), DstPort: 443}

// simulate verteilt n neue Verbindungen und zählt, wohin sie gehen.
func simulate(b *Balancer, n int) map[*Link]int {
	got := map[*Link]int{}
	for i := 0; i < n; i++ {
		l := first(b, meta, false)
		l.activeTCP.Add(1)
		got[l]++
	}
	return got
}

func TestFollowsCapacity(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default())
	wlan.capacity, phone.capacity = 20e6, 10e6 // WLAN doppelt so schnell
	b.recompute()
	got := simulate(b, 90)
	if got[wlan] < 55 || got[wlan] > 65 {
		t.Fatalf("erwartet ~60/30 (2:1), bekommen %d/%d", got[wlan], got[phone])
	}
}

func TestMinShareKeepsSlowLinkMeasured(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default())
	wlan.capacity, phone.capacity = 100e6, 0
	b.recompute()
	if phone.target < minShare-0.001 {
		t.Fatalf("langsame Leitung sollte mind. %.0f%% bekommen, hat %.1f%%", minShare*100, phone.target*100)
	}
}

func TestLimitDisablesLink(t *testing.T) {
	cfg := config.Default()
	cfg.Limits["Handy"] = 1000
	b, wlan, phone := testBalancer(t, cfg)
	b.usage.Add("Handy", 2000)
	b.recompute()
	if phone.getState() != StateLimit {
		t.Fatalf("Zustand %q statt limit", phone.getState())
	}
	for _, l := range cands(b, meta, false) {
		if l == phone {
			t.Fatal("Leitung über Limit darf nie benutzt werden")
		}
	}
	if got := simulate(b, 10); got[wlan] != 10 {
		t.Fatal("alles muss über WLAN gehen")
	}
	ev := b.Events()
	if len(ev) == 0 || ev[len(ev)-1].Kind != EventLimit {
		t.Fatal("beim Limit muss eine Limit-Meldung kommen (für die Benachrichtigung)")
	}
}

func TestMonthlyResetFreesLimit(t *testing.T) {
	cfg := config.Default()
	cfg.Limits["Handy"] = 1000
	b, _, phone := testBalancer(t, cfg)
	b.usage.Add("Handy", 2000)
	b.recompute()
	if phone.getState() != StateLimit {
		t.Fatal("Limit sollte erreicht sein")
	}
	// Zähler läuft seit letztem Monat → beim nächsten Durchlauf zurücksetzen
	b.usage.ResetBefore(time.Time{}) // harmlos
	if names := b.usage.ResetBefore(time.Now().Add(time.Hour)); len(names) != 1 {
		t.Fatal("Handy-Zähler hätte zurückgesetzt werden müssen")
	}
	b.recompute()
	if phone.getState() != StateOK {
		t.Fatal("nach dem Zurücksetzen muss das Handy wieder genutzt werden")
	}
}

func TestDownLinkOnlyLastResort(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default())
	wlan.healthy.Store(false)
	b.recompute()
	c := cands(b, meta, false)
	if c[0] != phone || c[len(c)-1] != wlan {
		t.Fatal("gestörte Leitung muss ans Ende")
	}
}

func TestLoginAndGamesUsePrimary(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default())
	phone.activeTCP.Store(0)
	wlan.activeTCP.Store(50) // WLAN voll belastet – trotzdem muss die Regel gelten

	ip := netip.MustParseAddr("1.2.3.4")
	b.dns.Learn(dnsAnswer(t, "eu.actual.battle.net", ip))
	login := &M.Metadata{Network: M.TCP, DstIP: ip, DstPort: 443}
	if c, pinned := b.candidates(login, false); !pinned || c[0] != wlan {
		t.Fatal("Battle.net-Login muss auf der Hauptleitung bleiben")
	}
	game := &M.Metadata{Network: M.UDP, DstIP: netip.MustParseAddr("8.8.4.4"), DstPort: 3074}
	if first(b, game, true) != wlan {
		t.Fatal("Spiele-UDP muss auf der Hauptleitung bleiben")
	}
}

func TestPrimaryPrefersNonPhoneAndFallsBack(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default())
	if b.primary() != wlan {
		t.Fatal("Hauptleitung sollte WLAN sein")
	}
	wlan.healthy.Store(false)
	b.recompute()
	if b.primary() != phone {
		t.Fatal("bei WLAN-Ausfall muss das Handy Hauptleitung werden")
	}
}

func TestIPv6Rejected(t *testing.T) {
	b, _, _ := testBalancer(t, config.Default())
	m := &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("2a01:bc80::1"), DstPort: 443}
	if _, err := b.DialContext(context.Background(), m); err != errNoIPv6 {
		t.Fatalf("IPv6 muss sofort abgelehnt werden, bekam %v", err)
	}
	if _, err := b.DialUDP(&M.Metadata{Network: M.UDP, DstIP: netip.MustParseAddr("2a01:bc80::1"), DstPort: 443}); err != errNoIPv6 {
		t.Fatalf("IPv6-UDP muss sofort abgelehnt werden, bekam %v", err)
	}
}

// Sparsame Gesundheitsprüfung: Fließen Daten, wird nicht geprüft (und die Leitung
// bleibt gesund); hängt eine Leitung mit Verbindungen, wird sie weiter geprüft.
func TestHealthSkipsLinesWithTraffic(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default())
	wlan.activeTCP.Store(1)  // hat Verbindungen, aber es kommt nichts mehr an
	phone.activeTCP.Store(1) // hat Verbindungen und Daten fließen
	for i := 0; i < 3; i++ {
		phone.rx.Add(100_000)
		b.checkHealth(context.Background()) // Test-Verbindungen schlagen hier immer fehl
	}
	if !phone.healthy.Load() {
		t.Fatal("Leitung mit fließenden Daten darf nicht als gestört gelten")
	}
	if wlan.healthy.Load() {
		t.Fatal("hängende Leitung muss weiter geprüft und als gestört erkannt werden")
	}
	// Ohne Verbindungen: nur selten prüfen
	idle := phone
	idle.activeTCP.Store(0)
	idle.mu.Lock()
	idle.hcLast = time.Now()
	idle.mu.Unlock()
	before := idle.hcLast
	b.checkHealth(context.Background())
	if idle.hcLast != before {
		t.Fatal("Leitung ohne Verbindungen wurde zu früh wieder geprüft")
	}
}
