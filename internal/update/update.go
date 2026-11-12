// Package update fragt GitHub, ob es eine neuere Version von WlanMerge gibt.
// WlanMerge lädt und installiert nichts selbst: Ein Klick auf den Hinweis
// lässt den Browser den neuen Installer herunterladen, installieren muss man
// ihn selbst (ein Programm mit Admin-Rechten, das sich still selbst ersetzt,
// wäre ein Sicherheitsrisiko).
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"wlanmerge/internal/version"
)

// API ist die Abfrage-Adresse (in Tests austauschbar).
var API = "https://api.github.com/repos/" + version.Repo + "/releases/latest"

// Info beschreibt eine verfügbare neuere Version.
type Info struct {
	Version  string `json:"version"`  // z. B. "1.3.0"
	URL      string `json:"url"`      // Seite der Version auf GitHub
	Download string `json:"download"` // direkter Link zum Installer ("" = keiner gefunden)
}

// Installer ist der Dateiname des Installers in jeder Version.
const Installer = "WlanMerge-Setup-Windows.exe"

// Check liefert die neuere Version – oder nil, wenn diese hier aktuell ist.
func Check(ctx context.Context) (*Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", API, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "WlanMerge/"+version.Current)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var r struct {
		Tag    string `json:"tag_name"`
		URL    string `json:"html_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, err
	}
	// Nur Links auf GitHub selbst annehmen (die Oberfläche öffnet ihn im Browser).
	if !version.Newer(r.Tag, version.Current) || !strings.HasPrefix(r.URL, "https://github.com/") {
		return nil, nil
	}
	in := &Info{Version: strings.TrimPrefix(r.Tag, "v"), URL: r.URL}
	for _, a := range r.Assets {
		if a.Name == Installer && strings.HasPrefix(a.URL, "https://github.com/") {
			in.Download = a.URL
		}
	}
	return in, nil
}
