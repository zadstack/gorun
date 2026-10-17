package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

func TestAuditFilterByActor(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	a := s.createUser("alice@example.com", "editor", "")
	b := s.createUser("bob@example.com", "editor", "")
	ctx := context.Background()
	s.app.Deps.Audit.Record(auth.WithUser(ctx, a), "test.alpha", "thing", "1", map[string]string{"note": "from-alice"})
	s.app.Deps.Audit.Record(auth.WithUser(ctx, b), "test.beta", "thing", "2", nil)
	s.app.Deps.Audit.Record(ctx, "test.gamma", "thing", "3", nil) // system actor

	resp, page := s.do("GET", "/admin/agents/audit?actor_id="+url.QueryEscape(a.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit: %d", resp.StatusCode)
	}
	mustContain(t, page, "test.alpha", "&#34;note&#34;: &#34;from-alice&#34;")
	mustNotContain(t, page, "test.beta", "test.gamma")

	_, page = s.do("GET", "/admin/agents/audit?actor_kind=system&action=test.", nil)
	mustContain(t, page, "test.gamma")
	mustNotContain(t, page, "test.alpha", "test.beta")

	_, page = s.do("GET", "/admin/agents/audit?action=test.al", nil)
	mustContain(t, page, "test.alpha")
	mustNotContain(t, page, "test.beta")

	// "%" and "_" in the prefix are literals.
	_, page = s.do("GET", "/admin/agents/audit?action=test_", nil)
	mustNotContain(t, page, "test.alpha")

	_, page = s.do("GET", "/admin/agents/audit?until=2000-01-01", nil)
	mustNotContain(t, page, "test.alpha")
	_, page = s.do("GET", "/admin/agents/audit?since=bad", nil)
	mustContain(t, page, `role="alert"`)
}
