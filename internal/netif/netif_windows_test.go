package netif

import (
	"net"
	"testing"
)

// Die Windows-Abfrage "getaktet?" muss ohne Fehler durchlaufen (echtes System).
func TestQueryMetered(t *testing.T) {
	m, err := queryMetered()
	if err != nil {
		t.Fatalf("Network List Manager: %v", err)
	}
	t.Logf("%d Verbindungen, getaktet: %v", len(m), m)
	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range all {
		t.Logf("%-30s %-18s mobil=%v usable=%v", a.Name, a.Kind, a.Mobile(), a.Usable())
	}
}

func TestRefineMobile(t *testing.T) {
	gw := net.IPv4(172, 20, 10, 1).To4()
	iphone := Adapter{Kind: KindWLAN, Up: true, IPv4: net.IPv4(172, 20, 10, 3).To4(), Gateway: gw}
	if k := refineMobile(iphone); k != KindHotspot {
		t.Fatalf("iPhone-Hotspot nicht erkannt: %s", k)
	}
	home := Adapter{Kind: KindWLAN, Dev: "{00000000-0000-0000-0000-000000000000}", Up: true, IPv4: net.IPv4(192, 168, 178, 20).To4(), Gateway: net.IPv4(192, 168, 178, 1).To4()}
	if k := refineMobile(home); k != KindWLAN {
		t.Fatalf("Heim-WLAN fälschlich als Handy: %s", k)
	}
	if !(Adapter{Kind: KindPhone}).Mobile() || (Adapter{Kind: KindWLAN}).Mobile() {
		t.Fatal("Mobile() falsch")
	}
}
