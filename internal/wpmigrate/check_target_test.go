package wpmigrate

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

// The validation probe targets the public base URL (site.base_url first), but
// may reach a private address only when that URL is the operator-configured
// GORUN_BASE_URL or GORUN_ALLOW_PRIVATE_NET is set.
func TestHTTPCheckKeepsPrivateBlockForSettingsBaseURL(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(internal.Close)
	setBase := func(v string) {
		t.Helper()
		if err := e.app.Deps.Settings.Set(e.ctx(), kernel.SettingBaseURL, v); err != nil {
			t.Fatal(err)
		}
	}

	// The settings value alone (no configured base URL) keeps the block.
	setBase(internal.URL)
	e.svc.cfg.BaseURL, e.svc.cfg.AllowPrivateNet = "", false
	if status := e.svc.httpCheck(e.ctx(), "/probe"); status != 0 || hits.Load() != 0 {
		t.Fatalf("settings base URL reached a loopback address: status %d, hits %d", status, hits.Load())
	}

	// A settings value that differs from the configured base URL keeps the block too.
	e.svc.cfg.BaseURL = "https://site.example"
	if status := e.svc.httpCheck(e.ctx(), "/probe"); status != 0 || hits.Load() != 0 {
		t.Fatalf("settings base URL differing from GORUN_BASE_URL: status %d, hits %d", status, hits.Load())
	}

	// The configured base URL itself (same origin, trailing slash ignored) may be private.
	e.svc.cfg.BaseURL = internal.URL + "/"
	if status := e.svc.httpCheck(e.ctx(), "/probe"); status != http.StatusNoContent || hits.Load() != 1 {
		t.Fatalf("configured base URL: status %d, hits %d", status, hits.Load())
	}
	setBase("")
	if base, private := e.svc.checkTarget(e.ctx()); base != internal.URL || !private {
		t.Fatalf("no setting: checkTarget = %q %v", base, private)
	}

	setBase("http://169.254.169.254")
	if base, private := e.svc.checkTarget(e.ctx()); base != "http://169.254.169.254" || private {
		t.Fatalf("metadata address in settings: checkTarget = %q %v", base, private)
	}
	e.svc.cfg.AllowPrivateNet = true
	if _, private := e.svc.checkTarget(e.ctx()); !private {
		t.Fatal("GORUN_ALLOW_PRIVATE_NET should lift the block")
	}
}

func TestSameOrigin(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080/", true},
		{"https://Site.Example/path", "https://site.example", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8081", false},
		{"http://site.example", "https://site.example", false},
		{"http://site.example", "", false},
		{"", "", false},
		{"ftp://site.example", "ftp://site.example", false},
	} {
		if got := sameOrigin(c.a, c.b); got != c.want {
			t.Errorf("sameOrigin(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
