package netif

import (
	"encoding/binary"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TunName ist der Name unseres eigenen virtuellen Adapters.
const TunName = "WlanMerge"

const (
	gaaFlagIncludeGateways = 0x80
	ifTypeLoopback         = 24
	ifTypeWLAN             = 71
	ifTypeTunnel           = 131
	ifOperStatusUp         = 1
)

var virtualHints = []string{"virtual", "hamachi", "vpn", "tap-", "wintun", "wireguard", "vmware", "hyper-v", "loopback", "tunnel", "zerotier", "tailscale"}
var phoneHints = []string{"ndis", "mobile", "iphone", "apple", "android", "tether"}

func list() ([]Adapter, error) {
	var buf []byte
	size := uint32(15000)
	var first *windows.IpAdapterAddresses
	for {
		buf = make([]byte, size)
		first = (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_INET, gaaFlagIncludeGateways, 0, first, &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil, err
		}
	}

	var out []Adapter
	for aa := first; aa != nil; aa = aa.Next {
		if aa.IfType == ifTypeLoopback {
			continue
		}
		a := Adapter{
			Index:       aa.IfIndex,
			Name:        windows.UTF16PtrToString(aa.FriendlyName),
			Dev:         windows.BytePtrToString(aa.AdapterName),
			Description: windows.UTF16PtrToString(aa.Description),
			Up:          aa.OperStatus == ifOperStatusUp,
			LinkMbps:    aa.ReceiveLinkSpeed / 1_000_000,
		}
		for u := aa.FirstUnicastAddress; u != nil; u = u.Next {
			if ip4 := u.Address.IP().To4(); ip4 != nil && !ip4.IsLinkLocalUnicast() {
				a.IPv4 = ip4
				break
			}
		}
		for g := aa.FirstGatewayAddress; g != nil; g = g.Next {
			if ip4 := g.Address.IP().To4(); ip4 != nil {
				a.Gateway = ip4
				break
			}
		}
		a.Kind = kindOf(aa.IfType, a.Name, a.Description)
		out = append(out, a)
	}
	return out, nil
}

func kindOf(ifType uint32, name, desc string) string {
	if name == TunName {
		return KindVirtual
	}
	d := strings.ToLower(desc)
	for _, h := range virtualHints {
		if strings.Contains(d, h) {
			return KindVirtual
		}
	}
	if ifType == ifTypeTunnel {
		return KindVirtual
	}
	if strings.Contains(d, "bluetooth") { // viel zu langsam (1–2 Mbit/s) – wird nicht genutzt
		return KindVirtual
	}
	for _, h := range phoneHints {
		if strings.Contains(d, h) {
			return KindPhone
		}
	}
	if ifType == ifTypeWLAN {
		return KindWLAN
	}
	return KindEthernet
}

// metered: führt Windows die Verbindung als "getaktet"? (Handy-Hotspots melden
// sich so, siehe cost_windows.go)
func metered(a Adapter) bool {
	id, err := windows.GUIDFromString(a.Dev)
	return err == nil && meteredAdapters()[id]
}

// ipUnicastIf ist die Windows-Socket-Option, die eine Verbindung zwingt, über
// genau dieses Interface rauszugehen – egal, was die Routing-Tabelle sagt.
// Das ist der Trick, der unsere eigenen Verbindungen am virtuellen Adapter
// vorbeischleust.
const ipUnicastIf = 31 // IPPROTO_IP, Interface-Nummer in Netzwerk-Byte-Reihenfolge

func (a Adapter) bindControl(network, address string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		idx := make([]byte, 4)
		binary.BigEndian.PutUint32(idx, a.Index)
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, ipUnicastIf, int(*(*uint32)(unsafe.Pointer(&idx[0]))))
	})
	if err != nil {
		return err
	}
	return serr
}
