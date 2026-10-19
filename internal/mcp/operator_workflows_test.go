package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOperatorWorkflowsResource(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	res, err := a.cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "gorun://operator-workflows"})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) != 1 || !strings.Contains(res.Contents[0].Text, `"morning_ops"`) {
		t.Fatalf("unexpected resource body: %v", res.Contents)
	}
	if !strings.Contains(res.Contents[0].Text, "canvases.append_result") {
		t.Fatal("playbook should reference canvas MCP tools")
	}
}
