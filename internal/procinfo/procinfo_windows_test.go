package procinfo

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Eine eigene Verbindung muss diesem Testprogramm zugeordnet werden.
func TestLookupOwnTCPConnection(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	c, err := net.Dial("tcp4", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	src := c.LocalAddr().(*net.TCPAddr)
	dst := netip.MustParseAddrPort(l.Addr().String())
	exe, _ := os.Executable()
	want := strings.ToLower(filepath.Base(exe))
	if got := Lookup(false, uint16(src.Port), dst); got != want {
		t.Fatalf("erwartet %q, bekommen %q", want, got)
	}
	if got := Lookup(false, 1, dst); got != "" {
		t.Fatalf("unbekannte Verbindung muss leer liefern, bekam %q", got)
	}
}

func TestLookupOwnUDPSocket(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	exe, _ := os.Executable()
	want := strings.ToLower(filepath.Base(exe))
	port := uint16(pc.LocalAddr().(*net.UDPAddr).Port)
	if got := Lookup(true, port, netip.AddrPort{}); got != want {
		t.Fatalf("erwartet %q, bekommen %q", want, got)
	}
}

// Wie teuer ist ein Nachschlagen? (Nur zur Information.)
func TestLookupCost(t *testing.T) {
	dst := netip.MustParseAddrPort("203.0.113.1:443")
	start := time.Now()
	const n = 200
	for i := 0; i < n; i++ {
		mu.Lock()
		refreshTCP()
		mu.Unlock()
		Lookup(false, uint16(40000+i), dst)
	}
	t.Logf("eine frische Tabellen-Abfrage: %v, Einträge: %d", time.Since(start)/n, len(tcp))
}
