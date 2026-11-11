package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestInstructionsAndGuideResource(t *testing.T) {
	if len(guide) > 2560 {
		t.Fatalf("guide is %d bytes; keep it under 2.5 KB", len(guide))
	}
	for _, must := range []string{"get_site_structure", "check_approval", "pending_approval", "per_page",
		"validation_failed", "fetch_failed", "rate_limited", "X-MCP-Toolsets", "readonly"} {
		if !strings.Contains(guide, must) {
			t.Errorf("guide does not mention %s", must)
		}
	}
	// Any key reads the guide: it needs no content scope.
	a := newEnv(t, nil).agent("comments.moderate")
	if got := a.cs.InitializeResult().Instructions; got != guide {
		t.Fatalf("instructions differ from the guide: %q", got)
	}
	res, err := a.cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "gorun://guide"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) != 1 || res.Contents[0].Text != guide || res.Contents[0].MIMEType != "text/markdown" {
		t.Fatalf("gorun://guide: %+v", res.Contents)
	}
}
