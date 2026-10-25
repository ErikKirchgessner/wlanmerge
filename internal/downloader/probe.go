package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// probeResult: was wir vor dem eigentlichen Download über den Server wissen.
type probeResult struct {
	FinalURL string // Ziel nach allen Weiterleitungen
	Range    bool   // Server kann Teil-Anfragen (206 + Content-Range)
	Size     int64  // Gesamtgröße, -1 = unbekannt
	ETag     string // nur "starke" ETags (für If-Range erlaubt)
	LastMod  string
	Name     string // Dateiname aus Content-Disposition oder URL
}

// statusError: der Server hat mit einem Fehlercode geantwortet.
type statusError struct {
	Code       int
	RetryAfter time.Duration
}

func (e *statusError) Error() string {
	switch e.Code {
	case 403:
		return "Server verweigert den Zugriff (403)"
	case 404:
		return "Datei nicht gefunden (404)"
	case 410:
		return "Link ist abgelaufen (410)"
	case 429:
		return "Server meldet zu viele Anfragen (429)"
	case 503:
		return "Server überlastet (503)"
	}
	return fmt.Sprintf("Server antwortet mit Fehler %d", e.Code)
}

func statusOf(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

// probe prüft mit einer 1-Byte-Teilanfrage (Range: bytes=0-0), ob der Server
// Teil-Anfragen kann, und löst dabei alle Weiterleitungen einmal auf.
func probe(ctx context.Context, client *http.Client, rawURL string, hdr http.Header) (*probeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	setHeaders(req, hdr)
	req.Header.Set("Range", "bytes=0-0")
	req.Close = true // Antwort nicht weiterlesen: ein Server ohne Range schickt sonst die ganze Datei

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	// Höchstens ein paar Bytes lesen, dann Verbindung schließen.
	io.CopyN(io.Discard, resp.Body, 1024)
	resp.Body.Close()

	r := &probeResult{FinalURL: resp.Request.URL.String(), Size: -1}
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if total, ok := parseContentRangeTotal(resp.Header.Get("Content-Range")); ok && total > 0 {
			r.Range, r.Size = true, total
		}
	case http.StatusOK:
		r.Size = resp.ContentLength // Server ignoriert Range
	default:
		return nil, &statusError{Code: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if et := resp.Header.Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
		r.ETag = et
	}
	r.LastMod = resp.Header.Get("Last-Modified")
	r.Name = nameFromResponse(resp)
	return r, nil
}

func setHeaders(req *http.Request, hdr http.Header) {
	for k, v := range hdr {
		req.Header[k] = v
	}
	req.Header.Set("Accept-Encoding", "identity") // keine Komprimierung, sonst stimmen die Bytes nicht
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) WlanMerge/1.0")
	}
}

// parseContentRangeTotal liest die Gesamtgröße aus "bytes 0-0/12345".
func parseContentRangeTotal(v string) (int64, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "bytes ") {
		return 0, false
	}
	i := strings.LastIndexByte(v, '/')
	if i < 0 || v[i+1:] == "*" {
		return 0, false
	}
	n, err := strconv.ParseInt(v[i+1:], 10, 64)
	return n, err == nil
}

// parseContentRangeStart liest den Anfang aus "bytes 100-199/1000".
func parseContentRangeStart(v string) (int64, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "bytes "))
	i := strings.IndexByte(v, '-')
	if i <= 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(v[:i], 10, 64)
	return n, err == nil
}

// parseRetryAfter: Sekunden oder HTTP-Datum; höchstens 60 s, Standard 2 s.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	d := 2 * time.Second
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		d = time.Duration(s) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
	}
	return max(time.Second, min(d, 60*time.Second))
}

func nameFromResponse(resp *http.Response) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if n := params["filename"]; n != "" { // mime dekodiert filename* bereits
				return n
			}
		}
	}
	return nameFromURL(resp.Request.URL)
}

func nameFromURL(u *url.URL) string {
	n := path.Base(u.Path)
	if n == "/" || n == "." {
		return ""
	}
	if un, err := url.PathUnescape(n); err == nil {
		n = un
	}
	return n
}

var reserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// SanitizeName macht aus einem fremden Dateinamen einen sicheren:
// keine Pfade, kein "..", keine verbotenen Zeichen, keine reservierten Namen.
func SanitizeName(n string) string {
	if i := strings.LastIndexAny(n, `/\`); i >= 0 {
		n = n[i+1:]
	}
	var b strings.Builder
	for _, r := range n {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	n = strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	n = strings.TrimLeft(n, ".") // keine versteckten Namen, kein ".."
	if r := []rune(n); len(r) > 180 {
		ext := path.Ext(n)
		if len([]rune(ext)) > 20 {
			ext = ""
		}
		n = string(r[:180-len([]rune(ext))]) + ext
	}
	base := strings.ToUpper(strings.TrimSuffix(n, path.Ext(n)))
	if reserved[base] {
		n = "_" + n
	}
	if n == "" {
		n = "download"
	}
	return n
}
