package admin_test

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/diagnostics"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestErrorInventoryShowsCollectorProvenance(t *testing.T) {
	s := boot(t)
	s.login("collector-fixture@example.invalid", "admin", "en")
	s.app.Deps.Log.Error("mailer: fixture failure", "payload", "private-fixture")
	c, ok := kernel.Get[*diagnostics.Collector](s.app.Deps, diagnostics.ServiceName)
	if !ok {
		t.Fatal("collector absent")
	}
	c.Close()
	response, body := s.do("GET", "/admin/system/errors", nil)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	for _, marker := range []string{"data-collector-read-at", "data-collector-last-success", "data-collector-dropped", "data-collector-storage-failures", "Counters from the current process"} {
		if !strings.Contains(body, marker) {
			t.Errorf("collector provenance missing: %s", marker)
		}
	}
	if strings.Contains(body, "private-fixture") {
		t.Fatal("private diagnostic payload leaked")
	}
}
