package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/webhooks"
)

func addActivityNote(t *testing.T, s *site, title string) (string, string) {
	t.Helper()
	path := newCanvas(t, s)
	id := "12345678-1234-4123-8123-123456789abc"
	card := map[string]any{"id": id, "kind": "note", "title": title, "body": "<script>private-result</script>", "x": 24, "y": 48}
	if code, _ := studioJSON(t, s, path+"/document", canvasInput(card), true); code != 200 {
		t.Fatal(code)
	}
	_, err := s.app.Deps.DB.Exec(`INSERT INTO admin_canvas_provenance(board_id,card_id,actor_kind,actor_name,run_id,created_at) VALUES(?,?,'agent','<b>Research agent</b>','',?)`, strings.TrimPrefix(path, "/admin/canvases/"), id, db.Now())
	if err != nil {
		t.Fatal(err)
	}
	return path, id
}

func TestCanvasActivityOwnerEscapingRefreshAndMissingModule(t *testing.T) {
	s := boot(t)
	owner := s.login("activity-owner@example.test", "admin", "en")
	path, id := addActivityNote(t, s, "<img src=x onerror=alert(1)>")
	response, page := s.do("GET", "/admin/x/canvas-activity", nil)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal(response.StatusCode, response.Header)
	}
	mustContain(t, page, "&lt;script&gt;private-result&lt;/script&gt;", "&lt;b&gt;Research agent&lt;/b&gt;", "&lt;img", path+"#card-"+id, "Webhook activity is available")
	mustNotContain(t, page, "<script>private-result</script>", "<img src=x", "ZgotmplZ")
	_, dashboard := s.do("GET", "/admin", nil)
	mustContain(t, dashboard, "Canvas notes are private to your account.", `hx-get="/admin/x/canvas-activity"`)
	if strings.Index(dashboard, `id="canvas-activity"`) > strings.Index(dashboard, `class="canvas-gallery-grid"`) {
		t.Fatal("activity must precede saved boards")
	}
	s.login("activity-other@example.test", "admin", "en")
	_, page = s.do("GET", "/admin/x/canvas-activity", nil)
	mustNotContain(t, page, "private-result", path, "Research agent")
	mustContain(t, page, "Agent results and permitted webhook activity will appear here.")
	s.signIn(owner.Email)
	if _, err := s.app.Deps.DB.Exec(`DELETE FROM admin_canvases WHERE id=?`, strings.TrimPrefix(path, "/admin/canvases/")); err != nil {
		t.Fatal(err)
	}
	_, page = s.do("GET", "/admin/x/canvas-activity", nil)
	mustNotContain(t, page, "private-result", path)
	s.c = newClient()
	response, _ = s.do("GET", "/admin/x/canvas-activity", nil)
	if response.StatusCode != http.StatusSeeOther {
		t.Fatal("anonymous access", response.StatusCode)
	}
}

func TestCanvasActivityWebhookPermissionAndSafeMetadata(t *testing.T) {
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), admin.New(), webhooks.New()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	s := &site{t: t, srv: srv, app: app, c: newClient()}
	s.login("activity-admin@example.test", "admin", "en")
	_, err = s.app.Deps.DB.Exec(`INSERT INTO webhooks(id,name,url,secret_enc,created_at,updated_at) VALUES('hook','<b>Orders</b>','https://private.example/URL_SENTINEL','SECRET_SENTINEL',?,?)`, db.Now(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.app.Deps.DB.Exec(`INSERT INTO webhook_deliveries(id,webhook_id,event,payload,status,error,created_at) VALUES('delivery','hook','content.created','PAYLOAD_SENTINEL','failed','ERROR_SENTINEL',?)`, db.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/x/canvas-activity", nil)
	mustContain(t, page, "&lt;b&gt;Orders&lt;/b&gt;", "content.created", "Failed", `href="/admin/webhooks/hook"`)
	mustNotContain(t, page, "URL_SENTINEL", "SECRET_SENTINEL", "PAYLOAD_SENTINEL", "ERROR_SENTINEL", "Webhook activity is available")
	s.login("activity-editor@example.test", "editor", "en")
	response, page := s.do("GET", "/admin/x/canvas-activity", nil)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	mustNotContain(t, page, "Orders", "content.created", "/admin/webhooks/hook")
	mustContain(t, page, "Webhook activity is available")
}

func TestCanvasActivityLimitAndDeletedResult(t *testing.T) {
	s := boot(t)
	s.login("activity-order@example.test", "admin", "en")
	path, _ := addActivityNote(t, s, "Old result")
	board := strings.TrimPrefix(path, "/admin/canvases/")
	cards := make([]map[string]any, 0, 10)
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("%08d-1234-4123-8123-123456789abc", i)
		cards = append(cards, map[string]any{"id": id, "kind": "note", "title": fmt.Sprintf("Result %02d", i)})
		_, err := s.app.Deps.DB.Exec(`INSERT INTO admin_canvas_provenance(board_id,card_id,actor_kind,actor_name,run_id,created_at) VALUES(?,?,'agent','Agent',?,?)`, board, id, fmt.Sprintf("run-%d", i), fmt.Sprintf("2026-10-%02dT12:00:00Z", i+1))
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.app.Deps.DB.Exec(`UPDATE admin_canvases SET document_json=? WHERE id=?`, string(raw), board); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/x/canvas-activity", nil)
	if strings.Count(page, `data-activity-kind="note"`) != 8 {
		t.Fatal("activity must cap recent results at eight")
	}
	mustNotContain(t, page, "Old result", "Result 00", "Result 01")
	if strings.Index(page, "Result 09") > strings.Index(page, "Result 08") {
		t.Fatal("latest results must appear first")
	}
}
