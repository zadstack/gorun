package admin_test

import (
	"strings"
	"testing"
)

func TestCanvasCancelOwnershipCSRFAndState(t *testing.T) {
	s := boot(t)
	owner := s.login("cancel-owner@example.com", "admin", "en")
	path := newCanvas(t, s)
	board := strings.TrimPrefix(path, "/admin/canvases/")
	for _, state := range []string{"requested", "queued", "waiting_agent", "waiting", "running", "succeeded", "uncertain"} {
		_, e := s.app.Deps.DB.Exec(`INSERT INTO admin_canvas_runs(id,board_id,owner_id,request_id,node_id,status,document,created_at) VALUES(?,?,?,?,?,?, '{}','2026-01-01T00:00:00Z')`, state, board, owner.ID, state, "node", state)
		if e != nil {
			t.Fatal(e)
		}
	}
	endpoint := path + "/operations/runs/waiting/cancel"
	if code, _ := studioJSON(t, s, endpoint, map[string]any{}, false); code != 403 {
		t.Fatal("CSRF", code)
	}
	s.login("cancel-other@example.com", "admin", "en")
	if code, _ := studioJSON(t, s, endpoint, map[string]any{}, true); code != 404 {
		t.Fatal("other owner", code)
	}
	s.signIn(owner.Email)
	for _, state := range []string{"requested", "queued", "waiting_agent", "waiting"} {
		for range 2 {
			if code, out := studioJSON(t, s, path+"/operations/runs/"+state+"/cancel", map[string]any{}, true); code != 200 {
				t.Fatal(state, code, out)
			}
		}
		var actual, finished string
		if e := s.app.Deps.DB.QueryRow(`SELECT status,finished_at FROM admin_canvas_runs WHERE id=?`, state).Scan(&actual, &finished); e != nil || actual != "cancelled" || finished == "" {
			t.Fatal(actual, finished, e)
		}
		if code, _ := studioJSON(t, s, path+"/operations/runs/"+state+"/check", map[string]any{}, true); code != 409 {
			t.Fatal("late poll", state, code)
		}
	}
	for _, state := range []string{"running", "succeeded", "uncertain"} {
		if code, _ := studioJSON(t, s, path+"/operations/runs/"+state+"/cancel", map[string]any{}, true); code != 409 {
			t.Fatal("terminal boundary", state, code)
		}
	}
}
