// Package speedtest misst, was alle Leitungen ZUSAMMEN schaffen.
//
// Jede Leitung lädt gleichzeitig über mehrere eigene Verbindungen Testdaten
// von Cloudflare (speed.cloudflare.com). Gezählt wird erst nach einer kurzen
// Anlaufzeit, damit der langsame TCP-Start das Ergebnis nicht verfälscht.
// Damit das Handy-Datenvolumen nicht leidet, hört jede Leitung spätestens nach
// MaxBytes auf.
package speedtest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"wlanmerge/internal/i18n"
)

const (
	PerLine  = 4          // Verbindungen je Leitung
	MaxBytes = 150 << 20  // höchstens 150 MB je Leitung
	chunk    = 25_000_000 // je Anfrage 25 MB
)

// In Tests verkürzbar.
var (
	Warmup   = 2 * time.Second  // Anlaufzeit, wird nicht mitgezählt
	Duration = 10 * time.Second // gesamte Testdauer
)

// Sources sind die Testquellen in dieser Reihenfolge. Antwortet eine mit einem
// Fehler oder sperrt sie wegen zu vieler Tests (HTTP 429), nimmt die Leitung die
// nächste. %d wird durch die Größe ersetzt (nur Cloudflare kennt das).
// In Tests austauschbar.
var Sources = []string{
	"https://speed.cloudflare.com/__down?bytes=%d",
	"https://fsn1-speed.hetzner.com/1GB.bin",
	"https://proof.ovh.net/files/1Gb.dat",
	"https://nbg1-speed.hetzner.com/1GB.bin",
}

// Line ist eine Leitung; Dial muss die Verbindung fest an sie binden.
type Line struct {
	Name string
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// LineResult ist das Ergebnis einer Leitung.
type LineResult struct {
	Name  string `json:"name"`
	Rate  int64  `json:"rate"`  // Bytes/s
	Bytes int64  `json:"bytes"` // insgesamt geladen (Datenverbrauch)
	Error string `json:"error"`
}

// Result ist der Zwischen- bzw. Endstand.
type Result struct {
	Running  bool         `json:"running"`
	Progress float64      `json:"progress"` // 0…1
	Total    int64        `json:"total"`    // Bytes/s aller Leitungen zusammen
	Lines    []LineResult `json:"lines"`
	Error    string       `json:"error"`
	At       time.Time    `json:"at"`
}

type lineRun struct {
	l       Line
	bytes   atomic.Int64
	atWarm  atomic.Int64 // Stand am Ende der Anlaufzeit
	capAt   atomic.Int64 // Zeitpunkt (UnixNano), an dem MaxBytes erreicht war
	errMu   sync.Mutex
	err     error
	working atomic.Int32
	src     atomic.Int32 // welche Testquelle diese Leitung gerade nimmt
}

// url liefert die aktuelle Testquelle der Leitung.
func (r *lineRun) url() (string, int32) {
	i := r.src.Load()
	u := Sources[int(i)%len(Sources)]
	if strings.Contains(u, "%d") {
		u = fmt.Sprintf(u, chunk)
	}
	return u, i
}

// nextSource: diese Quelle macht Ärger – nächste nehmen (nur einmal je Fehler,
// auch wenn mehrere Verbindungen ihn gleichzeitig sehen).
func (r *lineRun) nextSource(was int32) bool {
	if int(was)+1 >= len(Sources) {
		return false
	}
	r.src.CompareAndSwap(was, was+1)
	return true
}

func (r *lineRun) setErr(err error) {
	r.errMu.Lock()
	if r.err == nil {
		r.err = err
	}
	r.errMu.Unlock()
}

// Run misst alle Leitungen gleichzeitig. update (darf nil sein) bekommt etwa
// viermal pro Sekunde den Zwischenstand – ohne update läuft während des Tests
// nichts außer den Downloads (sparsam).
func Run(ctx context.Context, lines []Line, update func(Result)) Result {
	if len(lines) == 0 {
		return Result{Error: i18n.T("Keine Leitung verfügbar"), At: time.Now()}
	}
	ctx, cancel := context.WithTimeout(ctx, Duration)
	defer cancel()

	runs := make([]*lineRun, len(lines))
	var wg sync.WaitGroup
	for i, l := range lines {
		r := &lineRun{l: l}
		runs[i] = r
		client := newClient(l)
		for k := 0; k < PerLine; k++ {
			wg.Add(1)
			r.working.Add(1)
			go func() {
				defer wg.Done()
				defer r.working.Add(-1)
				worker(ctx, client, r)
			}()
		}
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()

	start := time.Now()
	warm := false
	var warmAt time.Time
	// Für die Live-Anzeige: Geschwindigkeit der letzten Sekunde.
	type sample struct {
		t time.Time
		b []int64
	}
	var hist []sample
	snap := func() []int64 {
		b := make([]int64, len(runs))
		for i, r := range runs {
			b[i] = r.bytes.Load()
		}
		return b
	}

	var tickC <-chan time.Time
	if update != nil {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		tickC = tick.C
	}
	warmC := time.After(Warmup)
	for {
		done := false
		select {
		case <-finished:
			done = true
		case <-warmC:
		case <-tickC:
		}
		now := time.Now()
		if !warm && now.Sub(start) >= Warmup {
			warm, warmAt = true, now
			for _, r := range runs {
				r.atWarm.Store(r.bytes.Load())
			}
		}
		cur := snap()
		hist = append(hist, sample{now, cur})
		for len(hist) > 1 && now.Sub(hist[0].t) > time.Second {
			hist = hist[1:]
		}

		res := Result{Running: !done, Progress: min(1, float64(now.Sub(start))/float64(Duration)), At: now}
		for i, r := range runs {
			lr := LineResult{Name: r.l.Name, Bytes: cur[i]}
			if done {
				// Endergebnis: Durchschnitt nach der Anlaufzeit
				end := now
				if ns := r.capAt.Load(); ns != 0 {
					end = time.Unix(0, ns) // Leitung hat wegen MaxBytes früher aufgehört
				}
				if warm && end.Sub(warmAt) > 500*time.Millisecond {
					lr.Rate = int64(float64(cur[i]-r.atWarm.Load()) / end.Sub(warmAt).Seconds())
				} else if el := end.Sub(start).Seconds(); el > 0 {
					lr.Rate = int64(float64(cur[i]) / el)
				}
			} else if old := hist[0]; now.Sub(old.t) > 0 {
				lr.Rate = int64(float64(cur[i]-old.b[i]) / now.Sub(old.t).Seconds())
			}
			r.errMu.Lock()
			if r.err != nil && lr.Bytes < 1<<20 {
				lr.Error = friendly(r.err)
			}
			r.errMu.Unlock()
			res.Total += lr.Rate
			res.Lines = append(res.Lines, lr)
		}
		if done {
			res.Progress = 1
			if res.Total == 0 {
				res.Error = i18n.T("Test fehlgeschlagen – keine Leitung hat Daten geladen")
				for _, lr := range res.Lines {
					if lr.Error != "" {
						res.Error += " (" + lr.Error + ")"
						break
					}
				}
			}
			return res
		}
		if update != nil {
			update(res)
		}
	}
}

func worker(ctx context.Context, client *http.Client, r *lineRun) {
	buf := make([]byte, 256<<10) // große Happen: weniger Systemaufrufe
	for ctx.Err() == nil && r.bytes.Load() < MaxBytes {
		u, src := r.url()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", "WlanMerge-Speedtest")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() == nil {
				r.setErr(err)
				if !r.nextSource(src) {
					time.Sleep(300 * time.Millisecond)
				}
			}
			continue
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			r.setErr(fmt.Errorf("HTTP %d", resp.StatusCode))
			if !r.nextSource(src) { // alle Quellen durch
				if resp.StatusCode == http.StatusTooManyRequests {
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
			continue
		}
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 && r.bytes.Add(int64(n)) >= MaxBytes {
				r.capAt.CompareAndSwap(0, time.Now().UnixNano())
				break
			}
			if err != nil {
				if err != io.EOF && ctx.Err() == nil {
					r.setErr(err)
				}
				break
			}
		}
		resp.Body.Close()
	}
}

// newClient: eigener Client je Leitung, nur HTTP/1.1 (sonst bündelt HTTP/2
// alles in eine Verbindung) und nur IPv4.
func newClient(l Line) *http.Client {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if ip := net.ParseIP(host); ip != nil {
			return l.Dial(ctx, "tcp4", addr)
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
		if err != nil {
			return nil, err
		}
		var last error = errors.New("keine IPv4-Adresse")
		for _, ip := range ips {
			c, err := l.Dial(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	}
	return &http.Client{Transport: &http.Transport{
		DialContext:           dial,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		DisableCompression:    true,
		MaxIdleConnsPerHost:   PerLine,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		Proxy:                 nil,
	}}
}

func friendly(err error) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return i18n.T("Zeitüberschreitung")
	case errors.Is(err, context.Canceled):
		return i18n.T("abgebrochen")
	}
	s := err.Error()
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}
