package i18n

import "golang.org/x/sys/windows"

var procGetUserDefaultUILanguage = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

// Detect liefert die Sprache der Windows-Oberfläche ("de" oder "en").
func Detect() string {
	id, _, _ := procGetUserDefaultUILanguage.Call()
	if id&0x3ff == 0x07 { // LANG_GERMAN (Deutschland, Österreich, Schweiz …)
		return "de"
	}
	return "en"
}
