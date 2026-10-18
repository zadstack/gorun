package admin_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

func studioJSON(t *testing.T, s *site, path string, in any, csrf bool) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", s.srv.URL+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-CSRF-Token", s.csrf())
	}
	resp, e := s.c.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}
func studioPost(t *testing.T, s *site, u *auth.User) *content.Item {
	t.Helper()
	svc, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	it, e := svc.Create(auth.WithUser(context.Background(), u), content.Input{Type: "post", Title: "Saved article", Body: "Original saved body", Status: "draft", AuthorID: u.ID})
	if e != nil {
		t.Fatal(e)
	}
	return it
}
func TestStudioNativeTextUsesUnsavedSourceAndNeverPublishes(t *testing.T) {
	s := boot(t, content.New())
	u := s.login("studio@example.com", "admin", "en")
	it := studioPost(t, s, u)
	fake := newFakeAI(t, 200, openAIReply("A candidate draft."), func(t *testing.T, r *http.Request, b map[string]any) {
		raw, _ := json.Marshal(b)
		if !strings.Contains(string(raw), "Unsaved article facts") || !strings.Contains(string(raw), "social draft") {
			t.Error("current source missing")
		}
	})
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {fake.srv.URL}, "model": {"test-model"}})
	input := map[string]any{"provider": "native-text", "kind": "text", "prompt": "social draft", "text": "Unsaved article facts", "title": "Unsaved title", "reviewed": true}
	path := "/admin/x/studio/" + it.ID
	if code, _ := studioJSON(t, s, path+"/generate", input, false); code != 403 {
		t.Fatalf("csrf %d", code)
	}
	code, preview := studioJSON(t, s, path+"/preview", input, true)
	if code != 200 {
		t.Fatalf("preview %d", code)
	}
	input["request_id"] = preview["request_id"]
	input["review_hash"] = preview["review_hash"]
	if fake.calls.Load() != 0 {
		t.Fatal("preview executed provider")
	}
	code, out := studioJSON(t, s, path+"/generate", input, true)
	if code != 202 {
		t.Fatalf("generate %d %#v", code, out)
	}
	id := out["id"].(string)
	if code, retry := studioJSON(t, s, path+"/generate", input, true); code != 202 || retry["id"] != id {
		t.Fatalf("idempotency %d %#v", code, retry)
	}
	var status string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if e := s.app.Deps.DB.QueryRow(`SELECT status FROM admin_studio_candidates WHERE id=?`, id).Scan(&status); e != nil {
			t.Fatal(e)
		}
		if status == "ready" || status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != "ready" || fake.calls.Load() != 1 {
		t.Fatalf("status %s calls %d", status, fake.calls.Load())
	}
	svc, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	saved, _ := svc.Get(context.Background(), it.ID)
	if saved.Body != "Original saved body" || saved.Status != "draft" {
		t.Fatal("studio changed article")
	}
	if code, _ := studioJSON(t, s, path+"/"+id+"/action", map[string]string{"action": "edit", "text": "Edited result"}, true); code != 200 {
		t.Fatal(code)
	}
	s.login("other@example.com", "editor", "en")
	if code, _ := studioJSON(t, s, path+"/"+id+"/action", map[string]string{"action": "remove"}, true); code != 404 {
		t.Fatalf("candidate ownership %d", code)
	}
	s.signIn("studio@example.com")
	if code, _ := studioJSON(t, s, path+"/"+id+"/action", map[string]string{"action": "remove"}, true); code != 200 {
		t.Fatal(code)
	}
}
func TestStudioNativeProviderLabelsDoNotClaimGeneration(t *testing.T) {
	s := boot(t, content.New())
	u := s.login("studio-labels@example.com", "admin", "en")
	it := studioPost(t, s, u)
	fake := newFakeAI(t, 200, openAIReply("unused"), nil)
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {fake.srv.URL}, "model": {"label-model"}})
	if err := s.app.Deps.Settings.Set(context.Background(), "ai.studio", map[string]any{"image_model": "dall-e-test"}); err != nil {
		t.Fatal(err)
	}
	resp, body := s.do("GET", "/admin/x/studio/"+it.ID, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list %d", resp.StatusCode)
	}
	mustContain(t, body, "Saved writing provider · generation not verified", "Saved image model · generation not verified: dall-e-test")
	mustNotContain(t, body, "Configured writing provider", `"Configured image model`)
}

func TestStudioContentOwnershipAndUnconfiguredCapabilities(t *testing.T) {
	s := boot(t, content.New())
	u := s.login("owner@example.com", "admin", "en")
	it := studioPost(t, s, u)
	resp, body := s.do("GET", "/admin/settings/studio", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "Tool contracts") {
		t.Fatalf("settings %d", resp.StatusCode)
	}
	s.login("author@example.com", "author", "en")
	resp, _ = s.do("GET", "/admin/x/studio/"+it.ID, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("foreign content %d", resp.StatusCode)
	}
	s.signIn("owner@example.com")
	in := map[string]any{"provider": "native-image", "kind": "image", "prompt": "Generate", "reviewed": true}
	if code, _ := studioJSON(t, s, "/admin/x/studio/"+it.ID+"/generate", in, true); code != 400 {
		t.Fatalf("unconfigured image %d", code)
	}
	if code, _ := studioJSON(t, s, "/admin/settings/studio", map[string]any{"bindings": []any{map[string]any{"id": "danger", "name": "Tool", "kind": "video", "connection_id": it.ID, "tool": "unknown", "arguments": map[string]any{}, "url_path": "structuredContent.url"}}}, true); code != 400 {
		t.Fatalf("undiscovered tool %d", code)
	}
}

func TestStudioMCPPreviewPinsDiscoveredConnectionDestination(t *testing.T) {
	s := boot(t, content.New())
	u := s.login("owner@example.com", "admin", "en")
	it := studioPost(t, s, u)
	if resp, _ := s.post("/admin/settings/connections", url.Values{"name": {"Renderer"}, "kind": {"mcp"}, "endpoint": {"https://render.example.com/mcp"}, "token": {"private-secret"}}); resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	row := savedConnection(t, s)
	id := row["id"].(string)
	if e := s.app.Deps.Settings.Set(context.Background(), "ai.studio.discovery."+id, map[string]any{"version": row["version"], "tools": []any{map[string]any{"name": "create_draft", "inputSchema": map[string]any{"type": "object"}}}}); e != nil {
		t.Fatal(e)
	}
	cfg := map[string]any{"bindings": []any{map[string]any{"id": "draft", "name": "Renderer draft", "kind": "text", "connection_id": id, "tool": "create_draft", "arguments": map[string]any{"prompt": "{{prompt}}"}}}}
	if code, _ := studioJSON(t, s, "/admin/settings/studio", cfg, true); code != 200 {
		t.Fatalf("save %d", code)
	}
	input := map[string]any{"provider": "draft", "kind": "text", "prompt": "reviewed task", "text": "Current article"}
	path := "/admin/x/studio/" + it.ID + "/preview"
	code, out := studioJSON(t, s, path, input, true)
	if code != 200 {
		t.Fatalf("preview %d", code)
	}
	args := out["arguments"].(map[string]any)
	if args["endpoint"] != "https://render.example.com/mcp" || args["connection_version"] != row["version"] {
		t.Fatalf("missing destination %#v", args)
	}
	row["version"] = "changed"
	row["endpoint"] = "https://different.example.com/mcp"
	if e := s.app.Deps.Settings.Set(context.Background(), "connections.profile."+id, row); e != nil {
		t.Fatal(e)
	}
	if code, _ := studioJSON(t, s, path, input, true); code != 400 {
		t.Fatalf("changed destination accepted %d", code)
	}
}

func TestStudioImageImportsOrganizedMediaAndRemovalKeepsIt(t *testing.T) {
	s := boot(t, content.New(), media.New())
	u := s.login("image@example.com", "admin", "en")
	it := studioPost(t, s, u)
	var pngBytes bytes.Buffer
	if e := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 8, 8))); e != nil {
		t.Fatal(e)
	}
	reply, _ := json.Marshal(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(pngBytes.Bytes())}}})
	fake := newFakeAI(t, 200, string(reply), func(t *testing.T, r *http.Request, b map[string]any) {
		if r.URL.Path != "/images/generations" || b["model"] != "image-model" {
			t.Errorf("wrong image request %s %#v", r.URL.Path, b)
		}
	})
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {fake.srv.URL}, "model": {"text-model"}})
	if code, _ := studioJSON(t, s, "/admin/settings/studio", map[string]any{"image_model": "image-model"}, true); code != 200 {
		t.Fatal(code)
	}
	in := map[string]any{"provider": "native-image", "kind": "image", "prompt": "Article cover", "text": "Unsaved facts", "reviewed": true}
	path := "/admin/x/studio/" + it.ID
	code, preview := studioJSON(t, s, path+"/preview", in, true)
	if code != 200 {
		t.Fatal(code)
	}
	in["request_id"] = preview["request_id"]
	in["review_hash"] = preview["review_hash"]
	code, out := studioJSON(t, s, path+"/generate", in, true)
	if code != 202 {
		t.Fatal(code)
	}
	id := out["id"].(string)
	var status, doc string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if e := s.app.Deps.DB.QueryRow(`SELECT status,document FROM admin_studio_candidates WHERE id=?`, id).Scan(&status, &doc); e != nil {
			t.Fatal(e)
		}
		if status == "ready" || status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != "ready" {
		t.Fatalf("image status %s %s", status, doc)
	}
	var stored struct {
		Candidate struct {
			MediaID string `json:"media_id"`
		} `json:"candidate"`
	}
	if json.Unmarshal([]byte(doc), &stored) != nil {
		t.Fatal("document")
	}
	svc, _ := kernel.Get[media.Service](s.app.Deps, kernel.ServiceMedia)
	m, e := svc.Get(context.Background(), stored.Candidate.MediaID)
	if e != nil || !strings.Contains(m.StorageKey, "/content/"+strings.ToLower(it.ID)+"/") || m.SourceURL != "" {
		t.Fatalf("media %#v %v", m, e)
	}
	if code, _ := studioJSON(t, s, path+"/"+id+"/action", map[string]string{"action": "remove"}, true); code != 200 {
		t.Fatal(code)
	}
	if _, e = svc.Get(context.Background(), m.ID); e != nil {
		t.Fatal("candidate removal deleted media")
	}
}
