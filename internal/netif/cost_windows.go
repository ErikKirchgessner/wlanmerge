package netif

// Erkennt "getaktete" Verbindungen (Datenvolumen kostet), so wie Windows sie
// kennt. Handy-Hotspots melden sich bei Windows selbst als getaktet (Android
// über DHCP "ANDROID_METERED", iPhone ebenso); man kann es auch in den
// Windows-WLAN-Einstellungen von Hand einschalten. So erkennt WlanMerge einen
// Hotspot über einen zweiten WLAN-Adapter als "Handy".
//
// Technik: Network List Manager (COM) → alle Verbindungen → Adapter-GUID und
// INetworkConnectionCost::GetCost.

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidNetworkListManager  = windows.GUID{Data1: 0xDCB00C01, Data2: 0x570F, Data3: 0x4A9B, Data4: [8]byte{0x8D, 0x69, 0x19, 0x9F, 0xDB, 0xA5, 0x72, 0x3B}}
	iidINetworkListManager   = windows.GUID{Data1: 0xDCB00000, Data2: 0x570F, Data3: 0x4A9B, Data4: [8]byte{0x8D, 0x69, 0x19, 0x9F, 0xDB, 0xA5, 0x72, 0x3B}}
	iidINetworkConnectionCst = windows.GUID{Data1: 0xDCB0000A, Data2: 0x570F, Data3: 0x4A9B, Data4: [8]byte{0x8D, 0x69, 0x19, 0x9F, 0xDB, 0xA5, 0x72, 0x3B}}
)

const (
	clsctxAll         = 0x17
	nlmCostFixed      = 0x2
	nlmCostVariable   = 0x4
	meteredCacheValid = 15 * time.Second
)

var (
	meteredMu   sync.Mutex
	meteredAt   time.Time
	meteredByID map[windows.GUID]bool
)

// meteredAdapters liefert für jede Adapter-GUID, ob Windows sie als getaktet
// führt. Ergebnis wird 15 s zwischengespeichert (List wird oft aufgerufen).
func meteredAdapters() map[windows.GUID]bool {
	meteredMu.Lock()
	defer meteredMu.Unlock()
	if meteredByID != nil && time.Since(meteredAt) < meteredCacheValid {
		return meteredByID
	}
	m, err := queryMetered()
	if err != nil {
		m = map[windows.GUID]bool{} // im Zweifel: nichts ist getaktet
	}
	meteredByID, meteredAt = m, time.Now()
	return m
}

// comObj ist ein COM-Objekt: der erste Eintrag zeigt auf die Methodentabelle.
type comObj struct{ vtbl *[16]uintptr }

func (o *comObj) call(i int, args ...uintptr) uintptr {
	hr, _, _ := syscall.SyscallN(o.vtbl[i], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return hr
}

func (o *comObj) release() {
	if o != nil {
		o.call(2)
	}
}

func queryMetered() (map[windows.GUID]bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err == nil {
		defer windows.CoUninitialize()
	} // sonst ist COM in diesem Thread schon eingerichtet – auch gut

	var nlm *comObj
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidNetworkListManager)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidINetworkListManager)), uintptr(unsafe.Pointer(&nlm)))
	if hr != 0 || nlm == nil {
		return nil, syscall.Errno(hr)
	}
	defer nlm.release()

	// INetworkListManager::GetNetworkConnections (IDispatch hat 7 Methoden davor → 9)
	var enum *comObj
	if hr := nlm.call(9, uintptr(unsafe.Pointer(&enum))); hr != 0 || enum == nil {
		return nil, syscall.Errno(hr)
	}
	defer enum.release()

	out := map[windows.GUID]bool{}
	for {
		// IEnumNetworkConnections::Next(1, &conn, &fetched) → Index 8
		var conn *comObj
		var fetched uint32
		if hr := enum.call(8, 1, uintptr(unsafe.Pointer(&conn)), uintptr(unsafe.Pointer(&fetched))); hr != 0 || fetched == 0 || conn == nil {
			break
		}
		// INetworkConnection::GetAdapterId → Index 12
		var id windows.GUID
		if conn.call(12, uintptr(unsafe.Pointer(&id))) == 0 {
			// QueryInterface(INetworkConnectionCost) → GetCost (Index 3)
			var cost *comObj
			if conn.call(0, uintptr(unsafe.Pointer(&iidINetworkConnectionCst)), uintptr(unsafe.Pointer(&cost))) == 0 && cost != nil {
				var c uint32
				if cost.call(3, uintptr(unsafe.Pointer(&c))) == 0 {
					out[id] = c&(nlmCostFixed|nlmCostVariable) != 0
				}
				cost.release()
			}
		}
		conn.release()
	}
	return out, nil
}
