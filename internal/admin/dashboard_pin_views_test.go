package admin_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func TestPinnedMetricoolHistoryIsPrivateAndDoesNotClaimPublishing(t *testing.T) {
	s := boot(t, content.New())
	owner := s.login("metricool-pin@example.test", "admin", "en")
	for id, row := range map[string]map[string]string{
		"own":   {"actor": owner.ID, "status": "confirmed", "created_at": "2026-10-12T01:00:00Z", "hash": "DO_NOT_EXPOSE_HASH", "provider_id": "DO_NOT_EXPOSE_PROVIDER"},
		"other": {"actor": "someone-else", "status": "uncertain", "created_at": "2099-01-01T00:00:00Z", "hash": "OTHER_PRIVATE_HASH"},
	} {
		if err := s.app.Deps.Settings.Set(context.Background(), "metricool.draft."+id, row); err != nil {
			t.Fatal(err)
		}
	}
	if r, _ := s.post("/admin/dashboard/pins", url.Values{"nav_id": {"content.metricool"}}); r.StatusCode != 200 {
		t.Fatal("pin", r.StatusCode)
	}
	r, page := s.do("GET", "/admin", nil)
	if r.StatusCode != 200 {
		t.Fatal("dashboard", r.StatusCode)
	}
	mustContain(t, page, `data-pin-id="content.metricool"`, "Draft confirmed", "not published", "2026-10-12T01:00:00Z")
	mustNotContain(t, page, "DO_NOT_EXPOSE_", "OTHER_PRIVATE_HASH", "2099-01-01")
	s.login("metricool-pin-other@example.test", "admin", "en")
	_, page = s.do("GET", "/admin", nil)
	if strings.Contains(page, `data-pin-id="content.metricool"`) {
		t.Fatal("other account saw pin")
	}
}
