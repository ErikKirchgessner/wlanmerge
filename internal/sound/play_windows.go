package sound

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procPlaySound = windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")

const (
	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndMemory    = 0x0004
)

func play(wait bool) {
	w := theWAV()
	mode := uintptr(sndAsync)
	if wait {
		mode = 0
	}
	procPlaySound.Call(uintptr(unsafe.Pointer(&w[0])), 0, sndMemory|sndNoDefault|mode)
}
