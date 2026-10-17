package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStudioMCPExplicitDiscoveryThenSingleToolAndStatus(t *testing.T) {
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Fatal("request")
		}
		calls = append(calls, in.Method)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("missing credentials")
		}
		switch in.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}}}}`)
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/list":
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"render_movie","inputSchema":{"type":"object","required":["prompt"]}},{"name":"movie_status","inputSchema":{"type":"object"}}]}}`)
		case "tools/call":
			if r.Header.Get("Mcp-Session-Id") != "session" {
				t.Error("session missing")
			}
			if in.Params["name"] == "render_movie" {
				args := in.Params["arguments"].(map[string]any)
				if args["prompt"] != "reviewed prompt" {
					t.Error("wrong arguments")
				}
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"structuredContent":{"job":"job-1","status":"queued"}}}`)
			} else if in.Params["name"] == "movie_status" {
				fmt.Fprint(w, `{"jsonrpc":"2.0","id":2,"result":{"structuredContent":{"status":"done","url":"https://media.example.com/result.mp4"}}}`)
			} else {
				t.Error("unselected tool")
			}
		default:
			t.Error("unexpected operation")
		}
	}))
	defer srv.Close()
	session, e := openStudioMCP(context.Background(), srv.Client(), connectionRecord{Endpoint: srv.URL}, connectionSecrets{Token: "private-token"})
	if e != nil {
		t.Fatal(e)
	}
	listed, e := session.tools(context.Background())
	if e != nil || len(listed) != 2 {
		t.Fatal(e)
	}
	if strings.Contains(strings.Join(calls, ","), "tools/call") {
		t.Fatal("discovery executed a tool")
	}
	b := studioBinding{Kind: "video", JobPath: "structuredContent.job", StatusPath: "structuredContent.status", ReadyValue: "done", FailedValue: "failed", URLPath: "structuredContent.url"}
	raw, e := session.call(context.Background(), "render_movie", map[string]any{"prompt": "reviewed prompt"})
	if e != nil {
		t.Fatal(e)
	}
	out, e := studioMCPOutput(b, raw)
	if e != nil || out.JobID != "job-1" || out.Status != "queued" {
		t.Fatalf("%+v %v", out, e)
	}
	raw, e = session.call(context.Background(), "movie_status", map[string]any{"job": "job-1"})
	if e != nil {
		t.Fatal(e)
	}
	out, e = studioMCPOutput(b, raw)
	if e != nil || out.Status != "done" || out.URL == "" {
		t.Fatalf("%+v %v", out, e)
	}
	if len(calls) != 5 {
		t.Fatalf("unexpected calls %v", calls)
	}
}
func TestStudioImageUsesExplicitModelAndSanitizedErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		_ = json.NewDecoder(r.Body).Decode(&v)
		if r.URL.Path != "/images/generations" || v["model"] != "configured-image" || v["prompt"] != "current article" || v["n"] != float64(1) {
			t.Errorf("request %s %#v", r.URL.Path, v)
		}
		fmt.Fprint(w, `{"data":[{"b64_json":"aW1hZ2U="}]}`)
	}))
	defer srv.Close()
	out, e := studioImage(context.Background(), srv.Client(), aiConfig{BaseURL: srv.URL}, "configured-image", "current article")
	if e != nil || string(out.Bytes) != "image" {
		t.Fatalf("%+v %v", out, e)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `private-provider-secret`)
	}))
	defer bad.Close()
	_, e = studioImage(context.Background(), bad.Client(), aiConfig{BaseURL: bad.URL}, "m", "p")
	if e == nil || strings.Contains(e.Error(), "private-provider-secret") {
		t.Fatal("error leak")
	}
}
func TestStudioDownloadRejectsPrivateCredentialsSchemesAndRedirects(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "http://example.com/x", "https://user:secret@example.com/x", "https://127.0.0.1/x", "https://169.254.169.254/x", "https://[::1]/x"} {
		if _, e := studioFetch(context.Background(), studioSafeClient(), u); e == nil {
			t.Errorf("accepted %s", u)
		}
	}
}
