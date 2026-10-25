// Package netif findet die echten Netzwerkadapter und baut Verbindungen
// auf, die fest an einen bestimmten Adapter gebunden sind.
//
// WlanMerge bündelt nur IPv4. IPv6 wird bewusst nicht benutzt: Viele Router
// richten IPv6 eigenwillig ein (z. B. Adressen, die nicht ins Internet führen),
// und alle Download-Dienste funktionieren genauso gut über IPv4.
//
// Je Betriebssystem gibt es eine eigene Datei (netif_windows.go,
// netif_linux.go, netif_darwin.go) mit List, bindControl und Counters.
package netif

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
)

// Art des Adapters, nur für die Anzeige.
const (
	KindWLAN     = "WLAN"
	KindEthernet = "LAN"
	KindPhone    = "Handy (USB)"
	KindHotspot  = "Handy (WLAN)" // zweiter WLAN-Adapter am Handy-Hotspot
	KindMetered  = "Handy (LAN)"  // z. B. LTE-Router per Kabel, vom System als getaktet geführt
	KindVirtual  = "Virtuell"
)

// Mobile: Verbindung übers Handy bzw. Mobilfunk – Datenvolumen zählt, Standard-
// Limit gilt, und sie wird nie die Hauptleitung.
func (a Adapter) Mobile() bool {
	switch a.Kind {
	case KindPhone, KindHotspot, KindMetered:
		return true
	}
	return false
}

type Adapter struct {
	Index       uint32 // Interface-Nummer des Systems (ifIndex)
	Name        string // Anzeigename, z. B. "WLAN", "Ethernet 2", "Wi-Fi", "wlp2s0"
	Dev         string // Gerätename des Systems (Windows: GUID, sonst z. B. "en0", "wlan0")
	Description string // z. B. "Intel(R) Wi-Fi 6 AX201"
	Kind        string
	IPv4        net.IP
	Gateway     net.IP
	LinkMbps    uint64
	Up          bool
}

// Usable: verbunden, hat eine IPv4-Adresse und einen Weg ins Internet (Gateway),
// und ist kein virtueller Adapter (VPN, Hamachi, unser eigener Tunnel).
func (a Adapter) Usable() bool {
	return a.Up && a.IPv4 != nil && a.Gateway != nil && a.Kind != KindVirtual
}

// List gibt alle Adapter zurück (auch nicht nutzbare), nutzbare zuerst.
func List() ([]Adapter, error) {
	out, err := list()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Kind == KindWLAN || out[i].Kind == KindEthernet {
			out[i].Kind = refineMobile(out[i])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Usable() && !out[j].Usable() })
	return out, nil
}

// Usable gibt nur die Adapter zurück, über die wir ins Internet können.
func Usable() ([]Adapter, error) {
	all, err := List()
	if err != nil {
		return nil, err
	}
	var out []Adapter
	for _, a := range all {
		if a.Usable() {
			out = append(out, a)
		}
	}
	return out, nil
}

// ByName sucht einen nutzbaren Adapter über seinen Namen (z. B. "WLAN").
func ByName(name string) (Adapter, error) {
	all, err := Usable()
	if err != nil {
		return Adapter{}, err
	}
	for _, a := range all {
		if strings.EqualFold(a.Name, name) {
			return a, nil
		}
	}
	return Adapter{}, errors.New("kein nutzbarer Adapter mit Namen " + name)
}

// iPhone-Hotspots verteilen immer Adressen aus 172.20.10.0/28.
var iphoneNet = net.IPNet{IP: net.IPv4(172, 20, 10, 0).To4(), Mask: net.CIDRMask(28, 32)}

// refineMobile: Ist dieses WLAN/LAN in Wahrheit ein Handy-Hotspot? Ja, wenn
// das System die Verbindung als getaktet führt oder es das iPhone-Netz ist.
func refineMobile(a Adapter) string {
	if !a.Up || a.Gateway == nil {
		return a.Kind
	}
	hotspot := a.IPv4 != nil && iphoneNet.Contains(a.IPv4)
	if !hotspot {
		hotspot = metered(a)
	}
	if !hotspot {
		return a.Kind
	}
	if a.Kind == KindWLAN {
		return KindHotspot
	}
	return KindMetered
}

// DialContext baut eine TCP-Verbindung (IPv4) über diesen Adapter auf.
// address ist "IP:Port" oder "Hostname:Port".
func (a Adapter) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d := &net.Dialer{Control: a.bindControl, LocalAddr: &net.TCPAddr{IP: a.IPv4}}
	return d.DialContext(ctx, "tcp4", address)
}

// ListenUDP öffnet einen UDP-Socket (IPv4), der über diesen Adapter sendet.
func (a Adapter) ListenUDP() (net.PacketConn, error) {
	lc := net.ListenConfig{Control: a.bindControl}
	return lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort(a.IPv4.String(), "0"))
}
