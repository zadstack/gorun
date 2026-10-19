package admin_test

import (
	"context"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"strings"
	"testing"
)

func TestAuditDashboardReviewsPrivateOwnerEscapedAndNoMutation(t *testing.T) {
	s := boot(t)
	owner := s.login("review-owner@example.test", "admin", "en")
	path := newCanvas(t, s)
	board := strings.TrimPrefix(path, "/admin/canvases/")
	if _, e := s.app.Deps.DB.Exec(`UPDATE admin_canvases SET title=? WHERE id=?`, `<img src=x onerror=alert(1)>`, board); e != nil {
		t.Fatal(e)
	}
	if _, e := s.app.Deps.DB.Exec(`INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,created_at) VALUES(?,?,?,'review','node','waiting_tool',?)`, db.NewID(), board, owner.ID, db.Now()); e != nil {
		t.Fatal(e)
	}
	_, body := s.do("GET", "/admin", nil)
	mustContain(t, body, `data-widget="review_queue"`, "&lt;img", path, `data-dashboard-customize`)
	mustNotContain(t, body, `<img src=x`)
	var count int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvas_runs WHERE status='waiting_tool'`).Scan(&count)
	if count != 1 {
		t.Fatal("dashboard changed approval")
	}
	s.login("review-other@example.test", "admin", "en")
	_, body = s.do("GET", "/admin", nil)
	mustNotContain(t, body, `data-widget="review_queue"`, path)
}

func TestAuditDockBundleSummaryAuthorityAndRedaction(t *testing.T) {
	s := boot(t)
	owner := s.login("bundle-dock@example.test", "admin", "en")
	keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
	_, key, e := keys.CreateKind(context.Background(), auth.KeyKindAgent, "Writer", owner.ID, []string{"*"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	profile, version := db.NewID(), db.NewID()
	values := map[string]any{
		"connections.profile." + profile: map[string]any{"id": profile, "version": version, "kind": "mcp", "name": "Archive reference", "endpoint": "https://archive.example/private-path-token", "secret": "NOT_FOR_DOCK", "test_status": "pass", "tested_at": "2026-10-19T10:00:00Z"},
		"agents.bundle." + key.ID:        map[string]any{"owner_id": owner.ID, "version": db.NewID(), "skills": []any{map[string]string{"name": "Editorial policy", "instructions": "PRIVATE_INSTRUCTIONS", "resource_name": "Policy", "resource_text": "PRIVATE_RESOURCE"}}, "mcp": []any{map[string]string{"id": profile, "version": version}}},
	}
	for k, v := range values {
		if e = s.app.Deps.Settings.Set(context.Background(), k, v); e != nil {
			t.Fatal(e)
		}
	}
	path := newCanvas(t, s)
	card := map[string]any{"id": "11111111-1111-4111-8111-111111111111", "kind": "agent", "agent_id": key.ID, "title": "Writer action"}
	if code, _ := studioJSON(t, s, path+"/document", canvasInput(card), true); code != 200 {
		t.Fatal(code)
	}
	_, body := s.do("GET", "/admin/x/agents-dock", nil)
	mustContain(t, body, `"bundle":`, "Editorial policy", "Archive reference", `"status":"pass"`)
	mustNotContain(t, body, "NOT_FOR_DOCK", "private-path-token", "PRIVATE_INSTRUCTIONS", "PRIVATE_RESOURCE")
	if _, e = s.app.Deps.DB.Exec(`UPDATE users SET role='editor' WHERE id=?`, owner.ID); e != nil {
		t.Fatal(e)
	}
	_, body = s.do("GET", "/admin/x/agents-dock", nil)
	mustNotContain(t, body, `"bundle":`, "Editorial policy", "Archive reference")
}
