package admin_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func lifecycleResult(t *testing.T, s *site, path string, owner *auth.User, media bool) string {
	t.Helper()
	board := strings.TrimPrefix(path, "/admin/canvases/")
	card := "44444444-4444-4444-8444-444444444444"
	run := db.NewID()
	payload := map[string]any{"id": card, "kind": "note", "title": "Campaign result", "body": "Review <script>alert(1)</script> & keep\nSecond line", "x": 24, "y": 24}
	raw, _ := json.Marshal([]any{payload})
	if _, e := s.app.Deps.DB.Exec(`UPDATE admin_canvases SET document_json=? WHERE id=?`, string(raw), board); e != nil {
		t.Fatal(e)
	}
	document := `{"candidate":{"input":{"kind":"text"}}}`
	if media {
		document = `{"media_file":"private-output.png","media_mime":"image/png"}`
	}
	if _, e := s.app.Deps.DB.Exec(`INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,result_card_id,created_at) VALUES(?,?,?,?,?,'succeeded',?,?,?)`, run, board, owner.ID, db.NewID(), card, document, card, db.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.app.Deps.DB.Exec(`INSERT INTO admin_canvas_provenance(board_id,card_id,actor_kind,actor_name,run_id,created_at) VALUES(?,?,'api_key','Test agent',?,?)`, board, card, run, db.Now()); e != nil {
		t.Fatal(e)
	}
	return card
}
func TestCanvasDraftAtomicCreationRetryAndIsolation(t *testing.T) {
	s := boot(t, content.New())
	owner := s.login("draft-owner@example.com", "admin", "en")
	path := newCanvas(t, s)
	card := lifecycleResult(t, s, path, owner, false)
	route := path + "/cards/" + card + "/draft"
	if r, _ := s.do("POST", route, url.Values{"type": {"post"}}); r.StatusCode != 403 {
		t.Fatal("csrf", r.StatusCode)
	}
	r, body := s.post(route, url.Values{"type": {"post"}})
	if r.StatusCode != 200 {
		t.Fatalf("create %d %s", r.StatusCode, body)
	}
	var result map[string]string
	if e := json.Unmarshal([]byte(body), &result); e != nil {
		t.Fatal(e)
	}
	cs, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	ctx := auth.WithUser(context.Background(), owner)
	item, e := cs.Get(ctx, result["id"])
	if e != nil {
		t.Fatal(e)
	}
	if item.Status != "draft" || item.AuthorID != owner.ID || item.PublishedAt != "" || strings.Contains(item.Body, "<script>") || !strings.Contains(item.Body, "&lt;script&gt;") {
		t.Fatalf("unsafe draft %#v", item)
	}
	var provenance string
	if e = s.app.Deps.DB.QueryRow(`SELECT source_json FROM admin_canvas_drafts WHERE content_id=?`, item.ID).Scan(&provenance); e != nil || !strings.Contains(provenance, "Test agent") {
		t.Fatal("provenance", e, provenance)
	}
	in := content.InputFromItem(item)
	in.Body = "<p>Human revision</p>"
	if _, e = cs.Update(ctx, item.ID, in); e != nil {
		t.Fatal(e)
	}
	// Concurrent repeated clicks must return the original item and preserve edits.
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			r, body := s.post(route, url.Values{"type": {"post"}})
			if r.StatusCode != 200 || !strings.Contains(body, item.ID) {
				t.Errorf("retry %d %s", r.StatusCode, body)
			}
		})
	}
	wg.Wait()
	item, e = cs.Get(ctx, item.ID)
	if e != nil || item.Body != "<p>Human revision</p>" {
		t.Fatal("retry overwrote draft", e)
	}
	var n int
	if e = s.app.Deps.DB.QueryRow(`SELECT count(*) FROM content`).Scan(&n); e != nil || n != 1 {
		t.Fatal("duplicates", n, e)
	}
	s.login("draft-other@example.com", "admin", "en")
	if r, _ = s.post(route, url.Values{"type": {"post"}}); r.StatusCode != 404 {
		t.Fatal("owner isolation", r.StatusCode)
	}
	if _, e := s.app.Deps.DB.Exec(`INSERT INTO roles(slug,name_json,system) VALUES('viewer','{"en":"Viewer"}',0)`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.app.Deps.DB.Exec(`INSERT INTO role_permissions(role,permission) VALUES('viewer','content.view')`); e != nil {
		t.Fatal(e)
	}
	if e := auth.ReloadRoles(context.Background(), s.app.Deps.DB); e != nil {
		t.Fatal(e)
	}
	s.login("draft-no-permission@example.com", "viewer", "en")
	if r, _ = s.post(route, url.Values{"type": {"post"}}); r.StatusCode != 403 {
		t.Fatal("permission", r.StatusCode)
	}
}
func TestCanvasDraftRejectsMediaAndPlainNotes(t *testing.T) {
	s := boot(t, content.New())
	owner := s.login("reject-owner@example.com", "admin", "en")
	path := newCanvas(t, s)
	card := lifecycleResult(t, s, path, owner, true)
	if r, _ := s.post(path+"/cards/"+card+"/draft", url.Values{"type": {"post"}}); r.StatusCode != 422 {
		t.Fatal("media accepted", r.StatusCode)
	}
	if _, e := s.app.Deps.DB.Exec(`DELETE FROM admin_canvas_provenance`); e != nil {
		t.Fatal(e)
	}
	if r, _ := s.post(path+"/cards/"+card+"/draft", url.Values{"type": {"post"}}); r.StatusCode != 422 {
		t.Fatal("unattributed note", r.StatusCode)
	}
}
func TestCanvasTemplatesArePrivateStructuralAndInactive(t *testing.T) {
	s := boot(t, content.New())
	owner := s.login("template-owner@example.com", "admin", "en")
	path := newCanvas(t, s)
	cards := []map[string]any{
		{"id": "11111111-1111-4111-8111-111111111111", "kind": "prompt", "title": "PRIVATE TITLE", "body": "Bearer private-secret"},
		{"id": "22222222-2222-4222-8222-222222222222", "kind": "agent", "agent_id": db.NewID()},
		{"id": "33333333-3333-4333-8333-333333333333", "kind": "schedule", "interval": "hourly"},
	}
	in := canvasInput(cards...)
	in["edges"] = []map[string]string{{"from": cards[0]["id"].(string), "to": cards[1]["id"].(string)}}
	if code, _ := studioJSON(t, s, path+"/document", in, true); code != 200 {
		t.Fatal(code)
	}
	if r, _ := s.do("POST", path+"/template", url.Values{"title": {"Reusable"}}); r.StatusCode != 403 {
		t.Fatal("csrf", r.StatusCode)
	}
	r, body := s.post(path+"/template", url.Values{"title": {"Reusable"}})
	if r.StatusCode != 201 {
		t.Fatalf("save %d %s", r.StatusCode, body)
	}
	var template map[string]string
	json.Unmarshal([]byte(body), &template)
	reuse := "/admin/canvas-templates/" + template["id"] + "/reuse"
	if r, _ := s.do("POST", reuse, url.Values{"title": {"Fresh workflow"}}); r.StatusCode != 403 {
		t.Fatal("reuse csrf", r.StatusCode)
	}
	var raw string
	if e := s.app.Deps.DB.QueryRow(`SELECT document_json FROM admin_canvas_templates WHERE id=?`, template["id"]).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"private-secret", "PRIVATE TITLE", cards[1]["agent_id"].(string), cards[0]["id"].(string)} {
		if strings.Contains(raw, secret) {
			t.Fatal("unsafe saved template", secret)
		}
	}
	r, body = s.post(reuse, url.Values{"title": {"Fresh workflow"}})
	if r.StatusCode != 201 {
		t.Fatalf("reuse %d %s", r.StatusCode, body)
	}
	var created map[string]string
	json.Unmarshal([]byte(body), &created)
	doc := canvasGet(t, s, created["href"])
	if len(doc["cards"].([]any)) != 3 || len(doc["edges"].([]any)) != 1 || len(doc["schedules"].(map[string]any)) != 0 || len(doc["provenance"].(map[string]any)) != 0 {
		t.Fatalf("bad reuse %#v", doc)
	}
	for _, value := range doc["cards"].([]any) {
		c := value.(map[string]any)
		if c["body"] != "" || c["agent_id"] != nil || c["run_id"] != nil {
			t.Fatalf("binding/output copied %#v", c)
		}
	}
	s.login("template-other@example.com", "admin", "en")
	if r, _ = s.post(reuse, url.Values{"title": {"Stolen"}}); r.StatusCode != 404 {
		t.Fatal("reuse leak", r.StatusCode)
	}
	r, body = s.do("GET", "/admin/canvas-templates", nil)
	if r.StatusCode != 200 || strings.Contains(body, "Reusable") {
		t.Fatal("list leak", r.StatusCode, body)
	}
	if r, _ := s.do("DELETE", "/admin/canvas-templates/"+template["id"], nil, "X-CSRF-Token", s.csrf()); r.StatusCode != 404 {
		t.Fatal("delete isolation", r.StatusCode)
	}
	s.signIn(owner.Email)
	if r, _ := s.do("DELETE", "/admin/canvas-templates/"+template["id"], nil); r.StatusCode != 403 {
		t.Fatal("delete csrf", r.StatusCode)
	}
	if r, _ := s.do("DELETE", "/admin/canvas-templates/"+template["id"], nil, "X-CSRF-Token", s.csrf()); r.StatusCode != 200 {
		t.Fatal("delete", r.StatusCode)
	}
	if r, _ := s.do("GET", created["href"], nil); r.StatusCode != 200 {
		t.Fatal("template deletion removed board", r.StatusCode)
	}
	r, body = s.do("GET", path, nil)
	if r.StatusCode != 200 || !strings.Contains(body, "data-save-template") || !strings.Contains(body, "data-draft-dialog") {
		t.Fatal("missing UI", r.StatusCode)
	}
}

func TestCanvasDraftSimultaneousFirstClicksCreateOneItem(t *testing.T) {
	s := boot(t, content.New())
	owner := s.login("draft-concurrent@example.com", "admin", "en")
	path := newCanvas(t, s)
	card := lifecycleResult(t, s, path, owner, false)
	route := path + "/cards/" + card + "/draft"
	token := s.csrf()
	var wg sync.WaitGroup
	ids := make(chan string, 4)
	for range 4 {
		wg.Go(func() {
			r, body := s.do("POST", route, url.Values{"type": {"post"}}, "X-CSRF-Token", token)
			if r.StatusCode != 200 {
				t.Errorf("first click %d %s", r.StatusCode, body)
				return
			}
			var out map[string]string
			json.Unmarshal([]byte(body), &out)
			ids <- out["id"]
		})
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("different draft returned", id, first)
		}
	}
	var n int
	if e := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM content`).Scan(&n); e != nil || n != 1 {
		t.Fatal("duplicate content", n, e)
	}
	if e := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvas_drafts`).Scan(&n); e != nil || n != 1 {
		t.Fatal("duplicate source mapping", n, e)
	}
}
