package downloader

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"wlanmerge/internal/i18n"
)

// Download ist ein einzelner Download.
type Download struct {
	m        *Manager
	id       string
	req      Request
	jar      *cookiejar.Jar
	created  time.Time
	finished time.Time

	mu         sync.Mutex
	state      string
	err        string
	path       string // endgültiger Pfad (die Teil-Datei heißt path + ".wmpart")
	finalURL   string
	size       int64 // -1 = unbekannt
	etag       string
	lastMod    string
	split      bool   // Server kann Teil-Anfragen
	oneLine    string // Grund, warum nur eine Leitung (leer = alle Leitungen)
	chunkSize  int64
	doneRanges []int64 // fertige Bereiche als Paare [von, bis] (für Fortsetzen)
	cancel     context.CancelFunc
	finishedC  chan struct{}
	job        *splitJob // laufender aufgeteilter Download (für die Weitergabe)

	// Browser-Weitergabe
	stream   bool      // Datei geht über die lokale Adresse an den Browser
	token    string    // Zufallsschlüssel der lokalen Adresse
	display  string    // Dateiname für den Browser
	readers  int       // Browser-Verbindungen, die gerade lesen
	lastSeen time.Time // letzte Aktivität des Browsers

	// Live-Zahlen
	doneBytes atomic.Int64
	lineBytes sync.Map // Leitungsname → *atomic.Int64
	speed     int64
	perLine   map[string]int64
	lastTotal map[string]int64
	usedLines int
}

func (d *Download) getState() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *Download) setState(s string) {
	d.mu.Lock()
	d.state = s
	d.mu.Unlock()
}

func (d *Download) partPath() string { return d.path + partExt }

func (d *Download) apply(pr *probeResult) {
	d.finalURL, d.size, d.etag, d.lastMod, d.split = pr.FinalURL, pr.Size, pr.ETag, pr.LastMod, pr.Range
	d.doneRanges, d.chunkSize = nil, 0
	d.oneLine = ""
	if u, err := url.Parse(pr.FinalURL); err == nil {
		if reason, ok := d.m.exc.Get(u.Hostname()); ok {
			d.oneLine = reason
		}
	}
}

func (d *Download) headers() http.Header {
	h := http.Header{}
	if d.req.Referrer != "" {
		h.Set("Referer", d.req.Referrer)
	}
	if d.req.UserAgent != "" {
		h.Set("User-Agent", d.req.UserAgent)
	}
	return h
}

// client: eigener HTTP-Client je Leitung. Nur HTTP/1.1 (HTTP/2 würde alle
// Anfragen in EINE Verbindung bündeln – dann ließe sich nichts verteilen).
func (d *Download) client(l Line, follow bool) *http.Client {
	tr := &http.Transport{
		DialContext:           d.m.dialer(l),
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		DisableCompression:    true,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		Proxy:                 nil,
	}
	c := &http.Client{Transport: tr, Jar: d.jar}
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if !follow {
			return http.ErrUseLastResponse
		}
		if len(via) >= 10 {
			return errors.New("zu viele Weiterleitungen")
		}
		if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
			return errors.New("ungültige Weiterleitung")
		}
		return nil
	}
	return c
}

func (d *Download) stop(newState string) {
	d.mu.Lock()
	cancel, done := d.cancel, d.finishedC
	if d.state != StateDone {
		d.state = newState
	}
	d.mu.Unlock()
	if cancel != nil {
		cancel()
		if done != nil {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (d *Download) fail(msg string) {
	d.mu.Lock()
	if d.state == StateRunning {
		d.state, d.err = StateError, msg
	}
	d.mu.Unlock()
}

// ---------- Ablauf ----------

// start legt Abbruch-Möglichkeit und Fertig-Signal an, BEVOR der Download
// losläuft – sonst könnte ein sofortiges "Abbrechen" ins Leere gehen.
func (d *Download) start() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	d.mu.Lock()
	d.cancel, d.finishedC = cancel, done
	d.mu.Unlock()
	go d.run(ctx, cancel, done)
}

func (d *Download) run(ctx context.Context, cancel context.CancelFunc, done chan struct{}) {
	defer func() {
		cancel()
		d.m.save() // erst speichern, dann "fertig" melden (Pause/Beenden verliert nichts)
		close(done)
		d.m.kick()
	}()

	for attempt := 0; attempt < 3; attempt++ {
		var err error
		if d.split {
			err = d.runSplit(ctx)
		} else {
			err = d.runStream(ctx)
		}
		if ctx.Err() != nil {
			return // pausiert oder abgebrochen
		}
		if errors.Is(err, errChanged) {
			// Datei hat sich auf dem Server geändert oder Range wurde abgeschaltet:
			// neu prüfen und von vorn beginnen.
			if !d.reprobe(ctx) {
				return
			}
			continue
		}
		if err != nil {
			d.fail(err.Error())
			return
		}
		d.finish()
		return
	}
	d.fail("Datei ändert sich ständig auf dem Server")
}

func (d *Download) reprobe(ctx context.Context) bool {
	lines := d.m.lines()
	if len(lines) == 0 {
		d.fail("Keine Internetverbindung")
		return false
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	pr, err := probe(pctx, d.client(lines[0], true), d.req.URL, d.headers())
	if err != nil {
		d.fail(friendly(err).Error())
		return false
	}
	d.mu.Lock()
	d.apply(pr)
	d.mu.Unlock()
	d.doneBytes.Store(0)
	os.Remove(d.partPath())
	return true
}

func (d *Download) finish() {
	part := d.partPath()
	if d.stream { // Browser holt die Datei selbst ab und legt sie ab
		d.mu.Lock()
		d.state, d.finished, d.err = StateDone, time.Now(), ""
		d.mu.Unlock()
		return
	}
	if d.size > 0 {
		if st, err := os.Stat(part); err != nil || st.Size() != d.size {
			d.fail("Datei unvollständig – bitte erneut versuchen")
			return
		}
	}
	final := d.path
	if _, err := os.Stat(final); err == nil { // Name inzwischen belegt → nummerieren
		final = uniquePath(dirOf(d.path), baseOf(d.path), nil)
	}
	if err := os.Rename(part, final); err != nil {
		d.fail("Datei konnte nicht umbenannt werden: " + err.Error())
		return
	}
	giveToUser(final)
	markFromInternet(final, d.finalURL, d.req.Referrer)
	d.mu.Lock()
	d.path, d.state, d.finished, d.err = final, StateDone, time.Now(), ""
	d.doneRanges = nil
	d.mu.Unlock()
}

func dirOf(p string) string  { return p[:max(0, strings.LastIndexAny(p, `\/`))] }
func baseOf(p string) string { return p[strings.LastIndexAny(p, `\/`)+1:] }

// lineFor liefert die Leitungen, die dieser Download gerade nutzen darf.
func (d *Download) usableLines() []Line {
	lines := d.m.lines()
	d.mu.Lock()
	one := d.oneLine != ""
	d.mu.Unlock()
	if one && len(lines) > 1 {
		return lines[:1]
	}
	return lines
}

func (d *Download) addLineBytes(name string, n int64) {
	v, _ := d.lineBytes.LoadOrStore(name, new(atomic.Int64))
	v.(*atomic.Int64).Add(n)
}

// ---------- Ohne Aufteilen: eine Verbindung, eine Leitung ----------

func (d *Download) runStream(ctx context.Context) error {
	var lastErr error
	for try := 0; try < maxRetries; try++ {
		lines := d.m.lines()
		if len(lines) == 0 {
			return errors.New("Keine Internetverbindung")
		}
		d.doneBytes.Store(0)
		lastErr = d.streamOnce(ctx, lines[0])
		if lastErr == nil || ctx.Err() != nil {
			return lastErr
		}
		if c := statusOf(lastErr); c == 404 || c == 410 || c == 403 {
			return lastErr
		}
		sleepCtx(ctx, time.Duration(try+1)*time.Second)
	}
	return lastErr
}

func (d *Download) streamOnce(ctx context.Context, l Line) error {
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodGet, d.finalURL, nil)
	if err != nil {
		return err
	}
	setHeaders(req, d.headers())
	resp, err := d.client(l, true).Do(req)
	if err != nil {
		return friendly(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &statusError{Code: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	f, err := os.Create(d.partPath())
	if err != nil {
		return err
	}
	defer f.Close()
	if d.size <= 0 && resp.ContentLength > 0 {
		d.mu.Lock()
		d.size = resp.ContentLength
		d.mu.Unlock()
	}
	r := newIdleReader(resp.Body, cancel)
	defer r.stop()
	buf := make([]byte, writeBufSize)
	var off int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, err := f.WriteAt(buf[:n], off); err != nil {
				return fmt.Errorf("Schreiben fehlgeschlagen: %v", err)
			}
			off += int64(n)
			d.doneBytes.Add(int64(n))
			d.addLineBytes(l.Name, int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if r.timedOut() {
				return errors.New("Server sendet keine Daten mehr")
			}
			return friendly(rerr)
		}
	}
	if d.size > 0 && off != d.size {
		return errors.New("Verbindung vorzeitig beendet")
	}
	if d.size <= 0 {
		d.mu.Lock()
		d.size = off
		d.mu.Unlock()
	}
	return nil
}

// ---------- Hilfen ----------

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// idleReader bricht ab, wenn 15 s lang keine Daten kommen.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
	fired atomic.Bool
}

func newIdleReader(r io.Reader, cancel context.CancelFunc) *idleReader {
	ir := &idleReader{r: r}
	ir.timer = time.AfterFunc(idleTimeout, func() { ir.fired.Store(true); cancel() })
	return ir
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.timer.Reset(idleTimeout)
	}
	return n, err
}

func (ir *idleReader) stop()          { ir.timer.Stop() }
func (ir *idleReader) timedOut() bool { return ir.fired.Load() }

// ---------- Anzeige und Speichern ----------

// tick rechnet einmal pro Sekunde die Geschwindigkeiten aus.
func (d *Download) tick() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastTotal == nil {
		d.lastTotal = map[string]int64{}
	}
	d.perLine = map[string]int64{}
	var sum int64
	used := 0
	d.lineBytes.Range(func(k, v any) bool {
		name, total := k.(string), v.(*atomic.Int64).Load()
		rate := total - d.lastTotal[name]
		d.lastTotal[name] = total
		d.perLine[name] = rate
		sum += rate
		if rate > 0 {
			used++
		}
		return true
	})
	d.speed = sum
	if used > 0 || d.state != StateRunning {
		d.usedLines = used
	}
}

func (d *Download) info() Info {
	d.mu.Lock()
	defer d.mu.Unlock()
	name := baseOf(d.path)
	if d.stream {
		name = d.display
	}
	in := Info{ID: d.id, Name: name, Path: d.path, Size: d.size, Done: d.doneBytes.Load(),
		State: d.state, Error: d.err, Split: d.split, Speed: d.speed, PerLine: d.perLine,
		Created: d.created, Finished: d.finished, Resume: d.split}
	if d.state == StateDone {
		in.Done, in.Speed, in.PerLine = d.size, 0, nil
	}
	if d.state != StateRunning {
		in.Speed, in.PerLine = 0, nil
	}
	lines := len(d.m.lines())
	switch {
	case !d.split:
		in.Lines, in.Mode = 1, i18n.Tf("über 1 Leitung (%s)", i18n.T("Server erlaubt kein Aufteilen"))
	case d.oneLine != "":
		in.Lines, in.Mode = 1, i18n.Tf("über 1 Leitung (%s)", i18n.T(d.oneLine))
	case lines < 2:
		in.Lines, in.Mode = 1, i18n.Tf("über 1 Leitung (%s)", i18n.T("nur eine Leitung verfügbar"))
	default:
		in.Lines = lines
		if d.state == StateRunning && d.usedLines > 0 {
			in.Lines = d.usedLines
		}
		in.Mode = i18n.Tf("über %d Leitungen", in.Lines)
		if in.Lines == 1 {
			in.Mode = i18n.T("über 1 Leitung")
		}
	}
	return in
}

func (d *Download) snapshot() saved {
	d.mu.Lock()
	defer d.mu.Unlock()
	return saved{ID: d.id, URL: d.req.URL, FinalURL: d.finalURL, Referrer: d.req.Referrer, UserAgent: d.req.UserAgent,
		Path: d.path, Size: d.size, ETag: d.etag, LastMod: d.lastMod, Split: d.split, OneLine: d.oneLine,
		Chunk: d.chunkSize, Done: d.doneRanges, State: d.state, Error: d.err,
		Created: d.created, Finished: d.finished}
}
