// Package engine startet den virtuellen Adapter (Windows: Wintun, macOS: utun,
// Linux: tun), fängt dort mit dem Netzwerk-Stack von tun2socks (gVisor) jede
// neue Verbindung ab und übergibt sie einem Dialer, der sie über einen echten
// Adapter neu aufbaut (siehe relay.go).
//
// Die Einrichtung des Adapters (Adressen, Routen) steht je Betriebssystem in
// engine_windows.go, engine_linux.go und engine_darwin.go.
package engine

import (
	"context"
	"net"
	"net/netip"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"
	"github.com/xjasonlyu/tun2socks/v2/log"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"

	"wlanmerge/internal/i18n"
	"wlanmerge/internal/netif"
	"wlanmerge/internal/safety"
	_ "wlanmerge/internal/tuning" // Laufzeit-Einstellungen für Durchsatz
)

// Dialer baut für jede abgefangene Verbindung die echte Verbindung auf
// (z. B. der Balancer).
type Dialer interface {
	DialContext(ctx context.Context, m *M.Metadata) (net.Conn, error)
	DialUDP(m *M.Metadata) (net.PacketConn, error)
}

// Adressen unseres virtuellen Adapters (198.18.0.0/15 ist für Tests reserviert
// und kollidiert nicht mit Heimnetzen; fd57:: ist ein privates IPv6-Netz).
var (
	tunAddrs = []netip.Prefix{
		netip.MustParsePrefix("198.18.0.1/24"),
		netip.MustParsePrefix("fd57:4d00::1/64"),
	}
	// Zwei halbe Internet-Routen sind genauer als die Standardroute (0.0.0.0/0)
	// und gewinnen deshalb – ohne dass wir die Original-Routen anfassen müssen.
	tunRoutes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/1"),
		netip.MustParsePrefix("128.0.0.0/1"),
		netip.MustParsePrefix("::/1"),
		netip.MustParsePrefix("8000::/1"),
	}
)

// MTU des virtuellen Adapters. Die Pakete verlassen den PC nie (wir bauen jede
// Verbindung über die echte Leitung neu auf), deshalb dürfen sie groß sein:
// 9000 statt 1500 → ~6× weniger Pakete durch den Netzwerk-Stack. Gemessen:
// CPU-Zeit pro MB etwa halbiert, Geschwindigkeit unverändert.
const mtu uint32 = 9000

type Engine struct {
	dev   device.Device
	stack *stack.Stack
	name  string // tatsächlicher Name des Adapters (macOS: utun3 …)
	plat  platState
}

// Start schaltet alles ein. Schlägt irgendein Schritt fehl, wird automatisch
// alles zurückgesetzt.
func Start(label string, d Dialer) (_ *Engine, err error) {
	log.SetLogger(log.Must(log.NewLeveled(log.ErrorLevel)))

	if err := safety.MarkActive(netif.TunName); err != nil {
		return nil, i18n.Errorf("Statusdatei: %w", err)
	}
	// Eigene Variable statt Rückgabewert: `return nil, err` darf das Objekt nicht
	// löschen, bevor wir aufgeräumt haben.
	e := &Engine{}
	defer func() {
		if err != nil {
			e.Stop()
		}
	}()

	if err = prepare(); err != nil {
		return nil, err
	}
	if e.dev, err = tun.Open(netif.TunName, mtu); err != nil {
		return nil, i18n.Errorf(openHint, err)
	}
	e.name = e.dev.Name()
	if e.name != netif.TunName {
		safety.MarkActive(e.name) // macOS vergibt die Nummer selbst
	}
	if e.stack, err = core.CreateStack(&core.Config{LinkEndpoint: e.dev, TransportHandler: &handler{d}}); err != nil {
		return nil, i18n.Errorf("Netzwerk-Stack: %w", err)
	}
	installTCP(e.stack, d) // "erst verbinden, dann annehmen"

	iface, err := waitForInterface(e.name, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if err = e.configure(iface); err != nil {
		return nil, err
	}
	return e, nil
}

// Stop schaltet alles aus und stellt den Originalzustand her.
func (e *Engine) Stop() {
	if e == nil {
		safety.Clear()
		return
	}
	e.teardown()
	if e.stack != nil {
		e.stack.Close()
	}
	if e.dev != nil {
		e.dev.Close()
	}
	safety.Clear()
}

func waitForInterface(name string, timeout time.Duration) (*net.Interface, error) {
	deadline := time.Now().Add(timeout)
	for {
		iface, err := net.InterfaceByName(name)
		if err == nil {
			return iface, nil
		}
		if time.Now().After(deadline) {
			return nil, i18n.Errorf("virtueller Adapter erscheint nicht: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
