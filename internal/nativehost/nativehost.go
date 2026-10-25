// Package nativehost verbindet die Browser-Erweiterung mit WlanMerge.
//
// Weg: Erweiterung → (Native Messaging) → wlanmerge-host → (Windows: Named Pipe,
// macOS/Linux: Unix-Socket) → WlanMerge. Der Kanal ist nur lokal und nur für
// den Benutzer zugänglich. Jede Nachricht wird streng geprüft.
//
// Kanal und Anmeldung beim Browser stehen in pipe_windows.go bzw. sock_unix.go.
package nativehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	HostName    = "com.wlanmerge.host"
	ExtensionID = "jjihegfggjgeeiehnidpiemloclhbeek"
	Origin      = "chrome-extension://" + ExtensionID + "/"
	maxMessage  = 64 << 10
)

// Offer: ein Browser-Download, den die Erweiterung WlanMerge anbietet.
type Offer struct {
	Type      string `json:"type"`
	URL       string `json:"url"`
	Referrer  string `json:"referrer"`
	UserAgent string `json:"userAgent"`
	Cookies   string `json:"cookies"`
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	Token     string `json:"token"` // nur bei type="drop"
}

// Reply: Antwort an die Erweiterung. Accept=false → der Browser lädt selbst weiter.
// Bei Accept lädt der Browser stattdessen URL (lokale Weitergabe von WlanMerge).
type Reply struct {
	Accept bool   `json:"accept"`
	URL    string `json:"url,omitempty"`
	Token  string `json:"token,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Validate prüft eine Nachricht streng (WlanMerge läuft mit Admin-Rechten).
func (o *Offer) Validate() error {
	if o.Type == "drop" { // Browser war schneller fertig: Angebot verwerfen
		if len(o.Token) != 32 || strings.Trim(o.Token, "0123456789abcdef") != "" {
			return errors.New("ungültiger Schlüssel")
		}
		return nil
	}
	if o.Type != "offer" {
		return errors.New("unbekannte Nachricht")
	}
	if len(o.URL) > 8192 || len(o.Referrer) > 8192 || len(o.UserAgent) > 1024 || len(o.Cookies) > 32<<10 || len(o.Filename) > 1024 {
		return errors.New("Nachricht zu groß")
	}
	u, err := url.Parse(o.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("nur http/https")
	}
	if o.Referrer != "" {
		if r, err := url.Parse(o.Referrer); err != nil || (r.Scheme != "http" && r.Scheme != "https") {
			o.Referrer = ""
		}
	}
	if strings.ContainsAny(o.UserAgent+o.Cookies, "\r\n") {
		return errors.New("ungültige Zeichen")
	}
	return nil
}

// ---------- App-Seite: Pipe-Server ----------

// Serve nimmt Angebote der Erweiterung an, bis ctx endet.
func Serve(ctx context.Context, handle func(Offer) Reply) error {
	l, err := listen()
	if err != nil {
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
		go serveConn(c, handle)
	}
}

func serveConn(c net.Conn, handle func(Offer) Reply) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(20 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(c, maxMessage)).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var o Offer
	reply := Reply{Reason: "ungültige Nachricht"}
	if json.Unmarshal(line, &o) == nil {
		if err := o.Validate(); err == nil {
			reply = handle(o)
		} else {
			reply.Reason = err.Error()
		}
	}
	b, _ := json.Marshal(reply)
	c.Write(append(b, '\n'))
}

// ---------- Helfer-Seite (wlanmerge-host.exe) ----------

// Forward reicht ein Angebot an die laufende App weiter.
func Forward(o Offer) Reply {
	c, err := dial(2 * time.Second)
	if err != nil {
		return Reply{Reason: "WlanMerge läuft nicht"}
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	b, _ := json.Marshal(o)
	if _, err := c.Write(append(b, '\n')); err != nil {
		return Reply{Reason: "WlanMerge antwortet nicht"}
	}
	line, _ := bufio.NewReader(io.LimitReader(c, maxMessage)).ReadBytes('\n')
	var r Reply
	if json.Unmarshal(line, &r) != nil {
		return Reply{Reason: "WlanMerge antwortet nicht"}
	}
	return r
}

// manifest ist die Anmeldung beim Browser (Chrome, Edge, Chromium, Brave).
func manifest(hostPath string) []byte {
	b, _ := json.MarshalIndent(map[string]any{
		"name":            HostName,
		"description":     "WlanMerge Download-Manager",
		"path":            hostPath,
		"type":            "stdio",
		"allowed_origins": []string{Origin},
	}, "", "  ")
	return b
}
