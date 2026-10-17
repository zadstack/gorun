package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// A "*" key does not imply mcp.approve_self: it must be listed explicitly.
func TestWildcardKeyStillNeedsApproval(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("*")
	it := a.ok("create_content", map[string]any{"type": "post", "title": "Wildcard"})
	res := a.ok("publish_content", map[string]any{"id": str(it, "id")})
	if str(res, "status") != "pending_approval" {
		t.Fatalf("* key must not self-approve: %v", res)
	}
	svc, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	if got, _ := svc.Get(context.Background(), str(it, "id")); got.Status != "draft" {
		t.Fatalf("published without approval: %s", got.Status)
	}
}

// The requesting key can never decide its own approval over REST.
func TestKeyCannotDecideOwnApproval(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("*")
	aid := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	for _, action := range []string{"approve", "deny"} {
		if status, _ := a.key.Post("/v1/mcp/approvals/"+aid+"/"+action, nil); status != http.StatusForbidden {
			t.Fatalf("self %s: %d", action, status)
		}
	}
	if e.fake.calls.Load() != 0 {
		t.Fatal("self-approved call executed")
	}
	if got := a.ok("check_approval", map[string]any{"approval_id": aid}); str(got, "status") != "pending_approval" {
		t.Fatalf("approval decided by its own key: %v", got)
	}
	// Another key holding mcp.approve may decide.
	if status, _ := apitest.NewClient(t, e.app, "mcp.approve").Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusOK {
		t.Fatalf("other key approve: %d", status)
	}
	if e.fake.calls.Load() != 1 {
		t.Fatalf("calls = %d", e.fake.calls.Load())
	}
}

func TestApprovalsExpire(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	aid := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	if _, err := e.app.Deps.DB.Exec(`UPDATE mcp_approvals SET expires_at = '2000-01-01T00:00:00Z' WHERE id = ?`, aid); err != nil {
		t.Fatal(err)
	}
	if got := healthLine(t, e); got.Status != admin.CheckWarn {
		t.Fatalf("stale pending approval must warn: %+v", got)
	}
	s, _ := kernel.Get[*server](e.app.Deps, serviceName)
	if err := s.approvals.expire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := a.ok("check_approval", map[string]any{"approval_id": aid}); str(got, "status") != "expired" {
		t.Fatalf("check_approval after expiry: %v", got)
	}
	if status, _ := apitest.NewClient(t, e.app, "mcp.approve").Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusOK {
		t.Fatalf("approve expired: %d", status)
	}
	if e.fake.calls.Load() != 0 {
		t.Fatal("expired approval executed")
	}
	if got := healthLine(t, e); got.Status != admin.CheckOK {
		t.Fatalf("health after expiry: %+v", got)
	}
}

func TestHealthCheckPendingApprovals(t *testing.T) {
	e := newEnv(t, nil)
	e.agent("content.view").ok("fake_sensitive", map[string]any{})
	got := healthLine(t, e)
	if got.Status != admin.CheckOK || got.Detail == "" {
		t.Fatalf("health line: %+v", got)
	}
}

func healthLine(t *testing.T, e *env) admin.HealthCheck {
	t.Helper()
	list := []admin.HealthCheck{}
	out, err := e.app.Deps.Hooks.Apply(context.Background(), admin.HealthCheckFilter, &list)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range *out.(*[]admin.HealthCheck) {
		if c.Name == "mcp_approvals" {
			return c
		}
	}
	t.Fatal("no mcp_approvals health check")
	return admin.HealthCheck{}
}
