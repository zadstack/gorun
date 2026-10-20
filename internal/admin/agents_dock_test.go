package admin_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestAgentsDockOwnedProjectionEffectivePromptAndNoDispatch(t *testing.T) {
	s := boot(t)
	u := s.login("dock-owner@example.test", "admin", "en")
	fake := newFakeAI(t, 200, openAIReply("never-called"), nil)
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {fake.srv.URL}, "model": {"dock-model"}})
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO settings(key,value_json,updated_at) VALUES('ai.studio','{"presets":[{"id":"dock-policy","name":"Policy","kind":"text","prompt":"Preset policy"}]}','now')`); err != nil {
		t.Fatal(err)
	}
	path := newCanvas(t, s)
	prompt := map[string]any{"id": "11111111-1111-4111-8111-111111111111", "kind": "prompt", "body": "Private saved instructions"}
	node := map[string]any{"id": "22222222-2222-4222-8222-222222222222", "kind": "provider", "title": "<img src=x onerror=alert(1)>", "provider_id": "native-text", "preset_id": "dock-policy"}
	source := map[string]any{"id": "33333333-3333-4333-8333-333333333333", "kind": "note", "body": "SOURCE_CONTEXT_NOT_FOR_DRAWER"}
	in := canvasInput(prompt, node, source)
	in["edges"] = []map[string]string{{"from": prompt["id"].(string), "to": node["id"].(string)}, {"from": source["id"].(string), "to": node["id"].(string)}}
	if code, _ := studioJSON(t, s, path+"/document", in, true); code != 200 {
		t.Fatal(code)
	}
	response, body := s.do("GET", "/admin/x/agents-dock", nil)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(response.StatusCode, response.Header)
	}
	var data struct {
		Agents []struct {
			ID, Prompt, Model, Href string
			Configured              bool
		}
		Available bool
	}
	if json.Unmarshal([]byte(body), &data) != nil || len(data.Agents) != 1 || !data.Available {
		t.Fatal(body)
	}
	x := data.Agents[0]
	if !x.Configured || x.Prompt != "Preset policy\n\nPrivate saved instructions" || x.Model != "dock-model" || x.Href != path+"#card-"+node["id"].(string) {
		t.Fatal(body)
	}
	if fake.calls.Load() != 0 {
		t.Fatal("opening the dock dispatched a model")
	}
	mustNotContain(t, body, fake.srv.URL, "api_key", "context", "review_token", "secret", "SOURCE_CONTEXT_NOT_FOR_DRAWER")
	var runs int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvas_runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatal(runs, err)
	}
	s.login("dock-other@example.test", "admin", "en")
	_, body = s.do("GET", "/admin/x/agents-dock", nil)
	mustNotContain(t, body, "Private saved instructions", "dock-model", node["id"].(string))
	s.signIn(u.Email)
	if err := s.app.Deps.Settings.Set(context.Background(), "site.workspace", "simple"); err != nil {
		t.Fatal(err)
	}
	_, body = s.do("GET", "/admin/x/agents-dock", nil)
	mustContain(t, body, `"available":false`)
	mustNotContain(t, body, "Private saved instructions")
	response, body = s.do("GET", "/admin", nil)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	mustNotContain(t, body, "data-agents-dock", "agents-dock.js", "agents-dock.css", `href="/admin/canvases"`)
}

func TestAgentsDockDoesNotResolveForeignRevokedOrAPIKeys(t *testing.T) {
	s := boot(t)
	other := s.login("dock-key-other@example.test", "admin", "en")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	secret, foreign, err := keys.CreateKind(context.Background(), auth.KeyKindAgent, "FOREIGN_NAME", other.ID, []string{"*"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	owner := s.login("dock-key-owner@example.test", "admin", "en")
	_, own, err := keys.CreateKind(context.Background(), auth.KeyKindAgent, "OWN_NAME", owner.ID, []string{"*"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	_, api, err := keys.CreateKind(context.Background(), auth.KeyKindAPI, "API_NAME", owner.ID, []string{"*"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	path := newCanvas(t, s)
	cards := []map[string]any{}
	for i, id := range []string{foreign.ID, own.ID, api.ID} {
		cards = append(cards, map[string]any{"id": []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"}[i], "kind": "agent", "agent_id": id, "title": "Saved card"})
	}
	if code, _ := studioJSON(t, s, path+"/document", canvasInput(cards...), true); code != 200 {
		t.Fatal(code)
	}
	_, body := s.do("GET", "/admin/x/agents-dock", nil)
	mustContain(t, body, "OWN_NAME", own.ID)
	mustNotContain(t, body, "FOREIGN_NAME", foreign.ID, "API_NAME", api.ID, secret, foreign.Prefix, `"configured":true`)
	if err := keys.Revoke(context.Background(), own.ID); err != nil {
		t.Fatal(err)
	}
	_, body = s.do("GET", "/admin/x/agents-dock", nil)
	mustNotContain(t, body, "OWN_NAME", own.ID)
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO roles(slug,name_json,system) VALUES('dockviewer','{"en":"Viewer"}',0)`); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(context.Background(), s.app.Deps.DB); err != nil {
		t.Fatal(err)
	}
	s.login("dock-viewer@example.test", "dockviewer", "en")
	response, _ := s.do("GET", "/admin/x/agents-dock", nil)
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
	s.c = newClient()
	response, _ = s.do("GET", "/admin/x/agents-dock", nil)
	if response.StatusCode != 303 {
		t.Fatal(response.StatusCode)
	}
}
