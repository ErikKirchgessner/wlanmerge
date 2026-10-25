package engine

import (
	"net"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"wlanmerge/internal/i18n"
	"wlanmerge/internal/wintundll"
)

const openHint = "virtuellen Adapter erstellen (liegt wintun.dll neben dem Programm?): %w"

type platState struct{ luid winipcfg.LUID }

// prepare legt den Wintun-Treiber (wintun.dll) neben das Programm.
func prepare() error {
	if err := wintundll.Ensure(); err != nil {
		return i18n.Errorf("Wintun-Treiber konnte nicht abgelegt werden: %w", err)
	}
	return nil
}

func (e *Engine) configure(iface *net.Interface) (err error) {
	if e.plat.luid, err = winipcfg.LUIDFromIndex(uint32(iface.Index)); err != nil {
		return err
	}
	for _, fam := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		if row, err := e.plat.luid.IPInterface(fam); err == nil {
			row.NLMTU = mtu // Windows muss dieselbe Paketgröße benutzen wie unser Stack
			row.Set()
		}
	}
	if err = e.plat.luid.SetIPAddresses(tunAddrs); err != nil {
		return i18n.Errorf("IP setzen: %w", err)
	}
	for _, r := range tunRoutes {
		hop := netip.IPv4Unspecified()
		if r.Addr().Is6() {
			hop = netip.IPv6Unspecified()
		}
		if err = e.plat.luid.AddRoute(r, hop, 0); err != nil {
			return i18n.Errorf("Route %s: %w", r, err)
		}
	}
	return nil
}

func (e *Engine) teardown() {
	if e.plat.luid != 0 {
		e.plat.luid.FlushRoutes(windows.AF_INET)
		e.plat.luid.FlushRoutes(windows.AF_INET6)
	}
}
