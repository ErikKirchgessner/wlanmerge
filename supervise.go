package main

// Zuverlässiger Autostart.
//
// Direkt nach der Anmeldung ist die Taskleiste oft noch nicht fertig.
// Startet WlanMerge zu früh, scheitert das Fenster oder das Tray-Symbol – und
// man sieht gar nichts. Darum startet "--tray" nur einen kleinen Wächter: Er
// wartet auf die Taskleiste (waitForTaskbar, je Betriebssystem), startet die
// eigentliche App und startet sie neu, falls sie mit einem Fehler endet. "Beenden" im Tray endet normal
// (Code 0) – dann hört auch der Wächter auf.

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"wlanmerge/internal/config"
)

const maxLog = 1 << 20 // 1 MB, danach von vorn

// openLog schreibt Fehlermeldungen in wlanmerge.log im Datenordner.
func openLog() *os.File {
	dir := config.Dir()
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "wlanmerge.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxLog {
		os.Remove(p)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags)
	return f
}

// supervise startet die App mit "--tray-run" und startet sie bei Fehlern neu.
func supervise(logf *os.File) {
	waitForTaskbar()
	exe, err := os.Executable()
	if err != nil {
		log.Print("Autostart: ", err)
		return
	}
	fails := 0
	for {
		cmd := exec.Command(exe, "--tray-run")
		if logf != nil {
			cmd.Stdout, cmd.Stderr = logf, logf // auch Abstürze landen im Protokoll
		}
		began := time.Now()
		err := cmd.Run()
		if err == nil {
			return // normal beendet
		}
		log.Printf("Autostart: App endete mit Fehler (%v) nach %s", err, time.Since(began).Round(time.Second))
		if time.Since(began) > 5*time.Minute {
			fails = 0 // lief lange gut – Zähler zurücksetzen
		}
		fails++
		if fails > 6 {
			log.Print("Autostart: zu viele Fehler hintereinander, gebe auf")
			return
		}
		time.Sleep(time.Duration(fails*5) * time.Second)
	}
}
