package admin_test

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func recipeHidden(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`name="` + name + `" value="([^"]*)"`).FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("missing %s", name)
	}
	return m[1]
}
func recipeForm(t *testing.T, body, key string) url.Values {
	return url.Values{"agent": {key}, "snapshot": {recipeHidden(t, body, "snapshot")}, "draft_id": {recipeHidden(t, body, "draft_id")}, "title": {"Source review recipe"}, "instructions": {"Explain supplied evidence. Keep source URL and date; flag uncertainty."}, "context": {"Approved excerpt at https://example.test/brief dated 2026-10-20. <script>do not execute</script>"}, "destination": {"Private source review"}, "action": {"preview"}}
}

func TestAgentRecipePrivateDraftNoAuthorityOrExecution(t *testing.T) {
	s := boot(t)
	owner := s.login("recipe-owner@example.test", "admin", "en")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	secret, key, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Recipe owned agent", owner.ID, []string{"*"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	profile, version := db.NewID(), db.NewID()
	if e = s.app.Deps.Settings.Set(context.Background(), "connections.profile."+profile, map[string]any{"id": profile, "version": version, "kind": "mcp", "name": "Saved source catalog", "endpoint": "https://example.test/SECRET_PATH", "secret": "ENCRYPTED_SENTINEL", "test_status": "pass", "tested_at": "2026-10-19T00:00:00Z"}); e != nil {
		t.Fatal(e)
	}
	if r, b := s.post("/admin/agents/keys/"+key.ID+"/bundle", url.Values{"skill_name": {"Source discipline"}, "skill_instructions": {"Never invent sources."}, "resource_name": {""}, "resource_text": {""}, "mcp": {profile}, "version_" + profile: {version}, "reviewed": {"1"}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode, b)
	}
	var before string
	s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key=?`, "agents.bundle."+key.ID).Scan(&before)
	counts := func() [4]int {
		out := [4]int{}
		for i, q := range []string{`SELECT count(*) FROM admin_canvas_runs`, `SELECT count(*) FROM admin_canvas_grants`, `SELECT count(*) FROM jobs`, `SELECT count(*) FROM admin_canvases`} {
			if e := s.app.Deps.DB.QueryRow(q).Scan(&out[i]); e != nil {
				t.Fatal(e)
			}
		}
		return out
	}
	initial := counts()
	path := "/admin/agents/recipes?agent=" + key.ID
	r, body := s.do("GET", path, nil)
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Fatal(r.StatusCode)
	}
	mustContain(t, body, "Updates Researcher", "Recipe owned agent", "Saved source catalog", "External client", "No managed tools enabled", "agent-wide settings")
	mustNotContain(t, body, secret, key.Prefix, "SECRET_PATH", "ENCRYPTED_SENTINEL", "Last tool discovery passed")
	form := recipeForm(t, body, key.ID)
	if r, _ = s.do("POST", "/admin/agents/recipes", form); r.StatusCode != 403 {
		t.Fatal("CSRF", r.StatusCode)
	}
	r, body = s.post("/admin/agents/recipes", form)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode, body)
	}
	mustContain(t, body, "Unsaved recipe preview", "&lt;script&gt;do not execute&lt;/script&gt;")
	if counts() != initial {
		t.Fatal("GET/preview mutated execution or canvas")
	}
	form.Set("action", "save")
	if r, _ = s.post("/admin/agents/recipes", form); r.StatusCode != 422 {
		t.Fatal("missing acknowledgement", r.StatusCode)
	}
	form.Set("reviewed", "1")
	r, body = s.post("/admin/agents/recipes", form)
	if r.StatusCode != 303 {
		t.Fatal(r.StatusCode, body)
	}
	location := r.Header.Get("Location")
	if !strings.HasPrefix(location, "/admin/canvases/") {
		t.Fatal(location)
	}
	var cardsRaw, edgesRaw, after string
	var boardCount, runCount, grantCount, audits int
	s.app.Deps.DB.QueryRow(`SELECT document_json,edges_json FROM admin_canvases WHERE id=? AND owner_id=?`, form.Get("draft_id"), owner.ID).Scan(&cardsRaw, &edgesRaw)
	var cards []map[string]any
	json.Unmarshal([]byte(cardsRaw), &cards)
	if len(cards) != 4 || cards[0]["body"] != form.Get("instructions") || cards[1]["body"] != form.Get("context") || cards[2]["agent_id"] != key.ID || cards[2]["title"] != form.Get("destination") {
		t.Fatal(cardsRaw)
	}
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvas_grants WHERE board_id=?`, form.Get("draft_id")).Scan(&grantCount)
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvas_runs WHERE board_id=?`, form.Get("draft_id")).Scan(&runCount)
	if grantCount != 0 || runCount != 0 {
		t.Fatal("implicit grants/execution")
	}
	s.app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key=?`, "agents.bundle."+key.ID).Scan(&after)
	if before != after {
		t.Fatal("modified shared agent bundle")
	}
	if r, _ = s.post("/admin/agents/recipes", form); r.StatusCode != 303 {
		t.Fatal("idempotent retry", r.StatusCode)
	}
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvases WHERE id=?`, form.Get("draft_id")).Scan(&boardCount)
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='agent.recipe.created'`).Scan(&audits)
	if boardCount != 1 || audits != 1 {
		t.Fatal("duplicate draft/audit", boardCount, audits)
	}
	_, body = s.do("GET", location+"/document", nil)
	mustContain(t, body, "Source review recipe", key.ID)
	s.login("recipe-other@example.test", "admin", "en")
	if r, b := s.do("GET", path, nil); r.StatusCode != 404 || strings.Contains(b, "Recipe owned agent") {
		t.Fatal("foreign metadata", r.StatusCode, b)
	}
	if r, _ = s.do("GET", location+"/document", nil); r.StatusCode != 404 {
		t.Fatal("foreign canvas", r.StatusCode)
	}
	s.login("recipe-editor@example.test", "editor", "en")
	r, body = s.do("GET", "/admin/agents/recipes", nil)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	mustContain(t, body, "This example is available to editors")
	mustNotContain(t, body, "Recipe owned agent", "Saved source catalog", "name=\"agent\"")
	if r, _ = s.do("GET", path, nil); r.StatusCode != 403 {
		t.Fatal("editor details", r.StatusCode)
	}
	if r, _ = s.post("/admin/agents/recipes", form); r.StatusCode != 403 {
		t.Fatal("editor save", r.StatusCode)
	}
}

func TestAgentRecipeRejectsStaleAndRevokedConfiguration(t *testing.T) {
	for _, change := range []string{"bundle", "model", "connection", "scope", "revoked", "permission", "simple"} {
		t.Run(change, func(t *testing.T) {
			s := boot(t)
			owner := s.login("stale-recipe@example.test", "admin", "en")
			keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
			_, key, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Stale recipe agent", owner.ID, []string{"*"}, 60, "")
			if e != nil {
				t.Fatal(e)
			}
			profile, version := db.NewID(), db.NewID()
			c := map[string]any{"id": profile, "version": version, "kind": "mcp", "name": "Sources", "endpoint": "https://example.test/sources"}
			s.app.Deps.Settings.Set(context.Background(), "connections.profile."+profile, c)
			r, _ := s.post("/admin/agents/keys/"+key.ID+"/bundle", url.Values{"mcp": {profile}, "version_" + profile: {version}, "reviewed": {"1"}})
			if r.StatusCode != 303 {
				t.Fatal(r.StatusCode)
			}
			_, body := s.do("GET", "/admin/agents/recipes?agent="+key.ID, nil)
			form := recipeForm(t, body, key.ID)
			form.Set("action", "save")
			form.Set("reviewed", "1")
			switch change {
			case "bundle":
				s.app.Deps.DB.Exec(`UPDATE settings SET value_json=json_set(value_json,'$.version',?) WHERE key=?`, db.NewID(), "agents.bundle."+key.ID)
			case "model":
				s.app.Deps.Settings.Set(context.Background(), "agents.model."+key.ID, map[string]any{"mode": "external", "version": db.NewID()})
			case "connection":
				c["name"] = "Changed even without version bump"
				s.app.Deps.Settings.Set(context.Background(), "connections.profile."+profile, c)
			case "scope":
				s.app.Deps.DB.Exec(`UPDATE api_keys SET scopes_json='[]' WHERE id=?`, key.ID)
			case "revoked":
				s.app.Deps.DB.Exec(`UPDATE api_keys SET revoked_at=? WHERE id=?`, db.Now(), key.ID)
			case "permission":
				s.app.Deps.DB.Exec(`UPDATE users SET role='editor' WHERE id=?`, owner.ID)
			case "simple":
				s.app.Deps.Settings.Set(context.Background(), "site.workspace", "simple")
			}
			r, _ = s.post("/admin/agents/recipes", form)
			if r.StatusCode != 409 && r.StatusCode != 403 && r.StatusCode != 404 {
				t.Fatal("stale accepted", r.StatusCode)
			}
			var n int
			s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvases WHERE id=?`, form.Get("draft_id")).Scan(&n)
			if n != 0 {
				t.Fatal("stale created canvas")
			}
		})
	}
}

func TestAgentRecipeRetryCannotReplaceAnotherRecipeOrOwner(t *testing.T) {
	s := boot(t)
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	owners := map[string]*auth.User{}
	createKey := func(email string) *auth.APIKey {
		owner, exists := owners[email]
		if !exists {
			owner = s.login(email, "admin", "en")
			owners[email] = owner
		}
		_, k, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Owned recipe", owner.ID, []string{"*"}, 60, "")
		if e != nil {
			t.Fatal(e)
		}
		return k
	}
	first := createKey("first-recipe@example.test")
	_, body := s.do("GET", "/admin/agents/recipes?agent="+first.ID, nil)
	form := recipeForm(t, body, first.ID)
	form.Set("action", "save")
	form.Set("reviewed", "1")
	if r, _ := s.post("/admin/agents/recipes", form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	for _, email := range []string{"first-recipe@example.test", "second-recipe@example.test"} {
		key := createKey(email)
		_, body = s.do("GET", "/admin/agents/recipes?agent="+key.ID, nil)
		retry := recipeForm(t, body, key.ID)
		retry.Set("draft_id", form.Get("draft_id"))
		retry.Set("action", "save")
		retry.Set("reviewed", "1")
		if r, _ := s.post("/admin/agents/recipes", retry); r.StatusCode != 409 {
			t.Fatal("conflicting retry", email, r.StatusCode)
		}
	}
	var owner, document string
	s.app.Deps.DB.QueryRow(`SELECT owner_id,document_json FROM admin_canvases WHERE id=?`, form.Get("draft_id")).Scan(&owner, &document)
	if owner != first.UserID || !strings.Contains(document, first.ID) {
		t.Fatal("retry replaced original recipe")
	}
	var n int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='agent.recipe.created'`).Scan(&n)
	if n != 1 {
		t.Fatal("retry duplicate audit", n)
	}
}

func TestAgentRecipeAcceptsBoundedLongArabicForm(t *testing.T) {
	s := boot(t)
	owner := s.login("long-recipe@example.test", "admin", "ar")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	_, key, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "باحث", owner.ID, []string{"*"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	_, body := s.do("GET", "/admin/agents/recipes?agent="+key.ID, nil)
	form := recipeForm(t, body, key.ID)
	form.Set("instructions", strings.Repeat("م", 2000))
	form.Set("context", strings.Repeat("ع", 2000))
	form.Set("title", strings.Repeat("ب", 60))
	form.Set("destination", strings.Repeat("ج", 60))
	if len(form.Encode()) <= 24<<10 {
		t.Fatal("fixture does not exceed old encoded-body cap")
	}
	if r, b := s.post("/admin/agents/recipes", form); r.StatusCode != 200 {
		t.Fatal("valid bounded Arabic form rejected", r.StatusCode, b)
	}
	form.Set("context", strings.Repeat("x", 65<<10))
	if r, _ := s.post("/admin/agents/recipes", form); r.StatusCode != 400 {
		t.Fatal("body limit not enforced", r.StatusCode)
	}
}
