package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func deleteCanvasJSON(t *testing.T, s *site, path, raw string, csrf bool) (int, map[string]any) {
	t.Helper()
	req, e := http.NewRequest("DELETE", s.srv.URL+path, strings.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-CSRF-Token", s.csrf())
	}
	r, e := s.c.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	out := map[string]any{}
	json.Unmarshal(b, &out)
	return r.StatusCode, out
}
func TestCanvasDeleteOwnerCSRFAndVersion(t *testing.T) {
	s := boot(t)
	owner := s.login("delete-owner@example.com", "admin", "en")
	path := newCanvas(t, s)
	if code, _ := deleteCanvasJSON(t, s, path, `{"version":1}`, false); code != 403 {
		t.Fatal("CSRF", code)
	}
	if code, _ := deleteCanvasJSON(t, s, path, `{"version":2}`, true); code != 409 {
		t.Fatal("version", code)
	}
	if code, _ := deleteCanvasJSON(t, s, path, `{"version":1,"owner":"x"}`, true); code != 422 {
		t.Fatal("strict JSON", code)
	}
	s.login("delete-other@example.com", "admin", "en")
	if code, _ := deleteCanvasJSON(t, s, path, `{"version":1}`, true); code != 404 {
		t.Fatal("foreign", code)
	}
	s.signIn(owner.Email)
	if code, _ := deleteCanvasJSON(t, s, path, `{"version":1}`, true); code != 200 {
		t.Fatal("owner", code)
	}
	if r, _ := s.do("GET", path, nil); r.StatusCode != 404 {
		t.Fatal("not deleted")
	}
}
func TestCanvasDeleteBusyAndMedia(t *testing.T) {
	s := boot(t)
	owner := s.login("delete-busy@example.com", "admin", "en")
	for _, tc := range []struct{ status, document, code string }{{"queued", "{}", "busy"}, {"running", "{}", "busy"}, {"waiting", "{}", "busy"}, {"waiting_agent", "{}", "busy"}, {"succeeded", `{"media_file":"private.png","media_size":10}`, "media"}, {"failed", `{"media_mime":"video/mp4"}`, "media"}} {
		path := newCanvas(t, s)
		id := strings.TrimPrefix(path, "/admin/canvases/")
		if _, e := s.app.Deps.DB.Exec(`INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,?,?,?)`, db.NewID(), id, owner.ID, db.NewID(), "node", tc.status, tc.document, db.Now()); e != nil {
			t.Fatal(e)
		}
		code, out := deleteCanvasJSON(t, s, path, `{"version":1}`, true)
		if code != 409 || out["error"] != tc.code {
			t.Fatal(tc, code, out)
		}
	}
}
func TestCanvasDeleteCascadesTextMetadataOnly(t *testing.T) {
	s := boot(t)
	owner := s.login("delete-cascade@example.com", "admin", "en")
	path := newCanvas(t, s)
	other := newCanvas(t, s)
	id := strings.TrimPrefix(path, "/admin/canvases/")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	_, key, e := keys.CreateKind(auth.WithUser(context.Background(), owner), auth.KeyKindAgent, "Delete test", owner.ID, []string{"canvas.run"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	run := db.NewID()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,'succeeded','{}',?)`, []any{run, id, owner.ID, db.NewID(), "node", db.Now()}},
		{`INSERT INTO admin_canvas_reviews(id,board_id,owner_id,node_id,document,expires_at) VALUES(?,?,?,?,'{}',?)`, []any{db.NewID(), id, owner.ID, "node", db.Now()}},
		{`INSERT INTO admin_canvas_grants(board_id,key_id) VALUES(?,?)`, []any{id, key.ID}},
		{`INSERT INTO admin_canvas_provenance(board_id,card_id,actor_kind,actor_name,run_id,created_at) VALUES(?,?,'provider','Test',?,?)`, []any{id, db.NewID(), run, db.Now()}},
		{`INSERT INTO admin_canvas_schedules(board_id,node_id,provider_node_id,interval,consent_hash,next_run_at) VALUES(?,?,'target','hourly','hash',?)`, []any{id, "schedule", db.Now()}},
	} {
		if _, e = s.app.Deps.DB.Exec(q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	if code, _ := deleteCanvasJSON(t, s, path, `{"version":1}`, true); code != 200 {
		t.Fatal(code)
	}
	for _, table := range []string{"admin_canvas_reviews", "admin_canvas_runs", "admin_canvas_grants", "admin_canvas_provenance", "admin_canvas_schedules"} {
		var n int
		if e = s.app.Deps.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE board_id=?`, id).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
	if r, _ := s.do("GET", other, nil); r.StatusCode != 200 {
		t.Fatal("other board changed")
	}
	var n int
	if e = s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='canvas.delete' AND target_id=?`, id).Scan(&n); e != nil || n != 1 {
		t.Fatal("audit", n, e)
	}
}

func TestCanvasDeleteRejectsKeyAndRevokedEditAuthority(t *testing.T) {
	s := boot(t)
	owner := s.login("delete-authority@example.com", "admin", "en")
	path := newCanvas(t, s)
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	secret, _, e := keys.CreateKind(auth.WithUser(context.Background(), owner), auth.KeyKindAgent, "Delete key", owner.ID, []string{"*"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	req, e := http.NewRequest("DELETE", s.srv.URL+path, strings.NewReader(`{"version":1}`))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	client := newClient()
	resp, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 && resp.StatusCode != 403 {
		t.Fatal("API key accepted", resp.StatusCode)
	}
	token := s.csrf()
	if _, e = s.app.Deps.DB.Exec(`UPDATE users SET role='editor' WHERE id=?`, owner.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.app.Deps.DB.Exec(`DELETE FROM role_permissions WHERE role='editor' AND permission='content.edit'`); e != nil {
		t.Fatal(e)
	}
	req, e = http.NewRequest("DELETE", s.srv.URL+path, strings.NewReader(`{"version":1}`))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", token)
	resp, e = s.c.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("revoked edit accepted", resp.StatusCode)
	}
	var n int
	if e = s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvases WHERE id=?`, strings.TrimPrefix(path, "/admin/canvases/")).Scan(&n); e != nil || n != 1 {
		t.Fatal("board altered", n, e)
	}
}
