package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
)

func TestTemplateReadIncludesDraftWithoutPublishing(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("themes.manage", "mcp.approve_self")
	before := a.ok("get_template", map[string]any{"name": "single.html"})
	if before["draft"] != nil {
		t.Fatal("unexpected initial draft")
	}
	a.ok("create_template_draft", map[string]any{"name": "single.html", "body": "DRAFT {{.Kind}}"})
	got := a.ok("get_template", map[string]any{"name": "single.html"})
	if got["body"] != before["body"] || got["draft"] != "DRAFT {{.Kind}}" || got["source"] == "" {
		t.Fatalf("wrong sources: %v", got)
	}
	// Missing/null must preserve an existing draft; explicit empty must remain valid.
	for _, args := range []map[string]any{{"name": "single.html"}, {"name": "single.html", "body": nil}} {
		a.fails("create_template_draft", args)
		if after := a.ok("get_template", map[string]any{"name": "single.html"}); after["draft"] != got["draft"] {
			t.Fatal("invalid request erased draft")
		}
	}
	a.ok("create_template_draft", map[string]any{"name": "single.html", "body": ""})
	if after := a.ok("get_template", map[string]any{"name": "single.html"}); after["draft"] != "" || after["body"] != before["body"] {
		t.Fatal("empty draft semantics changed")
	}
	a.ok("create_template_draft", map[string]any{"name": "new-agent.html", "body": "NEW"})
	if only := a.ok("get_template", map[string]any{"name": "new-agent.html"}); only["draft"] != "NEW" || only["body"] != "" {
		t.Fatal("draft-only template not readable")
	}
	a.fails("get_template", map[string]any{"name": "missing.html"})
	a.fails("get_template", map[string]any{"name": "../secret"})
}

func TestToolDecodeRejectsTrailingArguments(t *testing.T) {
	calls := 0
	handler := bind(func(context.Context, mediaIDInput) (any, error) { calls++; return nil, nil })
	for _, raw := range []string{`{"id":"x"} {}`, `{"id":"x"} trailing`, `{"id":"x","unknown":true}`} {
		if _, err := handler(t.Context(), json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if calls != 0 {
		t.Fatal("invalid arguments executed handler")
	}
}

func TestMediaToolsMetadataApprovalAndScopes(t *testing.T) {
	e := newEnv(t, nil)
	uploader := e.agent("media.upload", "content.view", "mcp.approve_self")
	item := uploader.ok("upload_media", map[string]any{"filename": "contract.png", "data_base64": base64.StdEncoding.EncodeToString(tinyPNG(t)), "alt": "original", "title": "keep"})
	id := str(item, "id")
	reader := e.agent("content.view")
	got := reader.ok("get_media", map[string]any{"id": id})
	if got["mime"] != "image/png" || got["url"] == "" {
		t.Fatalf("missing media: %v", got)
	}
	if _, ok := got["variants"]; !ok {
		t.Fatal("missing variants")
	}
	if contains(reader.toolNames(), "update_media") {
		t.Fatal("reader offered mutation")
	}
	reader.fails("update_media", map[string]any{"id": id, "alt": "forbidden"})
	pending := e.agent("media.upload").ok("update_media", map[string]any{"id": id, "alt": "updated"})
	if str(pending, "status") != "pending_approval" {
		t.Fatalf("approval bypassed: %v", pending)
	}
	if reader.ok("get_media", map[string]any{"id": id})["alt"] != "original" {
		t.Fatal("changed before approval")
	}
	if status, _ := apitest.NewClient(t, e.app, "mcp.approve").Post("/v1/mcp/approvals/"+str(pending, "approval_id")+"/approve", nil); status != http.StatusOK {
		t.Fatalf("approve: %d", status)
	}
	got = reader.ok("get_media", map[string]any{"id": id})
	if got["alt"] != "updated" || got["title"] != "keep" {
		t.Fatal("patch did not preserve omitted title")
	}
	uploader.ok("update_media", map[string]any{"id": id, "alt": ""})
	if reader.ok("get_media", map[string]any{"id": id})["alt"] != "" {
		t.Fatal("clear failed")
	}
	uploader.fails("update_media", map[string]any{"id": id})
	reader.fails("get_media", map[string]any{"id": "missing"})
}

func TestTemplateDraftSchemaRequiresStringBody(t *testing.T) {
	raw, err := schemaFor(templateDraftInput{})
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err = json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	body := schema["properties"].(map[string]any)["body"].(map[string]any)
	if body["type"] != "string" {
		t.Fatalf("body schema must match non-null contract: %s", raw)
	}
}
