package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

const suggestedFlow = `{"suggestions":[{"title":"Human review","summary":"Prepare a private draft","cards":[{"kind":"prompt","title":"Instructions","body":"Draft only; never publish","x":24,"y":24},{"kind":"agent","title":"Executor","body":"","x":420,"y":24},{"kind":"schedule","title":"Later","body":"","interval":"daily","x":24,"y":250}],"edges":[{"from":0,"to":1}]}]}`
const aiCanvasPath = "/admin/x/canvases/ai-suggestions"

func aiCanvasPreview(t *testing.T, s *site) map[string]any {
	t.Helper()
	code, p := studioJSON(t, s, aiCanvasPath+"/preview", map[string]any{"target_kind": "agent", "locale": "en"}, true)
	if code != 200 {
		t.Fatalf("preview %d %#v", code, p)
	}
	return p
}
func aiCanvasGenerate(t *testing.T, s *site, p map[string]any) (int, map[string]any) {
	return studioJSON(t, s, aiCanvasPath+"/generate", map[string]any{"request_id": p["request_id"], "target_kind": "agent", "locale": "en", "reviewed": true}, true)
}
func TestCanvasAIHTTPConsentDriftAndSafeApply(t *testing.T) {
	s := boot(t, content.New())
	owner := s.login("canvas-ai@example.invalid", "admin", "en")
	f := newFakeAI(t, 200, openAIReply(suggestedFlow), func(t *testing.T, r *http.Request, body map[string]any) {
		if body["model"] != "reviewed-model" {
			t.Errorf("unreviewed model: %#v", body)
		}
	})
	cfg := url.Values{"provider": {"openai"}, "base_url": {f.srv.URL}, "model": {"reviewed-model"}, "api_key": {testAIKey}}
	configureAI(t, s, cfg)
	p := aiCanvasPreview(t, s)
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), testAIKey) || strings.Contains(string(b), "api_key") || p["system"] == nil || p["user"] == nil {
		t.Fatalf("unsafe or incomplete preview %s", b)
	}
	if f.calls.Load() != 0 {
		t.Fatal("preview made network call")
	}
	if code, _ := studioJSON(t, s, aiCanvasPath+"/generate", map[string]any{"request_id": p["request_id"], "target_kind": "agent", "locale": "en", "reviewed": true}, false); code != 403 {
		t.Fatalf("CSRF %d", code)
	}
	for _, change := range []struct{ key, value string }{{"ai.model", "other-model"}, {"ai.base_url", "https://different.example/v1"}, {"ai.provider", "anthropic"}, {"ai.api_key", "different"}, {"site.title", "Different site"}} {
		p = aiCanvasPreview(t, s)
		old := s.app.Deps.Settings.GetString(context.Background(), change.key, "")
		if err := s.app.Deps.Settings.Set(context.Background(), change.key, change.value); err != nil {
			t.Fatal(err)
		}
		if code, _ := aiCanvasGenerate(t, s, p); code != 409 {
			t.Fatalf("%s drift accepted %d", change.key, code)
		}
		if err := s.app.Deps.Settings.Set(context.Background(), change.key, old); err != nil {
			t.Fatal(err)
		}
	}
	p = aiCanvasPreview(t, s)
	_ = s.app.Deps.Settings.Set(context.Background(), "ai.model", "temporary")
	_ = s.app.Deps.Settings.Set(context.Background(), "ai.model", "reviewed-model")
	if code, _ := aiCanvasGenerate(t, s, p); code != 409 {
		t.Fatalf("restored config accepted %d", code)
	}
	p = aiCanvasPreview(t, s)
	second := s.login("canvas-ai-other@example.invalid", "admin", "en")
	if code, _ := aiCanvasGenerate(t, s, p); code != 409 {
		t.Fatalf("cross-owner %d", code)
	}
	s.signIn(owner.Email)
	if code, _ := aiCanvasGenerate(t, s, map[string]any{"request_id": "01AAAAAAAAAAAAAAAAAAAAAAAA"}); code != 409 {
		t.Fatalf("invented token %d", code)
	}
	if f.calls.Load() != 0 {
		t.Fatal("invalid consent contacted provider")
	}
	code, out := aiCanvasGenerate(t, s, p)
	if code != 200 {
		t.Fatalf("generate %d %#v", code, out)
	}
	if code, _ := aiCanvasGenerate(t, s, p); code != 409 || f.calls.Load() != 1 {
		t.Fatalf("replay %d calls%d", code, f.calls.Load())
	}
	input := map[string]any{"request_id": out["request_id"], "index": 0}
	code, board := studioJSON(t, s, aiCanvasPath+"/apply", input, true)
	if code != 200 {
		t.Fatalf("apply %d %#v", code, board)
	}
	code, retry := studioJSON(t, s, aiCanvasPath+"/apply", input, true)
	if code != 200 || retry["id"] != board["id"] {
		t.Fatalf("duplicate apply %#v", retry)
	}
	var document string
	var count int
	_ = s.app.Deps.DB.QueryRow(`SELECT document_json FROM admin_canvases WHERE id=? AND owner_id=?`, board["id"], owner.ID).Scan(&document)
	_ = s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvases WHERE owner_id=?`, owner.ID).Scan(&count)
	if count != 1 || strings.Contains(document, `"enabled":true`) || strings.Contains(document, `"agent_id"`) || strings.Contains(document, `"provider_id"`) {
		t.Fatalf("unsafe board: %s count%d", document, count)
	}
	s.signIn(second.Email)
	if code, _ := studioJSON(t, s, aiCanvasPath+"/apply", input, true); code != 409 {
		t.Fatalf("cross-owner apply %d", code)
	}
}
func TestCanvasAIHTTPConcurrentInitializationAndOneShot(t *testing.T) {
	s := boot(t, content.New())
	s.login("canvas-ai-race@example.invalid", "admin", "en")
	f := newFakeAI(t, 200, openAIReply(suggestedFlow), nil)
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {f.srv.URL}, "model": {"race"}})
	// Concurrent first GETs exercise the outer cache initialization, not only its inner map.
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := s.c.Get(s.srv.URL + aiCanvasPath)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("status%d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	old := aiCanvasPreview(t, s)
	p := aiCanvasPreview(t, s)
	if code, _ := aiCanvasGenerate(t, s, old); code != 409 {
		t.Fatalf("superseded review %d", code)
	}
	csrf := s.csrf()
	raw := fmt.Sprintf(`{"request_id":%q,"target_kind":"agent","locale":"en","reviewed":true}`, p["request_id"])
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := http.NewRequest("POST", s.srv.URL+aiCanvasPath+"/generate", strings.NewReader(raw))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", csrf)
			resp, err := s.c.Do(r)
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(codes)
	successes := 0
	for code := range codes {
		if code == 200 {
			successes++
		} else if code != 409 {
			t.Errorf("parallel status%d", code)
		}
	}
	if successes != 1 || f.calls.Load() != 1 {
		t.Fatalf("one-shot successes%d calls%d", successes, f.calls.Load())
	}
}
func TestCanvasAIHTTPUnavailableAndUpstreamFailure(t *testing.T) {
	s := boot(t, content.New())
	s.login("canvas-ai-errors@example.invalid", "admin", "en")
	if code, _ := studioJSON(t, s, aiCanvasPath+"/preview", map[string]any{"target_kind": "agent"}, true); code != 503 {
		t.Fatalf("unconfigured%d", code)
	}
	f := newFakeAI(t, 500, `{"error":"private"}`, nil)
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {f.srv.URL}, "model": {"error"}})
	p := aiCanvasPreview(t, s)
	if code, out := aiCanvasGenerate(t, s, p); code != 502 || out["error"] != "upstream" {
		t.Fatalf("upstream%d %#v", code, out)
	}
	if code, _ := aiCanvasGenerate(t, s, p); code != 409 || f.calls.Load() != 1 {
		t.Fatalf("failed request retried%d", code)
	}
}

func TestCanvasAIHTTPInFlightRevocationDiscardsOutput(t *testing.T) {
	s := boot(t, content.New())
	owner := s.login("canvas-ai-revoke@example.invalid", "admin", "en")
	entered, release := make(chan struct{}), make(chan struct{})
	f := newFakeAI(t, 200, openAIReply(suggestedFlow), func(t *testing.T, r *http.Request, b map[string]any) { close(entered); <-release })
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {f.srv.URL}, "model": {"reviewed-model"}})
	p := aiCanvasPreview(t, s)
	token := s.csrf()
	done := make(chan int, 1)
	go func() {
		raw, _ := json.Marshal(map[string]any{"request_id": p["request_id"], "target_kind": "agent", "locale": "en", "reviewed": true})
		req, _ := http.NewRequest("POST", s.srv.URL+aiCanvasPath+"/generate", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", token)
		resp, err := s.c.Do(req)
		if err != nil {
			done <- 0
			return
		}
		defer resp.Body.Close()
		done <- resp.StatusCode
	}()
	<-entered
	if _, err := s.app.Deps.DB.Exec(`UPDATE users SET role='customer' WHERE id=?`, owner.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	if code := <-done; code != 409 {
		t.Fatalf("revoked in flight returned%d", code)
	}
	_, _ = s.app.Deps.DB.Exec(`UPDATE users SET role='admin' WHERE id=?`, owner.ID)
	resp, body := s.do("GET", aiCanvasPath, nil)
	if resp.StatusCode != 200 || strings.Contains(body, "suggestions") {
		t.Fatalf("revoked output cached: %s", body)
	}
}
