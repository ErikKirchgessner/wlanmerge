package balancer

import (
	"net"
	"net/netip"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"golang.org/x/net/dns/dnsmessage"

	"wlanmerge/internal/config"
)

// fakeConn registriert eine erfundene Verbindung auf einer Leitung.
func fakeConn(l *Link, dst string, age time.Duration) *countedConn {
	a, _ := net.Pipe()
	cc := &countedConn{Conn: a, l: l, dst: netip.MustParseAddr(dst), born: time.Now().Add(-age)}
	l.activeTCP.Add(1)
	l.conns.Store(cc, struct{}{})
	return cc
}

func isOpen(l *Link, cc *countedConn) bool { _, ok := l.conns.Load(cc); return ok }

func TestRebalanceMovesDownloadToIdleLink(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default()) // beide gleich schnell → je 50 %
	// Steam-artig: 4 Verbindungen zum selben Server, alle auf dem WLAN
	var cs []*countedConn
	for i := 0; i < 4; i++ {
		cs = append(cs, fakeConn(wlan, "203.0.113.7", time.Minute))
	}
	b.rebalance() // Startwerte
	for _, c := range cs {
		c.rx.Add(2_000_000) // je ~1 MB/s
	}
	b.reb.last = time.Now().Add(-2 * time.Second)
	b.rebalance()
	closed := 0
	for _, c := range cs {
		if !isOpen(wlan, c) {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("genau 1 Verbindung sollte umverteilt werden, waren %d", closed)
	}
	if first(b, meta, false) != phone {
		t.Fatal("die neue Verbindung muss auf die freie Leitung")
	}
}

func TestRebalanceNeverTouchesSingleDownload(t *testing.T) {
	b, wlan, _ := testBalancer(t, config.Default())
	c := fakeConn(wlan, "203.0.113.8", time.Minute) // Browser-Download: nur 1 Verbindung
	b.rebalance()
	c.rx.Add(20_000_000)
	b.reb.last = time.Now().Add(-2 * time.Second)
	b.rebalance()
	if !isOpen(wlan, c) {
		t.Fatal("einzelne Verbindungen dürfen nie geschlossen werden")
	}
}

func TestStalledConnectionIsClosed(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default())
	busy1 := fakeConn(phone, "203.0.113.9", time.Minute)
	busy2 := fakeConn(phone, "203.0.113.9", time.Minute)
	stuck := fakeConn(wlan, "203.0.113.9", time.Minute) // hängt (z. B. kaputtes IPv6)
	b.rebalance()
	b.reb.lastActive[stuck] = time.Now().Add(-time.Minute)
	busy1.rx.Add(1_000_000)
	busy2.rx.Add(1_000_000)
	b.reb.last = time.Now().Add(-2 * time.Second)
	b.rebalance()
	if isOpen(wlan, stuck) {
		t.Fatal("hängende Verbindung hätte geschlossen werden müssen")
	}
	if !isOpen(phone, busy1) || !isOpen(phone, busy2) {
		t.Fatal("laufende Verbindungen dürfen nicht geschlossen werden")
	}
}

func TestQUICIsDistributedButGamesStay(t *testing.T) {
	b, wlan, phone := testBalancer(t, config.Default())
	wlan.activeTCP.Store(20)
	quic := &M.Metadata{Network: M.UDP, DstIP: netip.MustParseAddr("8.8.4.4"), DstPort: 443}
	if c, pinned := b.candidates(quic, true); pinned || c[0] != phone {
		t.Fatal("QUIC (UDP 443) soll verteilt werden")
	}
	game := &M.Metadata{Network: M.UDP, DstIP: netip.MustParseAddr("8.8.4.4"), DstPort: 3074}
	if first(b, game, true) != wlan {
		t.Fatal("Spiele-UDP muss auf der Hauptleitung bleiben")
	}
}

func TestSharedCDNAddressNotPinned(t *testing.T) {
	cfg := config.Default() // enthält store.steampowered.com → haupt
	b, wlan, phone := testBalancer(t, cfg)
	wlan.activeTCP.Store(20)
	ip := netip.MustParseAddr("23.1.2.3")
	m := &M.Metadata{Network: M.TCP, DstIP: ip, DstPort: 443}
	b.dns.Learn(dnsAnswer(t, "store.steampowered.com", ip))
	if c, pinned := b.candidates(m, false); !pinned || c[0] != wlan {
		t.Fatal("reine Shop-IP soll auf der Hauptleitung bleiben")
	}
	b.dns.Learn(dnsAnswer(t, "cdn.steamcontent.example", ip)) // dieselbe CDN-IP liefert auch Downloads
	if c, pinned := b.candidates(m, false); pinned || c[0] != phone {
		t.Fatal("geteilte CDN-IP darf Downloads nicht festhalten")
	}
}

// dnsAnswer baut eine DNS-Antwort "name → ip", wie sie durch den Tunnel liefe.
func dnsAnswer(t *testing.T, name string, ip netip.Addr) []byte {
	n := dnsmessage.MustNewName(name + ".")
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: n, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	b.StartAnswers()
	b.AResource(dnsmessage.ResourceHeader{Name: n, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.AResource{A: ip.As4()})
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}
