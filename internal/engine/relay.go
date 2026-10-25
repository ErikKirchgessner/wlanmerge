package engine

import (
	"context"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

// Eigene, schlanke Vermittlung zwischen dem Netzwerk-Stack und den echten
// Leitungen (ersetzt tun2socks' "tunnel").
//
// Wichtigster Unterschied: Bei TCP bauen wir ZUERST die echte Verbindung auf
// und bestätigen dem Programm (Steam …) erst dann seinen Verbindungsaufbau.
// Klappt es nicht – z. B. IPv6, das wir nicht bündeln –, bekommt das Programm
// sofort ein sauberes "abgelehnt" und nimmt ohne Umweg IPv4 bzw. versucht es
// gleich neu. Nebenbei entfallen eine doppelte Byte-Zählung und eine
// Zwischenstation pro Verbindung.

const (
	dialTimeout      = 10 * time.Second // Programme versuchen selbst ~21 s lang
	halfCloseTimeout = 60 * time.Second
	udpTimeout       = 60 * time.Second
	dnsTimeout       = 10 * time.Second // DNS: eine Frage, eine Antwort – Socket schnell freigeben
	relayBufSize     = 32 << 10
	maxInFlight      = 2 << 10
	keepaliveIdle    = 60 * time.Second
	keepaliveIntvl   = 30 * time.Second
	keepaliveCount   = 9
)

var (
	relayBufs = sync.Pool{New: func() any { b := make([]byte, relayBufSize); return &b }}
	udpBufs   = sync.Pool{New: func() any { b := make([]byte, 65535); return &b }} // UDP-Pakete bis 64 KB
)

type handler struct{ d Dialer }

func toAddr(a tcpip.Address) netip.Addr {
	ip, _ := netip.AddrFromSlice(a.AsSlice())
	return ip.Unmap()
}

func meta(network M.Network, id stack.TransportEndpointID) *M.Metadata {
	return &M.Metadata{
		Network: network,
		SrcIP:   toAddr(id.RemoteAddress), SrcPort: id.RemotePort,
		DstIP: toAddr(id.LocalAddress), DstPort: id.LocalPort,
	}
}

// installTCP ersetzt die TCP-Annahme des Stacks durch "erst verbinden, dann annehmen".
func installTCP(s *stack.Stack, d Dialer) {
	f := tcp.NewForwarder(s, 0, maxInFlight, func(r *tcp.ForwarderRequest) {
		m := meta(M.TCP, r.ID())
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		remote, err := d.DialContext(ctx, m)
		cancel()
		if err != nil {
			r.Complete(true) // RST: Programm erfährt sofort, dass es nicht geht
			return
		}
		var wq waiter.Queue
		ep, terr := r.CreateEndpoint(&wq)
		if terr != nil {
			remote.Close()
			r.Complete(true)
			return
		}
		r.Complete(false)
		setTCPOptions(s, ep)
		relayTCP(gonet.NewTCPConn(&wq, ep), remote)
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, f.HandlePacket)
}

func setTCPOptions(s *stack.Stack, ep tcpip.Endpoint) {
	ep.SocketOptions().SetKeepAlive(true)
	idle := tcpip.KeepaliveIdleOption(keepaliveIdle)
	ep.SetSockOpt(&idle)
	intvl := tcpip.KeepaliveIntervalOption(keepaliveIntvl)
	ep.SetSockOpt(&intvl)
	ep.SetSockOptInt(tcpip.KeepaliveCountOption, keepaliveCount)
	var ss tcpip.TCPSendBufferSizeRangeOption
	if s.TransportProtocolOption(header.TCPProtocolNumber, &ss) == nil {
		ep.SocketOptions().SetSendBufferSize(int64(ss.Default), false)
	}
	var rs tcpip.TCPReceiveBufferSizeRangeOption
	if s.TransportProtocolOption(header.TCPProtocolNumber, &rs) == nil {
		ep.SocketOptions().SetReceiveBufferSize(int64(rs.Default), false)
	}
}

// HandleTCP wird nur in den ersten Millisekunden genutzt, bevor installTCP
// greift (dann gibt es noch keine Routen, also praktisch nie).
func (h *handler) HandleTCP(c adapter.TCPConn) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		remote, err := h.d.DialContext(ctx, meta(M.TCP, c.ID()))
		cancel()
		if err != nil {
			c.Close()
			return
		}
		relayTCP(c, remote)
	}()
}

// relayTCP kopiert in beide Richtungen, bis beide Seiten fertig sind.
func relayTCP(local, remote net.Conn) {
	defer local.Close()
	defer remote.Close()
	done := make(chan struct{})
	go func() {
		copyHalf(remote, local) // Programm → Internet
		close(done)
	}()
	copyHalf(local, remote) // Internet → Programm
	<-done
}

func copyHalf(dst, src net.Conn) {
	bp := relayBufs.Get().(*[]byte)
	io.CopyBuffer(dst, src, *bp)
	relayBufs.Put(bp)
	// Halb-Schließen weitergeben, dann der Gegenrichtung noch etwas Zeit lassen.
	if cr, ok := src.(interface{ CloseRead() error }); ok {
		cr.CloseRead()
	}
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
	dst.SetReadDeadline(time.Now().Add(halfCloseTimeout))
}

// HandleUDP wird im Paket-Pfad des Stacks aufgerufen und darf nicht blockieren.
func (h *handler) HandleUDP(c adapter.UDPConn) { go h.udp(c) }

func (h *handler) udp(local adapter.UDPConn) {
	defer local.Close()
	m := meta(M.UDP, local.ID())
	remote, err := h.d.DialUDP(m)
	if err != nil {
		return
	}
	defer remote.Close()
	timeout := udpTimeout
	if m.DstPort == 53 {
		timeout = dnsTimeout
	}
	dst := m.DestinationAddrPort()
	to := net.UDPAddrFromAddrPort(dst)

	done := make(chan struct{})
	go func() { // Programm → Internet
		defer close(done)
		bp := udpBufs.Get().(*[]byte)
		defer udpBufs.Put(bp)
		for {
			local.SetReadDeadline(time.Now().Add(timeout))
			n, err := local.Read(*bp)
			if err != nil {
				remote.SetReadDeadline(time.Now()) // Gegenrichtung beenden
				return
			}
			if _, err := remote.WriteTo((*bp)[:n], to); err != nil {
				return
			}
		}
	}()
	bp := udpBufs.Get().(*[]byte) // Internet → Programm
	defer udpBufs.Put(bp)
	buf := *bp
	for {
		remote.SetReadDeadline(time.Now().Add(timeout))
		n, from, err := remote.ReadFrom(buf)
		if err != nil {
			local.SetReadDeadline(time.Now())
			break
		}
		// Nur Antworten vom angefragten Ziel durchlassen (wie ein normaler Router).
		if ua, ok := from.(*net.UDPAddr); !ok || netip.AddrPortFrom(ua.AddrPort().Addr().Unmap(), ua.AddrPort().Port()) != dst {
			continue
		}
		if _, err := local.Write(buf[:n]); err != nil {
			break
		}
	}
	<-done
}
