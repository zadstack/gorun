package mcp

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/api/apitest"
)

// headers adds fixed request headers on top of the bearer transport.
type headers struct {
	h    map[string]string
	base http.RoundTripper
}

func (x headers) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range x.h {
		r.Header.Set(k, v)
	}
	return x.base.RoundTrip(r)
}

// agentAt connects key to /mcp with a query string and extra headers.
func (e *env) agentAt(key *apitest.Client, query string, hdr map[string]string) *agent {
	e.t.Helper()
	tr := &sdk.StreamableClientTransport{Endpoint: e.srv.URL + "/mcp" + query, DisableStandaloneSSE: true, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: headers{h: hdr, base: bearer{token: key.Token, base: http.DefaultTransport}}}}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "v0"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		e.t.Fatalf("connect %s: %v", query, err)
	}
	e.t.Cleanup(func() { cs.Close() })
	return &agent{t: e.t, cs: cs, key: key}
}

func TestToolsetsAndReadOnlyNarrowTheList(t *testing.T) {
	e := newEnv(t, nil)
	key := apitest.NewClient(t, e.app, "*")
	all := e.agentFor(key).toolNames()

	// Default: every tool the key may call, exactly as before toolsets existed.
	var registered []string
	for _, tl := range e.app.Deps.MCP.Tools() {
		registered = append(registered, tl.Name)
	}
	slices.Sort(registered)
	sorted := slices.Sorted(slices.Values(all))
	if !slices.Equal(sorted, registered) {
		t.Fatalf("default list differs from the registered tools:\n%v\n%v", sorted, registered)
	}

	media := e.agentAt(key, "?toolsets=media", nil)
	got := media.toolNames()
	slices.Sort(got)
	if want := []string{"check_approval", "get_media", "get_site_structure", "list_media", "update_media", "upload_media"}; !slices.Equal(got, want) {
		t.Fatalf("?toolsets=media: %v", got)
	}
	if errObj := media.fails("list_content", map[string]any{}); str(errObj, "code") != "forbidden" || !strings.Contains(str(errObj, "message"), "hidden") {
		t.Fatalf("hidden tool call: %v", errObj)
	}
	media.ok("get_site_structure", nil) // always visible

	byHeader := e.agentAt(key, "", map[string]string{"X-MCP-Toolsets": "content, taxonomy"}).toolNames()
	if !contains(byHeader, "create_content") || !contains(byHeader, "list_terms") || contains(byHeader, "upload_media") {
		t.Fatalf("X-MCP-Toolsets: %v", byHeader)
	}

	ro := e.agentAt(key, "?readonly=1", nil)
	res, err := ro.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("readonly list is empty")
	}
	for _, tl := range res.Tools {
		if !tl.Annotations.ReadOnlyHint {
			t.Errorf("readonly list has %s", tl.Name)
		}
	}
	if errObj := ro.fails("create_content", map[string]any{"type": "post", "title": "x"}); str(errObj, "code") != "forbidden" {
		t.Fatalf("readonly write: %v", errObj)
	}
	if n := e.count(`SELECT count(*) FROM content WHERE title = 'x'`); n != 0 {
		t.Fatalf("hidden write ran: %d rows", n)
	}
	hdrRO := e.agentAt(key, "?toolsets=content", map[string]string{"X-MCP-Readonly": "1"}).toolNames()
	if contains(hdrRO, "create_content") || !contains(hdrRO, "list_content") || !contains(hdrRO, "check_approval") {
		t.Fatalf("toolsets + X-MCP-Readonly: %v", hdrRO)
	}

	// Scopes still apply inside a view: always-visible tools need their scope.
	if names := e.agentAt(apitest.NewClient(t, e.app, "comments.moderate"), "?toolsets=media", nil).toolNames(); !slices.Equal(names, []string{"check_approval"}) {
		t.Fatalf("scoped view: %v", names)
	}

	for _, bad := range []string{"?toolsets=nope", "?readonly=maybe"} {
		req, _ := http.NewRequest("POST", e.srv.URL+"/mcp"+bad, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Authorization", "Bearer "+key.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), `"jsonrpc":"2.0"`) {
			t.Fatalf("%s: %d %s", bad, resp.StatusCode, b)
		}
	}
}

func TestStdioToolsetOptions(t *testing.T) {
	e := newEnv(t, nil)
	key := apitest.NewClient(t, e.app, "*")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	go func() {
		_ = run(ctx, e.app, key.Token, &sdk.IOTransport{Reader: sr, Writer: sw}, []Options{{Toolsets: []string{"taxonomy"}, ReadOnly: true}})
	}()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "stdio-test", Version: "v0"}, nil).Connect(ctx, &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	got := (&agent{t: t, cs: cs, key: key}).toolNames()
	slices.Sort(got)
	if want := []string{"check_approval", "get_site_structure", "list_taxonomies", "list_terms"}; !slices.Equal(got, want) {
		t.Fatalf("stdio --toolsets taxonomy --readonly: %v", got)
	}
	if err := ValidateOptions(Options{Toolsets: []string{"contents"}}); err == nil {
		t.Fatal("unknown toolset accepted")
	}
}
