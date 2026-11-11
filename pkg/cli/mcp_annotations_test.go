package cli

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// Every tool of a fully booted build carries a title and consistent hints,
// and tools/list publishes them as MCP annotations.
func TestFullModulesMCPToolAnnotations(t *testing.T) {
	app, srv := bootFull(t)
	tools := app.Deps.MCP.Tools()
	if len(tools) < 52 {
		t.Fatalf("expected the full tool set, got %d tools", len(tools))
	}
	for _, tool := range tools {
		switch {
		case strings.TrimSpace(tool.Title) == "":
			t.Errorf("%s: no Title", tool.Name)
		case tool.Toolset == "" || !slices.Contains(kernel.MCPToolsets, tool.Toolset):
			t.Errorf("%s: toolset %q is not one of %v", tool.Name, tool.Toolset, kernel.MCPToolsets)
		case tool.ReadOnly && tool.Destructive:
			t.Errorf("%s: both ReadOnly and Destructive", tool.Name)
		case tool.ReadOnly && tool.Sensitive:
			t.Errorf("%s: ReadOnly tools never need approval", tool.Name)
		case strings.HasPrefix(tool.Name, "delete_") && !tool.Destructive:
			t.Errorf("%s: delete tools must be Destructive", tool.Name)
		case (strings.HasPrefix(tool.Name, "list_") || strings.HasPrefix(tool.Name, "get_")) && !tool.ReadOnly:
			t.Errorf("%s: list_/get_ tools must be ReadOnly", tool.Name)
		}
	}

	tr := &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: bearerRT{adminKey(t, app, auth.KeyKindAgent)}}}
	cl, err := sdk.NewClient(&sdk.Implementation{Name: "it", Version: "v0"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	res, err := cl.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*sdk.Tool{}
	for _, tl := range res.Tools {
		if tl.Annotations == nil || tl.Annotations.Title == "" || tl.Annotations.OpenWorldHint == nil {
			t.Errorf("%s: missing annotations %+v", tl.Name, tl.Annotations)
		}
		byName[tl.Name] = tl
	}
	if a := byName["list_content"].Annotations; !a.ReadOnlyHint || a.DestructiveHint != nil || *a.OpenWorldHint {
		t.Errorf("list_content annotations: %+v", a)
	}
	if a := byName["delete_content"].Annotations; a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
		t.Errorf("delete_content annotations: %+v", a)
	}
	if a := byName["upload_media"].Annotations; a.ReadOnlyHint || !*a.OpenWorldHint || *a.DestructiveHint {
		t.Errorf("upload_media annotations: %+v", a)
	}
}
