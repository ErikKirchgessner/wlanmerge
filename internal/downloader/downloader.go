// Package downloader ist der Download-Manager von WlanMerge.
//
// Eine einzelne Verbindung kann ohne eigenen Server nicht auf zwei Leitungen
// laufen. Deshalb teilen wir nicht die Verbindung, sondern die DATEI: Viele
// Stücke werden gleichzeitig per HTTP-Range geholt, jedes in einer eigenen
// Verbindung, verteilt auf alle Leitungen. Eine schnellere Leitung holt
// automatisch mehr Stücke, weil ihre Arbeiter früher fertig sind.
//
// Automatische Kompatibilität (ohne feste Serverliste):
//   - Vor jedem Download: Teil-Anfrage "bytes=0-0" muss 206 + Content-Range liefern,
//     sonst normaler Download über eine Leitung bzw. zurück an den Browser.
//   - 403 auf einer zweiten Leitung: Stück erst über die erste Leitung wiederholen;
//     klappt es dort, steht der Server 7 Tage auf der Ausnahmeliste (nur eine Leitung).
//   - 429/503: Retry-After beachten, Verbindungen je Leitung verringern;
//     kommt es von einer zweiten Leitung, ebenfalls Ausnahmeliste.
//   - Nur HTTP/1.1 und IPv4, Weiterleitungen werden einmal aufgelöst.
package downloader

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	partExt        = ".wmpart"
	maxActive      = 3                // so viele Downloads laufen gleichzeitig, weitere warten
	maxRetries     = 4                // Versuche je Stück bei Netzwerkfehlern
	idleTimeout    = 15 * time.Second // ohne Daten: Stück abbrechen und neu
	probeTimeout   = 8 * time.Second
	endgameMinLeft = 512 << 10 // Endspurt nur, wenn noch mehr als 512 KB fehlen
	saveEvery      = 3 * time.Second
	writeBufSize   = 256 << 10
)

// Zustände eines Downloads.
const (
	StateProbe    = "prüfe"
	StateQueued   = "wartet"
	StateRunning  = "läuft"
	StatePaused   = "pausiert"
	StateDone     = "fertig"
	StateError    = "fehler"
	StateCanceled = "abgebrochen"
)

// Line ist eine Leitung, über die der Downloader Verbindungen aufbauen darf.
// Dial muss die Verbindung fest an diese Leitung binden (IPv4).
type Line struct {
	Name    string
	Primary bool
	Dial    func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Request beschreibt einen neuen Download (eingefügter Link oder vom Browser).
type Request struct {
	URL         string
	Referrer    string
	UserAgent   string
	Cookies     string // nur im Arbeitsspeicher, wird nie gespeichert
	Filename    string // Vorschlag des Browsers (wird bereinigt)
	FromBrowser bool
}

// Options steuern den Manager.
type Options struct {
	DataDir      string        // für downloads.json und dl-exceptions.json
	Dir          func() string // Zielordner
	PerLine      func() int    // Verbindungen je Leitung (zu Beginn)
	AutoTune     bool          // Verbindungen je Leitung automatisch erhöhen
	AllowPrivate bool          // nur Tests: lokale Adressen erlauben
	OnStream     func(Info)    // ein Browser-Download wurde übernommen
}

// Info ist der Zustand eines Downloads für die Anzeige.
type Info struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Path     string           `json:"path"`
	Size     int64            `json:"size"`
	Done     int64            `json:"done"`
	State    string           `json:"state"`
	Lines    int              `json:"lines"`   // wie viele Leitungen gerade benutzt werden
	Mode     string           `json:"mode"`    // "über 2 Leitungen" / "über 1 Leitung (…)"
	Split    bool             `json:"split"`   // wird aufgeteilt (mehrere Leitungen möglich)
	Speed    int64            `json:"speed"`   // Bytes/s
	PerLine  map[string]int64 `json:"perLine"` // Bytes/s je Leitung
	Error    string           `json:"error"`
	Resume   bool             `json:"resume"` // kann nach Pause weitermachen
	Created  time.Time        `json:"created"`
	Finished time.Time        `json:"finished"`
}

// Manager verwaltet alle Downloads.
type Manager struct {
	opt   Options
	lines func() []Line
	exc   *exceptions

	mu    sync.Mutex
	items []*Download
	base  string // lokale Adresse der Weitergabe an den Browser
}

func New(lines func() []Line, opt Options) *Manager {
	if opt.Dir == nil {
		opt.Dir = DefaultDir
	}
	if opt.PerLine == nil {
		opt.PerLine = func() int { return startPerLine }
	}
	m := &Manager{opt: opt, lines: lines, exc: loadExceptions(opt.DataDir)}
	m.load()
	go m.loop()
	return m
}

// loop rechnet jede Sekunde die Geschwindigkeiten der laufenden Downloads aus.
func (m *Manager) loop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		m.mu.Lock()
		items := append([]*Download(nil), m.items...)
		m.mu.Unlock()
		for _, d := range items {
			if d.getState() == StateRunning {
				d.tick()
			}
		}
		m.tidyStreams()
	}
}

// ---------- Öffentliche Schnittstelle ----------

// Add lädt einen Link direkt in eine Datei im Zielordner (Kern-Ablauf, den
// die Tests prüfen). Im Programm laufen Downloads über OfferStream.
func (m *Manager) Add(req Request) (Info, error) {
	d, err := m.prepare(req)
	if err != nil {
		return Info{}, err
	}
	m.enqueue(d)
	return d.info(), nil
}

func (m *Manager) List() []Info {
	m.mu.Lock()
	items := append([]*Download(nil), m.items...)
	m.mu.Unlock()
	out := make([]Info, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- { // neueste zuerst
		out = append(out, items[i].info())
	}
	return out
}

func (m *Manager) find(id string) *Download {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.items {
		if d.id == id {
			return d
		}
	}
	return nil
}

func (m *Manager) Pause(id string) {
	if d := m.find(id); d != nil {
		d.stop(StatePaused)
		m.save()
		m.kick()
	}
}

func (m *Manager) Resume(id string) {
	if d := m.find(id); d != nil {
		d.mu.Lock()
		ok := d.state == StatePaused || d.state == StateError
		if ok {
			d.state, d.err = StateQueued, ""
		}
		d.mu.Unlock()
		if ok {
			m.kick()
		}
	}
}

func (m *Manager) Cancel(id string) {
	if d := m.find(id); d != nil {
		d.stop(StateCanceled)
		os.Remove(d.partPath())
		m.save()
		m.kick()
	}
}

// Remove nimmt einen Download aus der Liste (fertige Dateien bleiben liegen).
func (m *Manager) Remove(id string) {
	d := m.find(id)
	if d == nil {
		return
	}
	if st := d.getState(); st != StateDone || d.stream {
		d.stop(StateCanceled)
		os.Remove(d.partPath())
	}
	m.mu.Lock()
	for i, x := range m.items {
		if x == d {
			m.items = append(m.items[:i], m.items[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	m.save()
	m.kick()
}

// Close hält alle laufenden Downloads an (beim Beenden) und speichert.
func (m *Manager) Close() {
	m.mu.Lock()
	items := append([]*Download(nil), m.items...)
	m.mu.Unlock()
	for _, d := range items {
		if s := d.getState(); s == StateRunning || s == StateProbe || s == StateQueued {
			d.stop(StatePaused)
		}
	}
	m.save()
}

// ---------- Vorbereitung ----------

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) prepare(req Request) (*Download, error) {
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("Nur http- und https-Links werden unterstützt")
	}
	req.URL = u.String()
	d := &Download{m: m, id: newID(), req: req, created: time.Now(), state: StateProbe}
	d.jar, _ = cookiejar.New(nil)
	if req.Cookies != "" {
		d.jar.SetCookies(u, parseCookies(req.Cookies))
	}
	lines := m.lines()
	if len(lines) == 0 {
		return nil, errors.New("Keine Internetverbindung")
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	pr, err := probe(ctx, d.client(lines[0], true), req.URL, d.headers())
	if err != nil {
		return nil, friendly(err)
	}
	d.apply(pr)
	name := pr.Name // Server-Vorgabe (Content-Disposition) oder Ziel-URL
	if req.Filename != "" {
		name = req.Filename // was der Browser ermittelt hat
	}
	if name == "" {
		name = nameFromURL(u)
	}
	dir := m.opt.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("Download-Ordner nicht verfügbar: %v", err)
	}
	d.path = uniquePath(dir, SanitizeName(name), m.pathTaken)
	return d, nil
}

func (m *Manager) pathTaken(p string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.items {
		if strings.EqualFold(d.path, p) && d.getState() != StateCanceled {
			return true
		}
	}
	return false
}

func (m *Manager) enqueue(d *Download) {
	d.mu.Lock()
	d.state = StateQueued
	d.mu.Unlock()
	m.mu.Lock()
	m.items = append(m.items, d)
	m.mu.Unlock()
	m.save()
	m.kick()
}

// kick startet wartende Downloads, solange Plätze frei sind.
func (m *Manager) kick() {
	m.mu.Lock()
	active := 0
	for _, d := range m.items {
		if s := d.getState(); s == StateRunning || s == StateProbe {
			active++
		}
	}
	var start []*Download
	for _, d := range m.items {
		if active >= maxActive {
			break
		}
		if d.getState() == StateQueued {
			d.setState(StateRunning)
			start = append(start, d)
			active++
		}
	}
	m.mu.Unlock()
	for _, d := range start {
		d.start()
	}
}

// friendly macht aus technischen Fehlern verständliche Sätze.
func friendly(err error) error {
	var se *statusError
	if errors.As(err, &se) {
		return se
	}
	if errors.Is(err, errPrivate) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("Server antwortet nicht")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return errors.New("Server nicht gefunden")
	}
	return fmt.Errorf("Verbindung fehlgeschlagen: %v", err)
}

func parseCookies(s string) []*http.Cookie {
	var out []*http.Cookie
	for _, part := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k != "" {
			out = append(out, &http.Cookie{Name: k, Value: v})
		}
	}
	return out
}

// ---------- Verbindungen ----------

var errPrivate = errors.New("Lokale Adressen werden nicht geladen")

// dialer bindet an die Leitung und verhindert Zugriffe auf interne Adressen
// (z. B. eine Weiterleitung auf den Router oder localhost).
func (m *Manager) dialer(l Line) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if m.opt.AllowPrivate {
			return l.Dial(ctx, "tcp4", addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
		if err != nil {
			return nil, err
		}
		var lastErr error = errPrivate
		for _, ip := range ips {
			if isPrivate(ip) {
				continue
			}
			c, err := l.Dial(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
			if err == nil {
				return c, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func isPrivate(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		ip.IsMulticast() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("198.18.0.0/15").Contains(ip)
}

// ---------- Speichern / Laden ----------

type saved struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	FinalURL  string    `json:"finalUrl"`
	Referrer  string    `json:"referrer"`
	UserAgent string    `json:"userAgent"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	ETag      string    `json:"etag"`
	LastMod   string    `json:"lastMod"`
	Split     bool      `json:"split"`
	OneLine   string    `json:"oneLine"`
	Chunk     int64     `json:"chunk"`
	Done      []int64   `json:"done"`
	State     string    `json:"state"`
	Error     string    `json:"error"`
	Created   time.Time `json:"created"`
	Finished  time.Time `json:"finished"`
}

func (m *Manager) storePath() string {
	if m.opt.DataDir == "" {
		return ""
	}
	return filepath.Join(m.opt.DataDir, "downloads.json")
}

var saveMu sync.Mutex

func (m *Manager) save() {
	p := m.storePath()
	if p == "" {
		return
	}
	m.mu.Lock()
	items := append([]*Download(nil), m.items...)
	m.mu.Unlock()
	var out []saved
	for _, d := range items {
		if d.getState() == StateCanceled || d.stream { // Browser-Downloads nicht merken
			continue
		}
		out = append(out, d.snapshot())
	}
	if len(out) > 50 { // nur die letzten 50 merken
		out = out[len(out)-50:]
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	saveMu.Lock()
	defer saveMu.Unlock()
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, p)
	}
}

func (m *Manager) load() {
	p := m.storePath()
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var in []saved
	if json.Unmarshal(b, &in) != nil {
		return
	}
	for _, s := range in {
		d := &Download{m: m, id: s.ID, created: s.Created, finished: s.Finished,
			req:  Request{URL: s.URL, Referrer: s.Referrer, UserAgent: s.UserAgent},
			path: s.Path, finalURL: s.FinalURL, size: s.Size, etag: s.ETag, lastMod: s.LastMod,
			split: s.Split, oneLine: s.OneLine, chunkSize: s.Chunk, state: s.State, err: s.Error}
		d.jar, _ = cookiejar.New(nil)
		d.doneRanges = s.Done
		var have int64 // Fortschritt sofort richtig anzeigen, nicht erst beim Fortsetzen
		for _, r := range normalizeRanges(s.Done, s.Size) {
			have += r[1] - r[0] + 1
		}
		d.doneBytes.Store(have)
		if d.state == StateRunning || d.state == StateProbe || d.state == StateQueued {
			d.state = StatePaused // nach Neustart: angehalten, Fortsetzen per Klick
		}
		if d.state == StatePaused {
			if _, err := os.Stat(d.partPath()); err != nil {
				d.state, d.err = StateError, "Teil-Datei fehlt – bitte neu starten"
			}
		}
		m.items = append(m.items, d)
	}
}
