package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"wlanmerge/internal/version"
)

func TestCheck(t *testing.T) {
	tag, url := "v99.0.0", "https://github.com/x/y/releases/tag/v99.0.0"
	dl := "https://github.com/x/y/releases/download/v99.0.0/" + Installer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":%q,"assets":[{"name":"SHA256SUMS.txt","browser_download_url":"https://github.com/x/y/releases/download/v99.0.0/SHA256SUMS.txt"},{"name":%q,"browser_download_url":%q}]}`, tag, url, Installer, dl)
	}))
	defer srv.Close()
	API = srv.URL

	in, err := Check(context.Background())
	if err != nil || in == nil || in.Version != "99.0.0" || in.URL != url || in.Download != dl {
		t.Fatalf("neuere Version nicht erkannt: %+v, %v", in, err)
	}
	tag = "v" + version.Current
	if in, _ := Check(context.Background()); in != nil {
		t.Fatalf("gleiche Version darf kein Update sein: %+v", in)
	}
	tag, dl = "v99.0.0", "https://boese.example/setup.exe" // fremder Download-Link: nur die Seite anbieten
	if in, _ := Check(context.Background()); in == nil || in.Download != "" {
		t.Fatalf("fremder Download-Link darf nicht angenommen werden: %+v", in)
	}
	tag, url = "v99.0.0", "https://boese.example/download"
	if in, _ := Check(context.Background()); in != nil {
		t.Fatal("fremde Links dürfen nicht angenommen werden")
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v1.10.0", "1.9.3", true}, {"1.2.0", "1.2.0", false}, {"1.2.1", "v1.2.0", true}, {"1.1.9", "1.2.0", false}, {"quatsch", "1.0.0", false}, {"v2.0.0-beta", "1.9.9", true}} {
		if got := version.Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
