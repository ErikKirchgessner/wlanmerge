package downloader

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Weitergabe an den Browser:
//
// WlanMerge lädt die Datei in Stücken über alle Leitungen und reicht sie über
// eine lokale Adresse (http://127.0.0.1:…/d/<Zufallsschlüssel>) der Reihe nach
// an den Browser weiter. Der Browser zeigt seinen ganz normalen Download – nur
// mit der Geschwindigkeit aller Leitungen zusammen – und legt die Datei dort
// ab, wo er sie immer ablegt. Der Nutzer merkt von WlanMerge nichts.

const (
	streamGrace  = 15 * time.Second // Browser getrennt (Pause/Abbruch): so lange weiterladen
	streamExpiry = 2 * time.Hour    // pausierte Browser-Downloads danach aufräumen
)

// cacheDir: Zwischenablage für Browser-Downloads (im geschützten Datenordner).
func (m *Manager) cacheDir() string {
	return filepath.Join(m.opt.DataDir, "cache")
}

// StartServer startet die lokale Weitergabe (nur 127.0.0.1, zufälliger Port).
func (m *Manager) StartServer(ctx context.Context) error {
	os.RemoveAll(m.cacheDir()) // Reste vom letzten Lauf
	if err := os.MkdirAll(m.cacheDir(), 0o755); err != nil {
		return err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.base = "http://" + ln.Addr().String()
	m.mu.Unlock()
	mux := http.NewServeMux()
	mux.HandleFunc("/d/", m.serveStream)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	go srv.Serve(ln)
	return nil
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// OfferStream: Die Erweiterung bietet einen Browser-Download an. Kann der
// Server Teil-Anfragen, beginnt WlanMerge sofort zu laden und liefert die
// lokale Adresse, die der Browser stattdessen laden soll.
func (m *Manager) OfferStream(req Request) (localURL, token string, ok bool, reason string) {
	m.mu.Lock()
	base := m.base
	m.mu.Unlock()
	if base == "" {
		return "", "", false, "Weitergabe nicht bereit"
	}
	req.FromBrowser = true
	d, err := m.prepare(req)
	if err != nil {
		return "", "", false, err.Error()
	}
	if !d.split || d.size <= 0 {
		return "", "", false, "Server erlaubt kein Aufteilen"
	}
	d.stream = true
	d.token = newToken()
	d.display = baseOf(d.path)
	d.path = filepath.Join(m.cacheDir(), d.id) // Daten liegen in <id>.wmpart
	d.lastSeen = time.Now()
	m.enqueue(d)
	if m.opt.OnStream != nil {
		go m.opt.OnStream(d.info())
	}
	return base + "/d/" + d.token + "/" + url.PathEscape(d.display), d.token, true, ""
}

// Drop: Der Browser hatte die Datei schon fertig, bevor wir übernehmen konnten.
func (m *Manager) Drop(token string) {
	if d := m.byToken(token); d != nil {
		m.Remove(d.id)
	}
}

func (m *Manager) byToken(token string) *Download {
	if token == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.items {
		if d.stream && d.token == token {
			return d
		}
	}
	return nil
}

// available: so viele Bytes ab Dateianfang sind lückenlos fertig.
func (d *Download) available() int64 {
	d.mu.Lock()
	j, state, size := d.job, d.state, d.size
	ranges := d.doneRanges
	d.mu.Unlock()
	if state == StateDone {
		return size
	}
	if j != nil {
		return j.contiguous()
	}
	return coveredPrefix(normalizeRanges(ranges, size), 0, size-1)
}

func (j *splitJob) contiguous() int64 {
	j.mu.Lock()
	type iv struct{ a, b int64 }
	var done []iv
	for _, c := range j.chunks {
		switch {
		case c.state == chunkDone:
			done = append(done, iv{c.start, c.end + 1})
		case c.pos > c.start:
			done = append(done, iv{c.start, c.pos})
		}
		if c.state == chunkRunning { // gerade geschriebene Bytes zählen auch
			if g := c.got.Load(); g > 0 {
				done = append(done, iv{c.pos, c.pos + g})
			}
		}
	}
	j.mu.Unlock()
	sort.Slice(done, func(a, b int) bool { return done[a].a < done[b].a })
	var have int64
	for _, x := range done {
		if x.a > have {
			break
		}
		have = max(have, x.b)
	}
	return have
}

// serveStream liefert die Datei der Reihe nach an den Browser – so schnell,
// wie die Stücke über alle Leitungen eintreffen. Unterstützt Range, damit der
// Browser pausieren und fortsetzen kann.
func (m *Manager) serveStream(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/d/"), "/", 2)
	d := m.byToken(parts[0])
	if d == nil {
		http.NotFound(w, r)
		return
	}
	d.mu.Lock()
	d.readers++
	d.lastSeen = time.Now()
	size, name := d.size, d.display
	paused := d.state == StatePaused
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.readers--
		d.lastSeen = time.Now()
		d.mu.Unlock()
	}()
	if paused {
		m.Resume(d.id) // Browser macht nach einer Pause weiter
	}

	var start int64
	if rg := r.Header.Get("Range"); strings.HasPrefix(rg, "bytes=") {
		if a, _, ok := strings.Cut(strings.TrimPrefix(rg, "bytes="), "-"); ok {
			if n, err := strconv.ParseInt(a, 10, 64); err == nil && n >= 0 && n < size {
				start = n
			}
		}
	}
	h := w.Header()
	h.Set("Content-Type", contentType(name))
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	h.Set("Accept-Ranges", "bytes")
	h.Set("ETag", `"`+d.token+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.FormatInt(size-start, 10))
	if start > 0 {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, size-1, size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead {
		return
	}

	f, err := os.Open(d.partPath())
	for err != nil && r.Context().Err() == nil { // Datei wird gerade erst angelegt
		time.Sleep(50 * time.Millisecond)
		if s := d.getState(); s == StateError || s == StateCanceled {
			return
		}
		f, err = os.Open(d.partPath())
	}
	if err != nil {
		return
	}
	defer f.Close()
	bufp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufp)
	buf := *bufp
	flusher, _ := w.(http.Flusher)
	pos := start
	for pos < size {
		if r.Context().Err() != nil {
			return
		}
		avail := d.available()
		if avail <= pos {
			if s := d.getState(); s == StateError || s == StateCanceled {
				return // Verbindung abbrechen → Browser meldet den Fehler
			}
			time.Sleep(40 * time.Millisecond)
			continue
		}
		n, rerr := f.ReadAt(buf[:min(int64(len(buf)), avail-pos)], pos)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			pos += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return
		}
	}
	// Alles beim Browser angekommen: Zwischendatei aufräumen.
	go func() {
		time.Sleep(2 * time.Second)
		d.mu.Lock()
		idle := d.readers == 0
		d.mu.Unlock()
		if idle {
			m.Remove(d.id)
		}
	}()
}

// tidyStreams: vom Browser verlassene Downloads anhalten bzw. aufräumen.
func (m *Manager) tidyStreams() {
	m.mu.Lock()
	items := append([]*Download(nil), m.items...)
	m.mu.Unlock()
	now := time.Now()
	for _, d := range items {
		if !d.stream {
			continue
		}
		d.mu.Lock()
		readers, last, state := d.readers, d.lastSeen, d.state
		d.mu.Unlock()
		if readers > 0 {
			continue
		}
		switch {
		case now.Sub(last) > streamExpiry || state == StateError || state == StateCanceled:
			if now.Sub(last) > streamGrace {
				m.Remove(d.id)
			}
		case (state == StateRunning || state == StateQueued) && now.Sub(last) > streamGrace:
			m.Pause(d.id) // Browser ist weg (pausiert/abgebrochen): Leitungen nicht weiter belasten
		}
	}
}

func contentType(name string) string {
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}
