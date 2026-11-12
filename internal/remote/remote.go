// Package remote verbindet das Fenster mit dem WlanMerge-Hintergrunddienst.
//
// WlanMerge besteht aus zwei Teilen: Der Dienst (Tray-Symbol, Bündelung) läuft
// dauerhaft und braucht kaum Speicher. Das Fenster ist ein eigenes Programm,
// das nur läuft, solange es offen ist – so belegt die Oberfläche im Hintergrund
// weder Arbeitsspeicher noch Grafikkarte.
//
// Technik: net/rpc (Go-Standardbibliothek) über eine benannte Pipe, die nur
// Administratoren und SYSTEM öffnen dürfen (beide Teile laufen mit
// Admin-Rechten). Verbindungen aus dem Netzwerk lehnt winio ab.
package remote

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"

	"wlanmerge/internal/config"
	"wlanmerge/internal/i18n"
	"wlanmerge/internal/service"
)

// PipeName ist eine Variable, damit Tests eine eigene Pipe nutzen können.
var PipeName = `\\.\pipe\WlanMerge-Control` + config.Sandbox()

// Empty ist das leere Argument bzw. die leere Antwort.
type Empty struct{}

// Limit / Flag: Argumente mit Namen.
type Limit struct {
	Name  string
	Bytes int64
}
type Flag struct {
	Name string
	On   bool
}

// ---------- Dienst-Seite ----------

// API ist das, was der Dienst über die Pipe anbietet.
type API struct {
	S *service.Service
	// WindowClosed wird gerufen, wenn das Fenster geschlossen wurde.
	WindowClosed func()
}

func (a *API) Snapshot(_ Empty, out *service.State) error  { *out = a.S.Snapshot(); return nil }
func (a *API) Ping(_ Empty, _ *Empty) error                { return nil }
func (a *API) SetOn(on bool, _ *Empty) error               { return a.S.SetOn(on) }
func (a *API) Emergency(_ Empty, out *[]string) error      { *out = a.S.Emergency(); return nil }
func (a *API) SetLimit(l Limit, _ *Empty) error            { return a.S.SetLimit(l.Name, l.Bytes) }
func (a *API) ResetUsage(name string, _ *Empty) error      { return a.S.ResetUsage(name) }
func (a *API) SetDisabled(f Flag, _ *Empty) error          { return a.S.SetDisabled(f.Name, f.On) }
func (a *API) SetAutoConnect(on bool, _ *Empty) error      { return a.S.SetAutoConnect(on) }
func (a *API) SetAutoPause(on bool, _ *Empty) error        { return a.S.SetAutoPause(on) }
func (a *API) SetRebalance(on bool, _ *Empty) error        { return a.S.SetRebalance(on) }
func (a *API) SetTheme(t string, _ *Empty) error           { return a.S.SetTheme(t) }
func (a *API) SetResetDay(d int, _ *Empty) error           { return a.S.SetResetDay(d) }
func (a *API) SetDownloadsEnabled(on bool, _ *Empty) error { return a.S.SetDownloadsEnabled(on) }
func (a *API) SetAppFilter(on bool, _ *Empty) error        { return a.S.SetAppFilter(on) }
func (a *API) SetPhoneApp(f Flag, _ *Empty) error          { return a.S.SetPhoneApp(f.Name, f.On) }
func (a *API) SetUpdateCheck(on bool, _ *Empty) error      { return a.S.SetUpdateCheck(on) }
func (a *API) SetOnboarded(_ Empty, _ *Empty) error        { return a.S.SetOnboarded() }
func (a *API) StartSpeedTest(_ Empty, _ *Empty) error      { return a.S.StartSpeedTest() }
func (a *API) SaveSpeedTest(_ Empty, _ *Empty) error       { return a.S.SaveSpeedTest() }
func (a *API) SetLang(l string, _ *Empty) error            { return a.S.SetLang(l) }
func (a *API) Diagnose(_ Empty, out *string) error         { *out = a.S.Diagnose(); return nil }
func (a *API) Closed(_ Empty, _ *Empty) error {
	if a.WindowClosed != nil {
		go a.WindowClosed()
	}
	return nil
}

// Listen öffnet die Pipe. Gibt es sie schon, läuft bereits ein Dienst – dann
// kommt ein Fehler zurück (so gibt es nie zwei Dienste gleichzeitig).
func Listen() (net.Listener, error) {
	cfg := &winio.PipeConfig{InputBufferSize: 64 << 10, OutputBufferSize: 64 << 10}
	if config.Sandbox() == "" {
		cfg.SecurityDescriptor = "D:P(A;;GA;;;BA)(A;;GA;;;SY)" // nur Administratoren und SYSTEM
	}
	return winio.ListenPipe(PipeName, cfg)
}

// Serve bedient die Pipe, bis ctx endet.
func Serve(ctx context.Context, l net.Listener, api *API) error {
	srv := rpc.NewServer()
	if err := srv.RegisterName("WM", api); err != nil {
		return err
	}
	go func() { <-ctx.Done(); l.Close() }()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		go srv.ServeConn(c)
	}
}

// ---------- Fenster-Seite ----------

// Client spricht mit dem Dienst und baut die Verbindung bei Bedarf neu auf.
type Client struct {
	mu sync.Mutex
	c  *rpc.Client
}

const noService = "WlanMerge läuft nicht"

func (c *Client) call(method string, args, reply any) error {
	c.mu.Lock()
	if c.c == nil {
		timeout := 2 * time.Second
		conn, err := winio.DialPipe(PipeName, &timeout)
		if err != nil {
			c.mu.Unlock()
			return errors.New(i18n.T(noService))
		}
		c.c = rpc.NewClient(conn)
	}
	cl := c.c
	c.mu.Unlock()
	err := cl.Call("WM."+method, args, reply)
	if _, fromService := err.(rpc.ServerError); err != nil && !fromService {
		// Verbindung verloren (Dienst beendet): beim nächsten Aufruf neu verbinden.
		c.mu.Lock()
		if c.c == cl {
			c.c.Close()
			c.c = nil
		}
		c.mu.Unlock()
		return errors.New(i18n.T(noService))
	} else if fromService {
		return errors.New(err.Error())
	}
	return nil
}

// Alive: Läuft der Dienst?
func (c *Client) Alive() bool { return c.call("Ping", Empty{}, &Empty{}) == nil }

// Snapshot liefert den Zustand; ok=false, wenn der Dienst nicht erreichbar ist.
func (c *Client) Snapshot() (st service.State, ok bool) {
	if err := c.call("Snapshot", Empty{}, &st); err != nil {
		return service.State{Error: err.Error()}, false
	}
	return st, true
}

func (c *Client) SetOn(on bool) error       { return c.call("SetOn", on, &Empty{}) }
func (c *Client) Emergency() []string       { var s []string; c.call("Emergency", Empty{}, &s); return s }
func (c *Client) ResetUsage(n string) error { return c.call("ResetUsage", n, &Empty{}) }
func (c *Client) SetLimit(n string, b int64) error {
	return c.call("SetLimit", Limit{Name: n, Bytes: b}, &Empty{})
}
func (c *Client) SetDisabled(n string, off bool) error {
	return c.call("SetDisabled", Flag{Name: n, On: off}, &Empty{})
}
func (c *Client) SetAutoConnect(on bool) error { return c.call("SetAutoConnect", on, &Empty{}) }
func (c *Client) SetAutoPause(on bool) error   { return c.call("SetAutoPause", on, &Empty{}) }
func (c *Client) SetRebalance(on bool) error   { return c.call("SetRebalance", on, &Empty{}) }
func (c *Client) SetTheme(t string) error      { return c.call("SetTheme", t, &Empty{}) }
func (c *Client) SetResetDay(d int) error      { return c.call("SetResetDay", d, &Empty{}) }
func (c *Client) SetDownloadsEnabled(on bool) error {
	return c.call("SetDownloadsEnabled", on, &Empty{})
}
func (c *Client) SetAppFilter(on bool) error { return c.call("SetAppFilter", on, &Empty{}) }
func (c *Client) SetPhoneApp(name string, allowed bool) error {
	return c.call("SetPhoneApp", Flag{Name: name, On: allowed}, &Empty{})
}
func (c *Client) SetUpdateCheck(on bool) error { return c.call("SetUpdateCheck", on, &Empty{}) }
func (c *Client) SetOnboarded() error          { return c.call("SetOnboarded", Empty{}, &Empty{}) }
func (c *Client) StartSpeedTest() error        { return c.call("StartSpeedTest", Empty{}, &Empty{}) }
func (c *Client) SaveSpeedTest() error         { return c.call("SaveSpeedTest", Empty{}, &Empty{}) }
func (c *Client) SetLang(l string) error       { return c.call("SetLang", l, &Empty{}) }
func (c *Client) Diagnose() string             { var s string; c.call("Diagnose", Empty{}, &s); return s }

// WindowClosed meldet dem Dienst, dass das Fenster geschlossen wird.
func (c *Client) WindowClosed() { c.call("Closed", Empty{}, &Empty{}) }

// Close trennt nur die Verbindung – der Dienst läuft weiter.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.c != nil {
		c.c.Close()
		c.c = nil
	}
}
