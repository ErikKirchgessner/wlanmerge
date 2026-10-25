package downloader

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Ausnahmeliste, die sich selbst aufbaut: Server, die mehrere Leitungen
// (= mehrere öffentliche IPs) nicht mögen, werden 7 Tage lang nur noch über
// eine Leitung bedient. So muss der Test nicht jedes Mal wiederholt werden.
const exceptionTTL = 7 * 24 * time.Hour

type exception struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}

type exceptions struct {
	mu   sync.Mutex
	path string
	m    map[string]exception
}

func loadExceptions(dir string) *exceptions {
	e := &exceptions{path: filepath.Join(dir, "dl-exceptions.json"), m: map[string]exception{}}
	if b, err := os.ReadFile(e.path); err == nil {
		json.Unmarshal(b, &e.m)
	}
	return e
}

func hostKey(h string) string { return strings.ToLower(strings.TrimSuffix(h, ".")) }

// Get liefert den Grund, falls der Server auf der Liste steht.
func (e *exceptions) Get(host string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	x, ok := e.m[hostKey(host)]
	if !ok || time.Now().After(x.Until) {
		return "", false
	}
	return x.Reason, true
}

func (e *exceptions) Add(host, reason string) {
	e.mu.Lock()
	e.m[hostKey(host)] = exception{Until: time.Now().Add(exceptionTTL), Reason: reason}
	now := time.Now()
	for k, x := range e.m { // Abgelaufene gleich mit entfernen
		if now.After(x.Until) {
			delete(e.m, k)
		}
	}
	b, _ := json.MarshalIndent(e.m, "", "  ")
	e.mu.Unlock()
	if e.path != "" {
		tmp := e.path + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			os.Rename(tmp, e.path)
		}
	}
}
