package api

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

// A panic while building the router must leave a working fallback that
// answers with the 500 envelope, on the first and every later request.
func TestBuildPanicLeavesFallback(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	logger.Store(discard)
	t.Cleanup(func() { logger.Store(nil) })
	s := &server{deps: &kernel.Deps{Log: discard}} // nil API registry: Operations panics
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.serveOps(w, httptest.NewRequest("GET", "/api/v1/x", nil))
		if w.Code != 500 || !strings.Contains(w.Body.String(), `"code":"internal_error"`) {
			t.Fatalf("request %d: %d %s", i+1, w.Code, w.Body)
		}
	}
}
