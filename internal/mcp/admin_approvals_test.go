package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

func TestArgListFlattensTopLevelArguments(t *testing.T) {
	got := argList(json.RawMessage(`{"title":"مسودة <b>","id":"01ABC","count":3,"tags":["a", "b"],"draft":true}`))
	want := []approvalArg{
		{Key: "count", Value: "3", Code: true},
		{Key: "draft", Value: "true", Code: true},
		{Key: "id", Value: "01ABC"},
		{Key: "tags", Value: `["a","b"]`, Code: true},
		{Key: "title", Value: "مسودة <b>"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argList = %#v", got)
	}
	for _, raw := range []string{``, `[1,2]`, `"text"`, `not json`} {
		if rows := argList(json.RawMessage(raw)); len(rows) != 0 {
			t.Fatalf("argList(%q) = %v, want none", raw, rows)
		}
	}
}

// The approvals page leads with a key/value list, keeps the raw JSON in a
// disclosure and isolates key and requester names for Arabic layouts.
func TestApprovalsPageShowsArgumentsAsKeyValues(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("content.view")
	a.ok("fake_sensitive", map[string]any{"note": "<script>alert(1)</script>"})
	e.login("admin")
	_, page := e.do("GET", "/admin/agents/approvals", nil)
	for _, want := range []string{`<dl class="kv">`, `<dt><code dir="ltr">note</code></dt>`, `<dd><bdi>&lt;script&gt;alert(1)&lt;/script&gt;</bdi></dd>`, `<details class="raw-detail">`, "<bdi>", `class="page-header"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("approvals page missing %q", want)
		}
	}
}

// A pending call whose tool can describe itself shows what it would change
// now; a preview that fails says so instead of hiding the call.
func TestApprovalsPageShowsToolPreview(t *testing.T) {
	e := newEnv(t, nil)
	e.app.Deps.MCP.AddTool(kernel.MCPTool{Name: "fake_previewed", Description: "previews", Scopes: []string{"content.view"}, Sensitive: true,
		Input: struct {
			Note string `json:"note,omitempty"`
		}{},
		Handler: func(context.Context, json.RawMessage) (any, error) { return map[string]any{}, nil },
		Preview: func(_ context.Context, args json.RawMessage) (string, error) {
			if strings.Contains(string(args), "boom") {
				return "", errors.New("boom")
			}
			return "~ hero h1 title: Old <b> → New", nil
		}})
	a := e.agent("content.view")
	a.ok("fake_previewed", map[string]any{"note": "fine"})
	a.ok("fake_previewed", map[string]any{"note": "boom"})
	a.ok("fake_sensitive", map[string]any{"note": "plain"})
	e.login("admin")
	_, page := e.do("GET", "/admin/agents/approvals", nil)
	for _, want := range []string{`<pre class="approval-preview" dir="auto">~ hero h1 title: Old &lt;b&gt; → New</pre>`, `<p class="field-help text-red">`} {
		if !strings.Contains(page, want) {
			t.Fatalf("approvals page missing %q", want)
		}
	}
	if strings.Count(page, "data-approval-preview") != 2 {
		t.Fatalf("preview blocks = %d, want 2 (the plain tool has none)", strings.Count(page, "data-approval-preview"))
	}
}
