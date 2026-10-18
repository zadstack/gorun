package mcp

import (
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
)

// Approvals by API key need a different person: another key of the
// requester's owner is refused unless GORUN_MCP_SAME_OWNER_APPROVAL is set.
// A session user (the human in the loop) may still approve.
func TestApprovalByKeyRequiresADifferentOwner(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	sameOwner := apitest.New(t, e.app, apitest.Options{Scopes: []string{"mcp.approve"}, User: a.key.User})
	other := apitest.NewClient(t, e.app, "mcp.approve")

	aid := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	if status, body := sameOwner.Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusForbidden {
		t.Fatalf("same owner approved: %d %v", status, body)
	}
	if status, _ := sameOwner.Post("/v1/mcp/approvals/"+aid+"/deny", nil); status != http.StatusForbidden {
		t.Fatalf("same owner denied: %d", status)
	}
	if e.fake.calls.Load() != 0 {
		t.Fatal("tool ran on a same-owner approval")
	}
	if status, _ := other.Post("/v1/mcp/approvals/"+aid+"/approve", nil); status != http.StatusOK || e.fake.calls.Load() != 1 {
		t.Fatalf("another person's key: %d, calls %d", status, e.fake.calls.Load())
	}

	// Rows parked before requested_by existed fall back to the key's owner.
	legacy := str(a.ok("fake_sensitive", map[string]any{}), "approval_id")
	if _, err := e.app.Deps.DB.Exec(`UPDATE mcp_approvals SET requested_by = '' WHERE id = ?`, legacy); err != nil {
		t.Fatal(err)
	}
	if status, _ := sameOwner.Post("/v1/mcp/approvals/"+legacy+"/approve", nil); status != http.StatusForbidden {
		t.Fatalf("same owner approved a legacy row: %d", status)
	}

	// Explicit opt-out for single-operator sites.
	e.app.Deps.Config.MCPSameOwnerApproval = true
	if status, _ := sameOwner.Post("/v1/mcp/approvals/"+legacy+"/approve", nil); status != http.StatusOK || e.fake.calls.Load() != 2 {
		t.Fatalf("GORUN_MCP_SAME_OWNER_APPROVAL: %d, calls %d", status, e.fake.calls.Load())
	}
	// The requesting key itself never decides, opt-out or not.
	self := apitest.New(t, e.app, apitest.Options{Scopes: []string{"content.view", "mcp.approve"}, User: a.key.User})
	selfAgent := e.agentFor(self)
	own := str(selfAgent.ok("fake_sensitive", map[string]any{}), "approval_id")
	if status, _ := self.Post("/v1/mcp/approvals/"+own+"/approve", nil); status != http.StatusForbidden {
		t.Fatalf("requesting key approved itself: %d", status)
	}
}
