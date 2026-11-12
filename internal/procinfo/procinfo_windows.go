// Package procinfo findet heraus, welches Programm eine Verbindung aufgebaut hat.
//
// WlanMerge sieht von einer neuen Verbindung nur Absender-Port und Ziel. Windows
// führt aber eine Tabelle "Verbindung → Programm" (dieselbe, die der
// Ressourcenmonitor zeigt). Dort schlagen wir nach und merken uns das Ergebnis
// kurz, damit viele Verbindungen auf einmal nur eine Abfrage kosten.
package procinfo

import (
	"encoding/binary"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	tcpTableOwnerPidAll = 5
	udpTableOwnerPid    = 1
	afInet              = 2

	minRefreshGap = 5 * time.Millisecond   // nicht öfter nachfragen (viele Verbindungen auf einmal)
	tcpHitMaxAge  = 2 * time.Second        // TCP: Port+Ziel ist eindeutig, Treffer dürfen etwas älter sein
	udpHitMaxAge  = 250 * time.Millisecond // UDP: nur der Port – Ports werden schneller neu vergeben
	nameTTL       = 30 * time.Second
)

type tcpKey struct {
	lport, rport uint16
	raddr        [4]byte
}

type nameEntry struct {
	name string
	at   time.Time
}

var (
	mu     sync.Mutex
	buf    []byte
	tcp    map[tcpKey]uint32
	tcpAt  time.Time
	udp    map[uint16]uint32
	udpAt  time.Time
	names  = map[uint32]nameEntry{}
	nameMu sync.Mutex
)

// Lookup liefert den Dateinamen des Programms (klein, z. B. "steam.exe"), dem
// die Verbindung mit diesem Absender-Port und Ziel gehört – oder "".
func Lookup(isUDP bool, srcPort uint16, dst netip.AddrPort) string {
	pid := lookupPID(isUDP, srcPort, dst)
	if pid == 0 {
		return ""
	}
	return nameOf(pid)
}

func lookupPID(isUDP bool, srcPort uint16, dst netip.AddrPort) uint32 {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	if isUDP {
		if pid, ok := udp[srcPort]; ok && now.Sub(udpAt) < udpHitMaxAge {
			return pid
		}
		if now.Sub(udpAt) >= minRefreshGap {
			refreshUDP()
		}
		return udp[srcPort]
	}
	k := tcpKey{lport: srcPort, rport: dst.Port(), raddr: dst.Addr().Unmap().As4()}
	if pid, ok := tcp[k]; ok && now.Sub(tcpAt) < tcpHitMaxAge {
		return pid
	}
	if now.Sub(tcpAt) >= minRefreshGap {
		refreshTCP()
	}
	return tcp[k]
}

// table ruft eine der beiden Windows-Tabellen ab (Puffer wird wiederverwendet).
func table(proc *windows.LazyProc, class uintptr) []byte {
	size := uint32(len(buf))
	for i := 0; i < 4; i++ {
		var p unsafe.Pointer
		if len(buf) > 0 {
			p = unsafe.Pointer(&buf[0])
		}
		r, _, _ := proc.Call(uintptr(p), uintptr(unsafe.Pointer(&size)), 0, afInet, class, 0)
		if r == 0 {
			return buf[:size]
		}
		if r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
			return nil
		}
		buf = make([]byte, size+4096)
		size = uint32(len(buf))
	}
	return nil
}

// ntohs: Windows liefert Ports in Netzwerk-Reihenfolge im unteren Teil eines DWORD.
func port(b []byte) uint16 { return binary.BigEndian.Uint16(b[:2]) }

func refreshTCP() {
	tcpAt = time.Now()
	t := table(procGetExtendedTcpTable, tcpTableOwnerPidAll)
	if len(t) < 4 {
		return
	}
	n := int(binary.LittleEndian.Uint32(t))
	m := make(map[tcpKey]uint32, n)
	const row = 24 // state, localAddr, localPort, remoteAddr, remotePort, pid
	for i := 0; i < n && 4+(i+1)*row <= len(t); i++ {
		r := t[4+i*row:]
		k := tcpKey{lport: port(r[8:]), rport: port(r[16:])}
		copy(k.raddr[:], r[12:16])
		m[k] = binary.LittleEndian.Uint32(r[20:])
	}
	tcp = m
}

func refreshUDP() {
	udpAt = time.Now()
	t := table(procGetExtendedUdpTable, udpTableOwnerPid)
	if len(t) < 4 {
		return
	}
	n := int(binary.LittleEndian.Uint32(t))
	m := make(map[uint16]uint32, n)
	const row = 12 // localAddr, localPort, pid
	for i := 0; i < n && 4+(i+1)*row <= len(t); i++ {
		r := t[4+i*row:]
		m[port(r[4:])] = binary.LittleEndian.Uint32(r[8:])
	}
	udp = m
}

// nameOf liefert den Dateinamen zu einer Prozessnummer (30 s gemerkt).
func nameOf(pid uint32) string {
	if pid == 4 {
		return "system" // Windows selbst
	}
	nameMu.Lock()
	defer nameMu.Unlock()
	now := time.Now()
	if e, ok := names[pid]; ok && now.Sub(e.at) < nameTTL {
		return e.name
	}
	if len(names) > 512 { // nicht endlos wachsen
		for k, e := range names {
			if now.Sub(e.at) >= nameTTL {
				delete(names, k)
			}
		}
	}
	name := ""
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		b := make([]uint16, windows.MAX_PATH)
		size := uint32(len(b))
		if windows.QueryFullProcessImageName(h, 0, &b[0], &size) == nil {
			name = strings.ToLower(filepath.Base(windows.UTF16ToString(b[:size])))
		}
		windows.CloseHandle(h)
	}
	names[pid] = nameEntry{name, now}
	return name
}
