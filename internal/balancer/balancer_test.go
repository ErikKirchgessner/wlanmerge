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

func cands(b *Balancer, m *M.Metadata, udp bool) []*Link { c, _ := b.candidates(m, udp, ""); return c }
func first(b *Balancer, m *M.Metadata, udp bool) *Link   { return cands(b, m, udp)[0] }

var meta = &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("93.184.216.34"), DstPort: 443}

// simulate verteilt n neue Verbindungen und zählt, wohin sie gehen.
func simulate(b *Balancer, n int) map[*Link]int {
	got := map[*Link]int{}
	for i := 0; i < n; i++ {
		l := first(b, meta, false)
		l.activeTCP.Add(1)
		b.dstAdd(meta.DstIP, 1) // wie DialContext: offene Verbindung zu diesem Ziel
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
	if c, pinned := b.candidates(login, false, ""); !pinned || c[0] != wlan {
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

// Surfen: Einzelne Verbindungen gehen über die feste Leitung (bester Ping, kein
// Handy-Datenvolumen) – auch wenn die Zählung das Handy vorziehen würde. Erst
// was nach Download aussieht, wird verteilt.
func TestSurfingStaysOnMainLine(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default())
	wlan.activeTCP.Store(20)
	site := &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("198.51.100.7"), DstPort: 443}
	if first(b, site, false) != wlan {
		t.Fatal("einzelne Verbindung (Surfen) muss über die feste Leitung gehen")
	}
	b.dstAdd(site.DstIP, 2) // zwei Verbindungen zum selben Server sind schon offen
	if first(b, site, false) != phone {
		t.Fatal("ab der dritten Verbindung zum selben Ziel ist es ein Download: verteilen")
	}
	b.dstAdd(site.DstIP, -2)

	other := &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("198.51.100.8"), DstPort: 443}
	b.markHot(other.DstIP)
	if first(b, other, false) != phone {
		t.Fatal("Ziel mit laufendem Download: neue Verbindungen sofort verteilen")
	}

	// Feste Leitung ausgelastet → auch einzelne Verbindungen verteilen
	wlan.mu.Lock()
	wlan.ewma, wlan.capacity = 2*bulkRate, 2*bulkRate
	wlan.mu.Unlock()
	if first(b, site, false) != phone {
		t.Fatal("bei ausgelasteter Surf-Leitung soll verteilt werden")
	}
}

// Programm-Filter: Nur freigegebene Programme dürfen das Handy nutzen – außer
// es ist die einzige Leitung (nie ohne Internet).
func TestAppFilter(t *testing.T) {
	cfg := config.Default()
	cfg.AppFilter = true
	cfg.PhoneApps = map[string]bool{"steam.exe": true}
	b, wlan, phone := testBalancer(t, cfg)
	wlan.activeTCP.Store(20)
	dl := &M.Metadata{Network: M.TCP, DstIP: netip.MustParseAddr("198.51.100.9"), DstPort: 443}
	b.markHot(dl.DstIP)

	if c, _ := b.candidates(dl, false, "steam.exe"); c[0] != phone {
		t.Fatal("freigegebenes Programm soll das Handy nutzen dürfen")
	}
	for _, app := range []string{"chrome.exe", ""} {
		c, _ := b.candidates(dl, false, app)
		for _, l := range c {
			if l == phone {
				t.Fatalf("nicht freigegebenes Programm %q darf nicht aufs Handy", app)
			}
		}
	}
	// WLAN fällt weg → Handy ist die einzige Leitung und darf dann benutzt werden
	wlan.mu.Lock()
	wlan.state = StateLimit
	wlan.mu.Unlock()
	if c, _ := b.candidates(dl, false, "chrome.exe"); len(c) != 1 || c[0] != phone {
		t.Fatal("ohne andere Leitung muss das Handy als Notlösung bleiben")
	}
}

func TestSavedSeconds(t *testing.T) {
	if s := savedSeconds(10e6, 10e6, 5e6); s != 0.5 {
		t.Fatalf("ausgelastete Hauptleitung + halb so viel dazu = 0,5 s gespart, bekam %v", s)
	}
	if s := savedSeconds(1e6, 10e6, 5e6); s != 0 {
		t.Fatalf("Hauptleitung nicht ausgelastet: nichts gespart, bekam %v", s)
	}
	if s := savedSeconds(10e6, 10e6, 0); s != 0 {
		t.Fatalf("ohne zweite Leitung nichts gespart, bekam %v", s)
	}
}

// Bekannte Programme mit mehreren Dateien sind ein Eintrag; bekannte ohne
// Verkehr stehen trotzdem in der Auswahl (am Ende).
func TestAppsGroupsKnownPrograms(t *testing.T) {
	b, _, _ := testBalancer(t, config.Default())
	b.appStat("battle.net.exe").bytes.Add(100)
	b.appStat("agent.exe").bytes.Add(900)
	b.appStat("agent.exe").phone.Add(400)
	b.appStat("discord.exe").bytes.Add(50)
	apps := b.Apps()
	if apps[0].Name != "battle.net.exe" || apps[0].Label != "Battle.net" || apps[0].Bytes != 1000 || apps[0].Phone != 400 {
		t.Fatalf("Battle.net nicht zusammengefasst: %+v", apps[0])
	}
	var names []string
	for _, a := range apps {
		if a.Name == "agent.exe" {
			t.Fatal("agent.exe darf nicht einzeln erscheinen")
		}
		names = append(names, a.Name)
	}
	if names[1] != "discord.exe" || names[2] != config.AppDownloads || names[3] != "steam.exe" {
		t.Fatalf("Reihenfolge falsch: %v", names)
	}
}
