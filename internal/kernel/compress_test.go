package kernel_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestRootHandlerCompressesTextResponses(t *testing.T) {
	app, err := kernel.BootWithOptions(context.Background(), testConfig(t), []kernel.Module{settingsmod.New()}, kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	r := httptest.NewRequest("GET", "/healthz", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, r)
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("root middleware should gzip behind the security headers: %v", rec.Header())
	}
	r = httptest.NewRequest("GET", "/admin/login", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	rec = httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, r)
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("admin responses stay uncompressed: %v", rec.Header())
	}
}
