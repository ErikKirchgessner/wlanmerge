package downloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- Testserver mit zwei "Leitungen" ----------

type serverCfg struct {
	noRange      bool // Server ignoriert Range
	deny403Line2 bool // zweite Leitung bekommt 403
	limit429L2   bool // zweite Leitung bekommt 429
	slowLine2    bool // zweite Leitung ist deutlich langsamer
	slowAll      bool // beide Leitungen langsam (für Pause-Test)
}

type testEnv struct {
	t        *testing.T
	data     []byte
	cfg      serverCfg
	s1, s2   *httptest.Server
	hits     sync.Map // Pfad → *atomic.Int64
	lineByte [3]atomic.Int64
	fail2    atomic.Bool // Leitung 2 fällt aus
	m        *Manager
	dir      string
}

func newEnv(t *testing.T, size int, cfg serverCfg) *testEnv {
	e := &testEnv{t: t, cfg: cfg, data: make([]byte, size)}
	rand.New(rand.NewSource(42)).Read(e.data)
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		e.hit("/start")
		http.Redirect(w, r, "/file", http.StatusFound)
	})
	mux.HandleFunc("/file", e.serveFile)
	e.s1 = httptest.NewServer(mux)
	e.s2 = httptest.NewServer(mux)
	t.Cleanup(func() { e.s1.Close(); e.s2.Close() })
	e.dir = t.TempDir()
	return e
}

func (e *testEnv) hit(p string) {
	v, _ := e.hits.LoadOrStore(p, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

func (e *testEnv) hits_(p string) int64 {
	if v, ok := e.hits.Load(p); ok {
		return v.(*atomic.Int64).Load()
	}
	return 0
}

func (e *testEnv) lineOf(r *http.Request) int {
	la := r.Context().Value(http.LocalAddrContextKey).(net.Addr).String()
	if strings.HasSuffix(e.s2.Listener.Addr().String(), la[strings.LastIndex(la, ":"):]) {
		return 2
	}
	return 1
}

type countingWriter struct {
	http.ResponseWriter
	n *atomic.Int64
}

func (c countingWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n.Add(int64(n))
	return n, err
}

func (e *testEnv) serveFile(w http.ResponseWriter, r *http.Request) {
	e.hit("/file")
	line := e.lineOf(r)
	isProbe := r.Header.Get("Range") == "bytes=0-0"
	if line == 2 && !isProbe {
		if e.cfg.deny403Line2 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if e.cfg.limit429L2 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
	}
	if e.cfg.noRange {
		r.Header.Del("Range")
	}
	w.Header().Set("ETag", `"v1"`)
	var rw http.ResponseWriter = countingWriter{w, &e.lineByte[line]}
	if (line == 2 && e.cfg.slowLine2) || e.cfg.slowAll {
		rw = slowWriter{rw}
	}
	http.ServeContent(rw, r, "test.bin", time.Unix(1700000000, 0), bytes.NewReader(e.data))
}

type slowWriter struct{ http.ResponseWriter }

func (s slowWriter) Write(p []byte) (int, error) {
	time.Sleep(time.Duration(len(p)/1024) * 300 * time.Microsecond) // ~3 MB/s
	return s.ResponseWriter.Write(p)
}

func (e *testEnv) lines() []Line {
	dial := func(srv *httptest.Server, line int) func(ctx context.Context, network, addr string) (net.Conn, error) {
		return func(ctx context.Context, network, _ string) (net.Conn, error) {
			if line == 2 && e.fail2.Load() {
				return nil, &net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp4", srv.Listener.Addr().String())
		}
	}
	return []Line{
		{Name: "L1", Primary: true, Dial: dial(e.s1, 1)},
		{Name: "L2", Dial: dial(e.s2, 2)},
	}
}

func (e *testEnv) manager() *Manager {
	e.m = New(e.lines, Options{
		DataDir: e.dir, Dir: func() string { return e.dir }, AllowPrivate: true,
		PerLine: func() int { return 3 },
	})
	return e.m
}

func (e *testEnv) url(p string) string { return e.s1.URL + p }

func (e *testEnv) wait(id string) Info {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, in := range e.m.List() {
			if in.ID == id && (in.State == StateDone || in.State == StateError) {
				return in
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("Download wurde nicht fertig")
	return Info{}
}

func (e *testEnv) checkFile(in Info) {
	if in.State != StateDone {
		e.t.Fatalf("Download fehlgeschlagen: %s", in.Error)
	}
	got, err := os.ReadFile(in.Path)
	if err != nil {
		e.t.Fatal(err)
	}
	if sha256.Sum256(got) != sha256.Sum256(e.data) {
		e.t.Fatalf("Datei falsch zusammengesetzt (%d statt %d Bytes)", len(got), len(e.data))
	}
	if _, err := os.Stat(in.Path + partExt); err == nil {
		e.t.Fatal("Teil-Datei wurde nicht aufgeräumt")
	}
}

// ---------- Tests ----------

func TestRangeSplitsAcrossBothLines(t *testing.T) {
	e := newEnv(t, 8<<20, serverCfg{})
	in, err := e.manager().Add(Request{URL: e.url("/file")})
	if err != nil {
		t.Fatal(err)
	}
	if !in.Split {
		t.Fatal("Server kann Range – Download muss aufgeteilt werden")
	}
	in = e.wait(in.ID)
	e.checkFile(in)
	if e.lineByte[1].Load() == 0 || e.lineByte[2].Load() == 0 {
		t.Fatalf("beide Leitungen müssen Daten liefern (L1 %d, L2 %d)", e.lineByte[1].Load(), e.lineByte[2].Load())
	}
	if !strings.HasPrefix(in.Mode, "über") || strings.Contains(in.Mode, "erlaubt") {
		t.Fatalf("Anzeige falsch: %q", in.Mode)
	}
}

func TestNoRangeFallsBackToOneLine(t *testing.T) {
	e := newEnv(t, 3<<20, serverCfg{noRange: true})
	m := e.manager()
	if _, _, ok, reason := m.OfferStream(Request{URL: e.url("/file")}); ok || reason == "" {
		t.Fatal("ohne Range muss der Browser-Download beim Browser bleiben")
	}
	in, err := m.Add(Request{URL: e.url("/file")})
	if err != nil {
		t.Fatal(err)
	}
	if in.Split || in.Mode != "über 1 Leitung (Server erlaubt kein Aufteilen)" {
		t.Fatalf("Anzeige falsch: split=%v %q", in.Split, in.Mode)
	}
	e.checkFile(e.wait(in.ID))
	if e.lineByte[2].Load() != 0 {
		t.Fatal("ohne Range darf nur die erste Leitung laden")
	}
}

func Test403OnSecondLineUsesOnlyFirstLine(t *testing.T) {
	e := newEnv(t, 6<<20, serverCfg{deny403Line2: true})
	in, err := e.manager().Add(Request{URL: e.url("/file")})
	if err != nil {
		t.Fatal(err)
	}
	in = e.wait(in.ID)
	e.checkFile(in)
	if reason, ok := e.m.exc.Get("127.0.0.1"); !ok || reason == "" {
		t.Fatal("Server muss auf die Ausnahmeliste")
	}
	if !strings.Contains(in.Mode, "über 1 Leitung (Server erlaubt nur eine Leitung)") {
		t.Fatalf("Anzeige falsch: %q", in.Mode)
	}
	// Neuer Download vom selben Server: gleich nur eine Leitung (gespeicherte Ausnahme)
	m2 := New(e.lines, Options{DataDir: e.dir, Dir: func() string { return e.dir }, AllowPrivate: true})
	in2, err := m2.Add(Request{URL: e.url("/file")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in2.Mode, "nur eine Leitung") {
		t.Fatalf("gespeicherte Ausnahme greift nicht: %q", in2.Mode)
	}
	m2.Cancel(in2.ID)
}

func Test429ReducesAndListsServer(t *testing.T) {
	e := newEnv(t, 4<<20, serverCfg{limit429L2: true})
	in, err := e.manager().Add(Request{URL: e.url("/file")})
	if err != nil {
		t.Fatal(err)
	}
	in = e.wait(in.ID)
	e.checkFile(in)
	if _, ok := e.m.exc.Get("127.0.0.1"); !ok {
		t.Fatal("429 nur auf der zweiten Leitung → Server auf die Ausnahmeliste")
	}
}

func TestRedirectResolvedOnce(t *testing.T) {
	e := newEnv(t, 4<<20, serverCfg{})
	in, err := e.manager().Add(Request{URL: e.url("/start")})
	if err != nil {
		t.Fatal(err)
	}
	in = e.wait(in.ID)
	e.checkFile(in)
	if n := e.hits_("/start"); n != 1 {
		t.Fatalf("Weiterleitung soll nur einmal aufgelöst werden, wurde %d× abgefragt", n)
	}
	if !strings.HasSuffix(in.Path, "file") {
		t.Fatalf("Dateiname sollte vom Ziel kommen: %s", in.Path)
	}
}

func TestFasterLineGetsMoreData(t *testing.T) {
	e := newEnv(t, 12<<20, serverCfg{slowLine2: true})
	in, err := e.manager().Add(Request{URL: e.url("/file")})
	if err != nil {
		t.Fatal(err)
	}
	e.checkFile(e.wait(in.ID))
	if e.lineByte[1].Load() <= e.lineByte[2].Load() {
		t.Fatalf("schnelle Leitung muss mehr holen (L1 %d, L2 %d)", e.lineByte[1].Load(), e.lineByte[2].Load())
	}
	// Teilen statt doppelt laden: Der Server liefert nicht mehr als die Datei (+ 1 Byte Prüfung).
	if total := e.lineByte[1].Load() + e.lineByte[2].Load(); total > int64(len(e.data))+1+64<<10 {
		t.Fatalf("es wurde doppelt geladen: %d Bytes für eine %d-Byte-Datei", total, len(e.data))
	}
}

func TestLineFailsMidDownload(t *testing.T) {
	e := newEnv(t, 12<<20, serverCfg{slowLine2: true})
	in, err := e.manager().Add(Request{URL: e.url("/file")})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	e.fail2.Store(true) // Leitung 2 fällt aus
	e.checkFile(e.wait(in.ID))
}

func TestPauseAndResume(t *testing.T) {
	e := newEnv(t, 12<<20, serverCfg{slowAll: true})
	m := e.manager()
	in, err := m.Add(Request{URL: e.url("/file")})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	m.Pause(in.ID)
	for _, x := range m.List() {
		if x.ID == in.ID && x.State != StatePaused {
			t.Fatalf("Zustand %q statt pausiert", x.State)
		}
	}
	m.Resume(in.ID)
	e.checkFile(e.wait(in.ID))
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		`..\..\Windows\evil.exe`: "evil.exe",
		`a/b/c.zip`:              "c.zip",
		`CON.txt`:                "_CON.txt",
		`bad:name?.iso`:          "bad_name_.iso",
		`   `:                    "download",
		`..`:                     "download",
		`.hidden`:                "hidden",
		"tab\tname.bin":          "tab_name.bin",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("%q → %q, erwartet %q", in, got, want)
		}
	}
}

func TestPrivateAddressesBlocked(t *testing.T) {
	m := New(func() []Line { return []Line{{Name: "L1", Primary: true, Dial: (&net.Dialer{}).DialContext}} },
		Options{DataDir: t.TempDir(), Dir: t.TempDir})
	if _, err := m.Add(Request{URL: "http://127.0.0.1:9/x"}); err == nil || !strings.Contains(err.Error(), "Lokale") {
		t.Fatalf("lokale Adressen müssen gesperrt sein, bekam %v", err)
	}
	if _, err := m.Add(Request{URL: "file:///C:/Windows/win.ini"}); err == nil {
		t.Fatal("nur http/https erlaubt")
	}
}

func TestResumeAfterRestart(t *testing.T) {
	e := newEnv(t, 12<<20, serverCfg{slowAll: true})
	m := e.manager()
	in, err := m.Add(Request{URL: e.url("/file")})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	m.Close() // Programm wird beendet: Download hält an, Stand wird gespeichert
	served := e.lineByte[1].Load() + e.lineByte[2].Load()

	m2 := New(e.lines, Options{DataDir: e.dir, Dir: func() string { return e.dir }, AllowPrivate: true, PerLine: func() int { return 3 }})
	e.m = m2
	var found bool
	for _, x := range m2.List() {
		if x.ID == in.ID {
			found = true
			if x.State != StatePaused || x.Done == 0 && served > 2<<20 {
				t.Fatalf("nach Neustart: Zustand %q", x.State)
			}
		}
	}
	if !found {
		t.Fatal("Download nach Neustart nicht mehr in der Liste")
	}
	m2.Resume(in.ID)
	e.checkFile(e.wait(in.ID))
	// Fortsetzen lädt nur den Rest
	if total := e.lineByte[1].Load() + e.lineByte[2].Load(); total > int64(len(e.data))+2<<20 {
		t.Fatalf("beim Fortsetzen wurde zu viel neu geladen: %d Bytes", total)
	}
}

// ---------- Weitergabe an den Browser ----------

func (e *testEnv) streamManager(t *testing.T) *Manager {
	e.m = New(e.lines, Options{DataDir: e.dir, Dir: func() string { return e.dir }, AllowPrivate: true,
		PerLine: func() int { return 4 }, AutoTune: true})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := e.m.StartServer(ctx); err != nil {
		t.Fatal(err)
	}
	return e.m
}

func TestStreamToBrowser(t *testing.T) {
	e := newEnv(t, 10<<20, serverCfg{slowLine2: true})
	m := e.streamManager(t)
	u, token, ok, reason := m.OfferStream(Request{URL: e.url("/file"), Filename: "spiel.zip"})
	if !ok || token == "" {
		t.Fatalf("Angebot abgelehnt: %s", reason)
	}
	resp, err := http.Get(u) // so holt sich der Browser die Datei
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, e.data) {
		t.Fatalf("Browser bekam %d statt %d Bytes oder falschen Inhalt", len(got), len(e.data))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "spiel.zip") {
		t.Fatalf("Dateiname fehlt für den Browser: %q", cd)
	}
	if e.lineByte[2].Load() == 0 {
		t.Fatal("auch die zweite Leitung muss mitladen")
	}
	// Danach wird die Zwischendatei aufgeräumt.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && m.byToken(token) != nil {
		time.Sleep(100 * time.Millisecond)
	}
	if m.byToken(token) != nil {
		t.Fatal("Zwischendatei wurde nicht aufgeräumt")
	}
}

func TestStreamResumeWithRange(t *testing.T) {
	e := newEnv(t, 6<<20, serverCfg{})
	m := e.streamManager(t)
	u, _, ok, reason := m.OfferStream(Request{URL: e.url("/file")})
	if !ok {
		t.Fatal(reason)
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Range", "bytes=3000000-")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, e.data[3000000:]) {
		t.Fatalf("Fortsetzen ab Mitte klappt nicht (Status %d, %d Bytes)", resp.StatusCode, len(got))
	}
}

func TestStreamRejectsNoRangeAndDrop(t *testing.T) {
	e := newEnv(t, 2<<20, serverCfg{noRange: true})
	m := e.streamManager(t)
	if _, _, ok, _ := m.OfferStream(Request{URL: e.url("/file")}); ok {
		t.Fatal("ohne Range muss der Browser selbst laden")
	}
	e2 := newEnv(t, 2<<20, serverCfg{})
	m2 := e2.streamManager(t)
	_, token, ok, _ := m2.OfferStream(Request{URL: e2.url("/file")})
	if !ok {
		t.Fatal("Angebot sollte angenommen werden")
	}
	m2.Drop(token) // Browser war schon fertig
	if m2.byToken(token) != nil {
		t.Fatal("verworfenes Angebot muss verschwinden")
	}
	if resp, err := http.Get(e2.m.base + "/d/" + token + "/x"); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("verworfener Schlüssel darf nichts mehr liefern (Status %d)", resp.StatusCode)
		}
	}
	if resp, err := http.Get(e2.m.base + "/d/falsch/x"); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatal("falscher Schlüssel darf nichts liefern")
		}
	}
}
