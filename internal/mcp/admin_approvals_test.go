package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
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
