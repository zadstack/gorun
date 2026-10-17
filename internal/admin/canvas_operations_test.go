package admin_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestCanvasOperationalGraphPersistsWithoutRunning(t *testing.T) {
	s := boot(t)
	s.login("operations@example.com", "admin", "en")
	path := newCanvas(t, s)
	prompt := map[string]any{"id": "11111111-1111-4111-8111-111111111111", "kind": "prompt", "title": "Summarize", "body": "Prepare a sourced draft", "x": 0, "y": 0}
	agent := map[string]any{"id": "22222222-2222-4222-8222-222222222222", "kind": "provider", "title": "Writer", "provider_id": "native-text", "x": 300, "y": 0}
	in := canvasInput(prompt, agent)
	in["edges"] = []map[string]string{{"from": prompt["id"].(string), "to": agent["id"].(string)}}
	if code, out := studioJSON(t, s, path+"/document", in, true); code != 200 {
		t.Fatalf("save operational graph: %d %v", code, out)
	}
	var n int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvas_runs`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("saving ran provider: %d %v", n, err)
	}
	doc := canvasGet(t, s, path)
	if len(doc["edges"].([]any)) != 1 {
		t.Fatal("edges not persisted")
	}
	if code, _ := studioJSON(t, s, path+"/operations/preview", map[string]any{"node_id": agent["id"], "version": 2}, true); code != 422 {
		t.Fatalf("unconfigured provider should be unavailable: %d", code)
	}
}

func TestCanvasOperationalOwnershipAndCSRF(t *testing.T) {
	s := boot(t)
	owner := s.login("ops-owner@example.com", "admin", "en")
	path := newCanvas(t, s)
	for _, endpoint := range []string{"preview", "runs", "schedules", "grants"} {
		if code, _ := studioJSON(t, s, path+"/operations/"+endpoint, map[string]any{}, false); code != 403 {
			t.Fatalf("CSRF %s %d", endpoint, code)
		}
	}
	s.login("ops-other@example.com", "admin", "en")
	if r, _ := s.do("GET", path+"/operations/options", nil); r.StatusCode != 404 {
		t.Fatalf("other owner: %d", r.StatusCode)
	}
	s.signIn(owner.Email)
	if r, _ := s.do("GET", path+"/operations/options", nil); r.StatusCode != 200 {
		t.Fatalf("own operations: %d", r.StatusCode)
	}
}

func TestCanvasProviderRunPreservesConcurrentHumanCards(t *testing.T) {
	s := boot(t)
	s.login("canvas-run@example.com", "admin", "en")
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	fake := newFakeAI(t, 200, openAIReply("Result draft only"), func(t *testing.T, r *http.Request, body map[string]any) { close(entered); <-release })
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {fake.srv.URL}, "model": {"test-model"}})
	path := newCanvas(t, s)
	prompt := map[string]any{"id": "11111111-1111-4111-8111-111111111111", "kind": "prompt", "body": "Summarize cautiously", "x": 0, "y": 0}
	provider := map[string]any{"id": "22222222-2222-4222-8222-222222222222", "kind": "provider", "title": "Writer", "provider_id": "native-text", "x": 300, "y": 0}
	input := canvasInput(prompt, provider)
	input["edges"] = []map[string]string{{"from": prompt["id"].(string), "to": provider["id"].(string)}}
	if code, out := studioJSON(t, s, path+"/document", input, true); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	code, preview := studioJSON(t, s, path+"/operations/preview", map[string]any{"version": 2, "node_id": provider["id"]}, true)
	if code != 200 {
		t.Fatalf("preview %d %v", code, preview)
	}
	if fake.calls.Load() != 0 {
		t.Fatal("preview called provider")
	}
	request := map[string]any{"request_id": "33333333-3333-4333-8333-333333333333", "review_token": preview["review_token"]}
	code, run := studioJSON(t, s, path+"/operations/runs", request, true)
	if code != 202 {
		t.Fatalf("run %d %v", code, run)
	}
	id := run["run"].(map[string]any)["id"].(string)
	select {
	case <-entered:
	case <-time.After(8 * time.Second):
		t.Fatal("job did not dispatch")
	}
	human := map[string]any{"id": "44444444-4444-4444-8444-444444444444", "kind": "note", "body": "Concurrent human note", "x": 400, "y": 400}
	input["version"] = 2
	input["cards"] = []map[string]any{prompt, provider, human}
	if code, out := studioJSON(t, s, path+"/document", input, true); code != 200 {
		t.Fatalf("human save %d %v", code, out)
	}
	close(release)
	deadline := time.Now().Add(8 * time.Second)
	status := ""
	for time.Now().Before(deadline) {
		if e := s.app.Deps.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id=?`, id).Scan(&status); e != nil {
			t.Fatal(e)
		}
		if status == "succeeded" || status == "failed" || status == "uncertain" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != "succeeded" || fake.calls.Load() != 1 {
		t.Fatalf("status %s calls %d", status, fake.calls.Load())
	}
	doc := canvasGet(t, s, path)
	if len(doc["cards"].([]any)) != 4 || len(doc["provenance"].(map[string]any)) != 1 {
		t.Fatalf("human/output lost: %v", doc)
	}
	if code, out := studioJSON(t, s, path+"/operations/runs", request, true); code != 202 || out["run"].(map[string]any)["id"] != id {
		t.Fatalf("repeat not idempotent: %d %v", code, out)
	}
	if fake.calls.Load() != 1 {
		t.Fatal("provider repeated")
	}
}
