package mcp

import (
	"net/http"
	"slices"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
)

// Agent keys work on /mcp only; API keys work on both /api and /mcp.
func TestAgentKeyMCPOnly(t *testing.T) {
	e := newEnv(t, nil)
	agentKey := apitest.New(t, e.app, apitest.Options{Kind: auth.KeyKindAgent})
	if agentKey.Key.Kind != auth.KeyKindAgent {
		t.Fatalf("kind %q", agentKey.Key.Kind)
	}
	status, body := agentKey.Get("/v1/me")
	if status != http.StatusForbidden || apitest.ErrorCode(body) != "agent_key_not_allowed" {
		t.Fatalf("agent key on /api/v1/me: %d %v", status, body)
	}
	if status, _ := agentKey.Post("/v1/mcp/approvals/x/approve", nil); status != http.StatusForbidden {
		t.Fatalf("agent key must not reach the approvals API: %d", status)
	}
	if names := e.agentFor(agentKey).toolNames(); !slices.Contains(names, "list_content") {
		t.Fatalf("agent key on /mcp: tools %v", names)
	}

	apiKey := apitest.NewClient(t, e.app, "*")
	if status, _ := apiKey.Get("/v1/me"); status != http.StatusOK {
		t.Fatalf("api key on /api/v1/me: %d", status)
	}
	if names := e.agentFor(apiKey).toolNames(); !slices.Contains(names, "list_content") {
		t.Fatalf("api key on /mcp: tools %v", names)
	}
}
