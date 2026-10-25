package speedtest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// Zwei "Leitungen" mit unterschiedlicher Geschwindigkeit: Das Ergebnis muss
// beide einzeln zeigen und die Summe bilden.
func TestSumOfLines(t *testing.T) {
	Warmup, Duration = 300*time.Millisecond, 1500*time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		w.Header().Set("Content-Length", strconv.Itoa(n))
		// Langsame Leitung: kleine Häppchen mit Pause
		slow := r.Header.Get("X-Line") == "slow"
		buf := make([]byte, 32<<10)
		for sent := 0; sent < n; sent += len(buf) {
			if _, err := w.Write(buf); err != nil {
				return
			}
			if slow {
				time.Sleep(20 * time.Millisecond)
			} else {
				time.Sleep(5 * time.Millisecond)
			}
		}
	}))
	defer srv.Close()
	Sources = []string{srv.URL + "/__down?bytes=%d"}

	line := func(name string) Line {
		return Line{Name: name, Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &tagConn{Conn: c, tag: name}, nil
		}}
	}
	var updates int
	res := Run(context.Background(), []Line{line("fast"), line("slow")}, func(Result) { updates++ })
	if res.Error != "" || res.Running {
		t.Fatalf("Fehler: %+v", res)
	}
	f, s := res.Lines[0], res.Lines[1]
	if f.Rate <= 0 || s.Rate <= 0 {
		t.Fatalf("beide Leitungen müssen messen: %+v", res.Lines)
	}
	if f.Rate <= s.Rate {
		t.Fatalf("schnelle Leitung sollte schneller sein: %+v", res.Lines)
	}
	if res.Total != f.Rate+s.Rate {
		t.Fatalf("Summe falsch: %d != %d + %d", res.Total, f.Rate, s.Rate)
	}
	if updates < 3 {
		t.Fatalf("zu wenige Zwischenstände: %d", updates)
	}
}

func TestNoLines(t *testing.T) {
	if r := Run(context.Background(), nil, nil); r.Error == "" {
		t.Fatal("ohne Leitung muss ein Fehler kommen")
	}
}

// tagConn schreibt den Leitungsnamen als Header in die Anfrage (nur Test).
type tagConn struct {
	net.Conn
	tag  string
	done bool
}

func (c *tagConn) Write(p []byte) (int, error) {
	if !c.done {
		c.done = true
		// "GET ... HTTP/1.1\r\n" → Header direkt nach der ersten Zeile einfügen
		for i := 0; i+1 < len(p); i++ {
			if p[i] == '\r' && p[i+1] == '\n' {
				out := append(append(append([]byte{}, p[:i+2]...), "X-Line: "+c.tag+"\r\n"...), p[i+2:]...)
				if _, err := c.Conn.Write(out); err != nil {
					return 0, err
				}
				return len(p), nil
			}
		}
	}
	return c.Conn.Write(p)
}

// Sperrt die erste Quelle (HTTP 429), muss die Leitung auf die nächste wechseln.
func TestFallbackSource(t *testing.T) {
	Warmup, Duration = 200*time.Millisecond, 1200*time.Millisecond
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer blocked.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 32<<10)
		for i := 0; i < 2000; i++ {
			if _, err := w.Write(buf); err != nil {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer good.Close()
	Sources = []string{blocked.URL + "/__down?bytes=%d", good.URL + "/1GB.bin"}
	plain := Line{Name: "WLAN", Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	res := Run(context.Background(), []Line{plain}, nil)
	if res.Error != "" || res.Total <= 0 {
		t.Fatalf("Ausweich-Quelle nicht genutzt: %+v", res)
	}
}
