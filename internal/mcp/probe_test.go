package mcp

import (
	"context"
	"slices"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// The admin "Test connection" probe sees exactly what a client on /mcp sees
// with the same key, without an HTTP round trip.
func TestProbeMatchesHTTPToolsList(t *testing.T) {
	e := newEnv(t, nil)
	probe, ok := kernel.Get[admin.MCPProbe](e.app.Deps, admin.ServiceMCPProbe)
	if !ok {
		t.Fatal("mcp module must register the admin probe")
	}
	key := apitest.New(t, e.app, apitest.Options{Kind: auth.KeyKindAgent, Scopes: []string{"content.view"}})
	principal, err := auth.WithAPIKeyActor(context.Background(), e.app.Deps, key.Key.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := probe.Probe(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerName != serverName || got.ServerVersion != serverVersion || got.ProtocolVersion == "" {
		t.Fatalf("server info: %+v", got)
	}
	want := e.agentFor(key).toolNames()
	slices.Sort(want)
	if !slices.Equal(got.Tools, want) {
		t.Fatalf("probe tools %v\n/mcp tools %v", got.Tools, want)
	}
	if !slices.Contains(got.Tools, "list_content") || !slices.Contains(got.Tools, "fake_sensitive") {
		t.Fatalf("content.view tools missing: %v", got.Tools)
	}
	if got.Scoped == 0 || !slices.Contains(got.Toolsets, kernel.MCPToolsetContent) || !slices.IsSorted(got.Toolsets) {
		t.Fatalf("toolsets %v scoped %d", got.Toolsets, got.Scoped)
	}

	// A key whose scopes reach no tool still sees the scope-free ones only.
	bare := apitest.New(t, e.app, apitest.Options{Kind: auth.KeyKindAgent, Scopes: []string{PermApproveSelf}})
	principal, err = auth.WithAPIKeyActor(context.Background(), e.app.Deps, bare.Key.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err = probe.Probe(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scoped != 0 {
		t.Fatalf("scope-less key sees scoped tools: %v", got.Tools)
	}
}
