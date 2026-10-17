package admin_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestCanvasImageResultIsPrivateAndAuthorized(t *testing.T) {
	s := boot(t)
	owner := s.login("canvas-image@example.com", "admin", "en")
	var buf bytes.Buffer
	if e := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(buf.Bytes())}}})
	fake := newFakeAI(t, 200, string(raw), nil)
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {fake.srv.URL}, "model": {"test-model"}})
	if code, out := studioJSON(t, s, "/admin/settings/studio", map[string]any{"image_model": "image-test", "bindings": []any{}, "presets": []any{}}, true); code != 200 {
		t.Fatal(code, out)
	}
	path := newCanvas(t, s)
	promptID, providerID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	in := canvasInput(map[string]any{"id": promptID, "kind": "prompt", "body": "A geometric illustration", "x": 0, "y": 0}, map[string]any{"id": providerID, "kind": "provider", "provider_id": "native-image", "generation_kind": "image", "title": "Illustrator", "x": 200, "y": 0})
	in["edges"] = []map[string]string{{"from": promptID, "to": providerID}}
	if code, out := studioJSON(t, s, path+"/document", in, true); code != 200 {
		t.Fatal(code, out)
	}
	code, preview := studioJSON(t, s, path+"/operations/preview", map[string]any{"version": 2, "node_id": providerID}, true)
	if code != 200 {
		t.Fatal(code, preview)
	}
	code, out := studioJSON(t, s, path+"/operations/runs", map[string]any{"request_id": "33333333-3333-4333-8333-333333333333", "review_token": preview["review_token"]}, true)
	if code != 202 {
		t.Fatal(code, out)
	}
	id := out["run"].(map[string]any)["id"].(string)
	status := ""
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		if e := s.app.Deps.DB.QueryRow(`SELECT status FROM admin_canvas_runs WHERE id=?`, id).Scan(&status); e != nil {
			t.Fatal(e)
		}
		if status == "succeeded" || status == "failed" || status == "uncertain" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != "succeeded" {
		t.Fatal(status)
	}
	doc := canvasGet(t, s, path)
	result := doc["results"].(map[string]any)[id].(map[string]any)
	if result["kind"] != "image" {
		t.Fatal(result)
	}
	asset := result["url"].(string)
	resp, _ := s.do("GET", asset, nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("unsafe media delivery", resp.StatusCode, resp.Header)
	}
	resp, _ = s.do("GET", asset, nil, "Range", "bytes=0-7")
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatal("authorized range", resp.StatusCode)
	}
	s.login("other-image@example.com", "admin", "en")
	resp, _ = s.do("GET", asset, nil)
	if resp.StatusCode != 404 {
		t.Fatal("other owner media", resp.StatusCode)
	}
	s.signIn(owner.Email)
	// A browser cannot create a second result card by copying its run ID.
	cards := doc["cards"].([]any)
	copy := map[string]any{"id": "44444444-4444-4444-8444-444444444444", "kind": "result", "run_id": id, "body": "forged", "x": 0, "y": 0}
	cards = append(cards, copy)
	if code, _ = studioJSON(t, s, path+"/document", map[string]any{"version": doc["version"], "title": doc["title"], "cards": cards, "edges": doc["edges"]}, true); code != 422 {
		t.Fatal("forged result", code)
	}
}
