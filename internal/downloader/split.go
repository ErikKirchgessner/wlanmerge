package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// errChanged: Die Datei hat sich auf dem Server geändert (oder der Server
// liefert plötzlich keine Teile mehr) – neu prüfen und von vorn beginnen.
var errChanged = errors.New("Datei auf dem Server hat sich geändert")

const (
	chunkOpen = iota
	chunkRunning
	chunkDone
)

// minSplit: Ein laufendes Stück wird nur geteilt, wenn beide Hälften mindestens
// so groß sind (sonst lohnt die neue Verbindung nicht).
const minSplit = 1 << 20

type chunk struct {
	start, end  int64 // end inklusive; end kann beim Teilen kleiner werden
	pos         int64 // ab hier fehlt noch etwas
	state       int
	line        string             // Leitung, die gerade lädt
	got         atomic.Int64       // im laufenden Versuch geschriebene Bytes
	cancel      context.CancelFunc // laufenden Versuch abbrechen
	avoid       string             // zuletzt gescheiterte Leitung – möglichst eine andere nehmen
	onlyPrimary bool               // nach 403 auf einer zweiten Leitung: nur noch die erste Leitung
	retries     int
}

// splitJob ist ein laufender aufgeteilter Download.
type splitJob struct {
	d        *Download
	f        *os.File
	host     string
	mu       sync.Mutex
	chunks   []*chunk
	open     int // noch nicht fertige Stücke
	fatal    error
	changed  bool
	perLine  atomic.Int32 // Verbindungen je Leitung (sinkt bei 429/503)
	backoff  sync.Map     // Leitung → time.Time (Retry-After)
	last429P atomic.Int64 // letzte 429/503 auf der ersten Leitung (UnixNano)
	suspect  atomic.Bool  // 403 auf einer zweiten Leitung gesehen
	workers  map[string]map[int]bool
	wmu      sync.Mutex
	clients  sync.Map // Leitung → *http.Client
	wake     chan struct{}

	// Automatische Verbindungszahl: so lange erhöhen, wie es schneller wird.
	tuning    bool
	tuneAt    time.Time
	tuneBytes int64
	tuneRate  float64
}

const (
	startPerLine = 6  // Verbindungen je Leitung zu Beginn
	maxPerLine   = 12 // höchstens
	tuneEvery    = 3 * time.Second
)

// tune erhöht die Verbindungen je Leitung um 2, solange das messbar (+8 %)
// schneller macht. Bremst ein Server (429/503), wird gesenkt und nicht mehr erhöht.
func (j *splitJob) tune() {
	now := time.Now()
	if now.Sub(j.tuneAt) < tuneEvery {
		return
	}
	total := j.d.doneBytes.Load()
	rate := float64(total-j.tuneBytes) / now.Sub(j.tuneAt).Seconds()
	first := j.tuneAt.IsZero()
	j.tuneAt, j.tuneBytes = now, total
	if first || !j.tuning {
		return
	}
	if float64(total) > 0.8*float64(j.d.size) { // kurz vor Schluss lohnt kein Umbau mehr
		j.tuning = false
		return
	}
	if j.tuneRate == 0 {
		j.tuneRate = rate // erste volle Messung = Ausgangswert
		return
	}
	p := j.perLine.Load()
	if rate > j.tuneRate*1.08 && p < maxPerLine {
		j.perLine.Store(min(maxPerLine, p+2))
		j.tuneRate = rate
		return
	}
	if p == startPerLine && rate > 0 { // erster Versuch: noch einmal mehr probieren
		j.perLine.Store(p + 2)
		j.tuneRate = max(j.tuneRate, rate)
		return
	}
	j.tuning = false // mehr Verbindungen bringen nichts mehr
}

func chunkSizeFor(size int64) int64 {
	switch {
	case size >= 2<<30:
		return 16 << 20
	case size >= 256<<20:
		return 8 << 20
	case size >= 32<<20:
		return 4 << 20
	default:
		return max(256<<10, size/16)
	}
}

func (d *Download) runSplit(ctx context.Context) error {
	d.mu.Lock()
	if d.chunkSize == 0 {
		d.chunkSize = chunkSizeFor(d.size)
	}
	cs, size := d.chunkSize, d.size
	done := normalizeRanges(d.doneRanges, size)
	d.mu.Unlock()

	j := &splitJob{d: d, workers: map[string]map[int]bool{}, wake: make(chan struct{}, 1)}
	if u, err := url.Parse(d.finalURL); err == nil {
		j.host = u.Hostname()
	}
	j.perLine.Store(int32(max(1, min(maxPerLine, d.m.opt.PerLine()))))
	j.tuning = d.m.opt.AutoTune

	// Stücke anlegen; schon Geladenes (nach Pause/Neustart) überspringen.
	var have int64
	for off := int64(0); off < size; off += cs {
		c := &chunk{start: off, end: min(size, off+cs) - 1}
		c.pos = c.start + coveredPrefix(done, c.start, c.end)
		have += c.pos - c.start
		if c.pos > c.end {
			c.state = chunkDone
		} else {
			j.open++
		}
		j.chunks = append(j.chunks, c)
	}
	d.doneBytes.Store(have)

	// Teil-Datei: vorhandene weiterverwenden, sonst in voller Größe anlegen.
	f, err := os.OpenFile(d.partPath(), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("Datei konnte nicht angelegt werden: %v", err)
	}
	defer f.Close()
	if st, _ := f.Stat(); st == nil || st.Size() != size {
		setSparse(f)
		if err := f.Truncate(size); err != nil {
			return fmt.Errorf("Nicht genug Speicherplatz? %v", err)
		}
	}
	j.f = f
	d.mu.Lock()
	d.job = j
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.job = nil
		d.mu.Unlock()
	}()

	jctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastSave := time.Now()
	for {
		j.spawn(jctx, &wg)
		j.mu.Lock()
		finished, fatal, changed := j.open == 0, j.fatal, j.changed
		j.mu.Unlock()
		if finished || fatal != nil || changed || ctx.Err() != nil {
			cancel()
			wg.Wait()
			j.persist()
			if changed {
				return errChanged
			}
			if fatal != nil {
				return fatal
			}
			return ctx.Err()
		}
		j.tune()
		if time.Since(lastSave) > saveEvery {
			j.persist()
			lastSave = time.Now()
		}
		select {
		case <-tick.C:
		case <-j.wake:
		case <-ctx.Done():
		}
	}
}

// normalizeRanges sortiert und verschmilzt fertige Bereiche [a,b] (Paare).
func normalizeRanges(r []int64, size int64) [][2]int64 {
	var out [][2]int64
	for i := 0; i+1 < len(r); i += 2 {
		a, b := max(0, r[i]), min(size-1, r[i+1])
		if a <= b {
			out = append(out, [2]int64{a, b})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	var merged [][2]int64
	for _, x := range out {
		if n := len(merged); n > 0 && x[0] <= merged[n-1][1]+1 {
			merged[n-1][1] = max(merged[n-1][1], x[1])
		} else {
			merged = append(merged, x)
		}
	}
	return merged
}

// coveredPrefix: wie viele Bytes ab start schon lückenlos fertig sind.
func coveredPrefix(done [][2]int64, start, end int64) int64 {
	for _, r := range done {
		if r[0] <= start && r[1] >= start {
			return min(r[1], end) - start + 1
		}
	}
	return 0
}

// persist merkt sich, was fertig ist (für Pause, Fortsetzen, Neustart).
func (j *splitJob) persist() {
	j.mu.Lock()
	var r []int64
	for _, c := range j.chunks {
		if c.state == chunkDone {
			r = append(r, c.start, c.end)
		} else if c.pos > c.start {
			r = append(r, c.start, c.pos-1)
		}
	}
	j.mu.Unlock()
	merged := normalizeRanges(r, j.d.size)
	flat := make([]int64, 0, len(merged)*2)
	for _, x := range merged {
		flat = append(flat, x[0], x[1])
	}
	j.d.mu.Lock()
	j.d.doneRanges = flat
	j.d.mu.Unlock()
	j.d.m.save()
}

func (j *splitJob) poke() {
	select {
	case j.wake <- struct{}{}:
	default:
	}
}

// spawn sorgt dafür, dass jede nutzbare Leitung ihre Arbeiter hat
// (auch für Leitungen, die erst während des Downloads dazukommen).
func (j *splitJob) spawn(ctx context.Context, wg *sync.WaitGroup) {
	per := int(j.perLine.Load())
	lines := j.d.usableLines()
	j.wmu.Lock()
	defer j.wmu.Unlock()
	for _, l := range lines {
		if j.workers[l.Name] == nil {
			j.workers[l.Name] = map[int]bool{}
		}
		for i := 0; i < per; i++ {
			if j.workers[l.Name][i] {
				continue
			}
			j.workers[l.Name][i] = true
			wg.Add(1)
			go func(l Line, i int) {
				defer wg.Done()
				j.worker(ctx, l, i)
				j.wmu.Lock()
				delete(j.workers[l.Name], i)
				j.wmu.Unlock()
				j.poke()
			}(l, i)
		}
	}
}

func (j *splitJob) client(l Line) *http.Client {
	if c, ok := j.clients.Load(l.Name); ok {
		return c.(*http.Client)
	}
	c, _ := j.clients.LoadOrStore(l.Name, j.d.client(l, false))
	return c.(*http.Client)
}

func (j *splitJob) lineAlive(name string) (primary, ok bool) {
	for i, l := range j.d.usableLines() {
		if l.Name == name {
			return i == 0, true
		}
	}
	return false, false
}

func (j *splitJob) worker(ctx context.Context, l Line, idx int) {
	for ctx.Err() == nil {
		if idx >= int(j.perLine.Load()) {
			return // weniger Verbindungen je Leitung gewünscht
		}
		primary, ok := j.lineAlive(l.Name)
		if !ok {
			return // Leitung weg, gestört, Limit erreicht – oder nur noch eine Leitung erlaubt
		}
		if v, ok := j.backoff.Load(l.Name); ok {
			if wait := time.Until(v.(time.Time)); wait > 0 {
				if !sleepCtx(ctx, wait) {
					return
				}
				continue
			}
		}
		c := j.next(l.Name, primary)
		if c == nil {
			j.mu.Lock()
			done := j.open == 0
			j.mu.Unlock()
			if done {
				return
			}
			if !sleepCtx(ctx, 300*time.Millisecond) {
				return
			}
			continue
		}
		err := j.fetch(ctx, l, c)
		j.result(l.Name, primary, c, err)
	}
}

// next liefert das nächste offene Stück für eine Leitung. Sind alle vergeben,
// wird das Stück mit dem größten Rest geteilt: Der bisherige Arbeiter lädt nur
// noch die erste Hälfte, die zweite übernimmt dieser Arbeiter. So bremst am
// Ende keine langsame Leitung – und kein Byte wird doppelt geladen
// (wichtig fürs Handy-Datenvolumen).
func (j *splitJob) next(line string, primary bool) *chunk {
	j.mu.Lock()
	defer j.mu.Unlock()
	multi := len(j.d.usableLines()) > 1
	take := func(c *chunk) *chunk {
		c.state, c.line = chunkRunning, line
		c.got.Store(0)
		return c
	}
	for _, c := range j.chunks {
		if c.state == chunkOpen && (!c.onlyPrimary || primary) && (!multi || c.avoid != line) {
			return take(c)
		}
	}
	for _, c := range j.chunks { // nur noch "gemiedene" Stücke übrig: dann doch nehmen
		if c.state == chunkOpen && (!c.onlyPrimary || primary) {
			return take(c)
		}
	}
	// Teilen
	var best *chunk
	var bestLeft int64
	for _, c := range j.chunks {
		if c.state != chunkRunning || (c.onlyPrimary && !primary) {
			continue
		}
		left := c.end + 1 - (c.pos + c.got.Load())
		if left >= 2*minSplit && left > bestLeft {
			best, bestLeft = c, left
		}
	}
	if best == nil {
		return nil
	}
	mid := best.end + 1 - bestLeft/2
	nc := &chunk{start: mid, end: best.end, pos: mid, onlyPrimary: best.onlyPrimary}
	best.end = mid - 1 // der laufende Arbeiter hört an der neuen Grenze auf
	j.chunks = append(j.chunks, nc)
	j.open++
	return take(nc)
}

func (j *splitJob) endOf(c *chunk) int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return c.end
}

// fetch lädt ein Stück (ab c.pos) und schreibt es an die richtige Stelle.
func (j *splitJob) fetch(ctx context.Context, l Line, c *chunk) error {
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	j.mu.Lock()
	c.cancel = cancel
	from, to := c.pos, c.end
	j.mu.Unlock()

	req, err := http.NewRequestWithContext(actx, http.MethodGet, j.d.finalURL, nil)
	if err != nil {
		return err
	}
	setHeaders(req, j.d.headers())
	req.Header.Set("Range", "bytes="+strconv.FormatInt(from, 10)+"-"+strconv.FormatInt(to, 10))
	if j.d.etag != "" {
		req.Header.Set("If-Range", j.d.etag)
	} else if j.d.lastMod != "" {
		req.Header.Set("If-Range", j.d.lastMod)
	}
	resp, err := j.client(l).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK, http.StatusRequestedRangeNotSatisfiable:
		return errChanged // Datei geändert oder Server liefert keine Teile mehr
	default:
		return &statusError{Code: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if s, ok := parseContentRangeStart(resp.Header.Get("Content-Range")); !ok || s != from {
		return errChanged
	}
	if total, ok := parseContentRangeTotal(resp.Header.Get("Content-Range")); ok && total != j.d.size {
		return errChanged
	}

	ir := newIdleReader(resp.Body, cancel)
	defer ir.stop()
	bufp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufp)
	buf := *bufp
	var got int64
	for {
		want := j.endOf(c) - from + 1 // kann durch Teilen kleiner werden
		if got >= want {
			return nil
		}
		n, rerr := ir.Read(buf[:min(int64(len(buf)), want-got)])
		if n > 0 {
			if _, err := j.f.WriteAt(buf[:n], from+got); err != nil {
				j.setFatal(fmt.Errorf("Schreiben fehlgeschlagen: %v", err))
				return err
			}
			got += int64(n)
			c.got.Store(got)
			j.d.doneBytes.Add(int64(n)) // Fortschritt Byte für Byte
			j.d.addLineBytes(l.Name, int64(n))
		}
		if rerr == io.EOF {
			if got >= j.endOf(c)-from+1 {
				return nil
			}
			return io.ErrUnexpectedEOF
		}
		if rerr != nil {
			if ir.timedOut() {
				return errors.New("keine Daten mehr")
			}
			return rerr
		}
	}
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, writeBufSize); return &b }}

func (j *splitJob) setFatal(err error) {
	j.mu.Lock()
	if j.fatal == nil {
		j.fatal = err
	}
	j.mu.Unlock()
	j.poke()
}

// result verarbeitet das Ergebnis eines Versuchs – hier stecken die Regeln
// für Fehler, 403, 429/503 und die selbstlernende Ausnahmeliste.
func (j *splitJob) result(line string, primary bool, c *chunk, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	got := c.got.Load()
	c.cancel, c.line = nil, ""
	c.pos += got // Geladenes behalten (bei Fehler geht es ab hier weiter)
	if err == nil {
		c.state = chunkDone
		j.open--
		// 403 auf einer zweiten Leitung, aber die erste Leitung schafft dasselbe
		// Stück: Der Server mag keine zweite IP → nur noch eine Leitung.
		if primary && c.onlyPrimary && j.suspect.Load() {
			j.toOneLine("Server erlaubt nur eine Leitung")
		}
		j.poke()
		return
	}
	if errors.Is(err, errChanged) {
		j.changed = true
		j.poke()
		return
	}
	c.state = chunkOpen
	if c.pos > c.end { // Fehler genau am Ende – trotzdem vollständig
		c.state = chunkDone
		j.open--
		j.poke()
		return
	}
	c.avoid = line
	code := statusOf(err)
	switch {
	case code == 403 && !primary:
		c.onlyPrimary = true // erst über die erste Leitung wiederholen
		j.suspect.Store(true)
	case code == 429 || code == 503:
		var se *statusError
		errors.As(err, &se)
		j.backoff.Store(line, time.Now().Add(se.RetryAfter))
		if p := j.perLine.Load(); p > 1 {
			j.perLine.Store(p - 1)
		}
		j.tuning = false // Server bremst: nicht wieder erhöhen
		now := time.Now().UnixNano()
		if primary {
			j.last429P.Store(now)
		} else if now-j.last429P.Load() > int64(30*time.Second) {
			// Nur die zweite Leitung wird gebremst → Server mag keine zweite IP.
			j.toOneLine("Server erlaubt nur eine Leitung")
		}
	default:
		if got == 0 { // nur Versuche ohne jeden Fortschritt zählen als Fehlschlag
			c.retries++
		}
		if code == 404 || code == 410 || (code == 403 && primary && c.retries >= 2) || c.retries > maxRetries {
			if code == 0 {
				j.fatal = fmt.Errorf("Download abgebrochen: %v", friendly(err))
			} else {
				j.fatal = friendly(err)
			}
		}
	}
	j.poke()
}

// toOneLine: Server auf die Ausnahmeliste setzen und nur noch die erste
// Leitung benutzen. (j.mu ist gesperrt.)
func (j *splitJob) toOneLine(reason string) {
	j.d.mu.Lock()
	already := j.d.oneLine != ""
	j.d.oneLine = reason
	j.d.mu.Unlock()
	if already {
		return
	}
	if j.host != "" {
		j.d.m.exc.Add(j.host, reason)
	}
	// Stücke, die nur noch für die erste Leitung markiert waren, wieder freigeben.
	for _, c := range j.chunks {
		c.onlyPrimary = false
		c.avoid = ""
	}
}
