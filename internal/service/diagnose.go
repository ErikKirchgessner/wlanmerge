package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"wlanmerge/internal/balancer"
	"wlanmerge/internal/config"
)

var (
	ipv4Re = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	userRe = regexp.MustCompile(`(?i)([A-Z]:\\Users\\)[^\\\s"']+`)
)

// scrub entfernt IP-Adressen und Benutzernamen aus einem Text.
func scrub(s string) string {
	return userRe.ReplaceAllString(ipv4Re.ReplaceAllString(s, "x.x.x.x"), "${1}…")
}

// Diagnose liefert einen kurzen technischen Bericht zum Kopieren (für
// Fehlermeldungen). Bewusst ohne IP-Adressen, Benutzernamen und Programmliste.
func (s *Service) Diagnose() string {
	st := s.Snapshot()
	cfg := s.Config()
	var b strings.Builder
	major, minor, build := windows.RtlGetNtVersionNumbers()
	fmt.Fprintf(&b, "WlanMerge %s · Windows %d.%d.%d · %s · %s\n", st.Version, major, minor, build, runtime.GOARCH, time.Now().Format("2006-01-02 15:04"))

	state := "off"
	switch {
	case st.Busy:
		state = "starting"
	case st.On:
		state = "on"
	case st.Paused:
		state = "paused (waiting for a second line)"
	}
	fmt.Fprintf(&b, "state: %s", state)
	if st.Error != "" {
		fmt.Fprintf(&b, " · error: %s", scrub(st.Error))
	}
	fmt.Fprintf(&b, "\nsettings: autoConnect=%v autoPause=%v rebalance=%v downloads=%v appFilter=%v (%d apps) resetDay=%d lang=%s\n",
		cfg.AutoConnect, cfg.AutoPause, cfg.Rebalance, cfg.DLEnabled, cfg.AppFilter, len(cfg.PhoneApps), cfg.ResetDay, st.Lang)

	fmt.Fprintf(&b, "\nlines (%d):\n", len(st.Links))
	for _, l := range st.Links {
		limit := "no limit"
		if l.Limit > 0 {
			limit = balancer.HumanBytes(l.Limit)
		}
		main := ""
		if l.Primary {
			main = " main"
		}
		fmt.Fprintf(&b, "- %s [%s%s] %s · %s/s of ~%s/s · ping %d ms · %d conns · used %s / %s\n",
			l.Name, l.Kind, main, l.State, balancer.HumanBytes(l.RxRate+l.TxRate), balancer.HumanBytes(l.Capacity),
			l.RTTms, l.Conns, balancer.HumanBytes(l.Used), limit)
	}

	fmt.Fprintf(&b, "\ntoday: main %s, extra %s · 30 days: main %s, extra %s\n",
		balancer.HumanBytes(st.Stats.Today.Main), balancer.HumanBytes(st.Stats.Today.Extra),
		balancer.HumanBytes(st.Stats.Month.Main), balancer.HumanBytes(st.Stats.Month.Extra))

	if n := len(st.Events); n > 0 {
		b.WriteString("\nevents:\n")
		for _, e := range st.Events[max(0, n-15):] {
			fmt.Fprintf(&b, "- %s %s\n", e.Time.Format("15:04:05"), scrub(e.Text))
		}
	}
	if tail := logTail(20); tail != "" {
		b.WriteString("\nlog:\n" + scrub(tail))
	}
	return b.String()
}

// logTail liefert die letzten n Zeilen von wlanmerge.log.
func logTail(n int) string {
	raw, err := os.ReadFile(filepath.Join(config.Dir(), "wlanmerge.log"))
	if err != nil {
		return ""
	}
	if len(raw) > 16<<10 {
		raw = raw[len(raw)-16<<10:]
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n") + "\n"
}
