package admin_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/db"
)

func recoveryCopyJSON(t *testing.T, s *site, path, raw string, csrf bool) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", s.srv.URL+path+"/recovery-copy", strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-CSRF-Token", s.csrf())
	}
	resp, err := s.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

func TestCanvasRecoveryCopyPreservesContentWithoutAuthority(t *testing.T) {
	s := boot(t)
	owner := s.login("recovery-owner@example.com", "admin", "en")
	path := newCanvas(t, s)
	lifecycleResult(t, s, path, owner, true)
	original := canvasGet(t, s, path)
	cards := []map[string]any{}
	for i, kind := range []string{"note", "prompt", "checklist", "link", "result", "agent", "provider", "source", "content", "widget", "schedule"} {
		card := map[string]any{
			"id": fmt.Sprintf("%08d-1111-4111-8111-111111111111", i), "kind": kind,
			"title": "Saved " + kind, "body": "Private <script>literal</script> " + kind,
			"x": i * 24, "y": i * 48,
			"run_id": "01J00000000000000000000001", "agent_id": "01J00000000000000000000002",
			"provider_id": "native-text", "source_type": "post", "source_limit": 3,
			"source_ids": []string{"01J00000000000000000000003"}, "content_id": "01J00000000000000000000004",
			"widget_id": "private-widget", "generation_kind": "image", "preset_id": "private-preset", "interval": "daily",
		}
		if kind == "link" {
			card["url"] = "https://example.org/review"
		}
		if kind == "checklist" {
			card["items"] = []any{map[string]any{"text": "Human review", "checked": true}}
		}
		cards = append(cards, card)
	}
	payload := map[string]any{"title": "Recovered private work", "cards": cards, "edges": []any{map[string]string{"from": cards[1]["id"].(string), "to": cards[5]["id"].(string)}}}
	code, result := recoveryCopyJSON(t, s, path, string(mustJSON(t, payload)), true)
	if code != http.StatusCreated || result["id"] == nil || result["href"] != "/admin/canvases/"+result["id"].(string) {
		t.Fatalf("recovery copy: %d %#v", code, result)
	}
	copy := canvasGet(t, s, result["href"].(string))
	if copy["version"] != float64(1) || copy["title"] != payload["title"] || len(copy["edges"].([]any)) != 0 || len(copy["cards"].([]any)) != len(cards) {
		t.Fatalf("atomic recovered document: %#v", copy)
	}
	ids := map[string]bool{}
	uuid := regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
	for i, item := range copy["cards"].([]any) {
		card := item.(map[string]any)
		id := card["id"].(string)
		if !uuid.MatchString(id) || ids[id] || id == cards[i]["id"] || card["title"] != cards[i]["title"] || card["body"] != cards[i]["body"] || card["x"] != float64(i*24) || card["y"] != float64(i*48) {
			t.Fatalf("recovered card %d: %#v", i, card)
		}
		ids[id] = true
		kind := cards[i]["kind"].(string)
		if i >= 4 {
			kind = "note"
		}
		if card["kind"] != kind {
			t.Fatalf("execution/reference card must become a note: %#v", card)
		}
		for _, key := range []string{"run_id", "agent_id", "provider_id", "source_type", "source_limit", "source_ids", "content_id", "widget_id", "generation_kind", "preset_id", "interval"} {
			if _, exists := card[key]; exists {
				t.Errorf("authority %s survived recovery: %#v", key, card)
			}
		}
		if kind == "link" && card["url"] != "https://example.org/review" || kind == "checklist" && len(card["items"].([]any)) != 1 {
			t.Fatalf("safe card fields lost: %#v", card)
		}
	}
	if after := canvasGet(t, s, path); !reflect.DeepEqual(after, original) {
		t.Fatalf("source changed during recovery: before %#v after %#v", original, after)
	}
	for _, table := range []string{"admin_canvas_runs", "admin_canvas_schedules", "admin_canvas_grants", "admin_canvas_inbound", "admin_canvas_provenance"} {
		var n int
		if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE board_id=?`, result["id"]).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s state copied: %d %v", table, n, err)
		}
	}
	if s.auditCount("canvas.recovery_copy", result["id"].(string)) != 1 {
		t.Fatal("recovery copy audit missing")
	}
}

func TestCanvasRecoveryCopyOwnershipCSRFAndStrictValidation(t *testing.T) {
	s := boot(t)
	owner := s.login("recovery-validation@example.com", "admin", "en")
	path := newCanvas(t, s)
	valid := `{"title":"Recovered","cards":[],"edges":[]}`
	if code, _ := recoveryCopyJSON(t, s, path, valid, false); code != http.StatusForbidden {
		t.Fatalf("CSRF: %d", code)
	}
	s.login("recovery-other@example.com", "admin", "en")
	if code, _ := recoveryCopyJSON(t, s, path, valid, true); code != http.StatusNotFound {
		t.Fatalf("foreign source: %d", code)
	}
	s.signIn(owner.Email)
	tooMany := make([]map[string]any, 101)
	for i := range tooMany {
		tooMany[i] = map[string]any{"kind": "note"}
	}
	tooManyEdges := make([]map[string]string, 201)
	for i := range tooManyEdges {
		tooManyEdges[i] = map[string]string{"from": "old-prompt", "to": "old-agent"}
	}
	cases := []string{
		`{"title":"Recovered","cards":[],"edges":[],"version":1}`,
		`{"title":"Recovered","cards":[],"edges":[],"owner_id":"other"}`,
		`{"title":"Recovered","cards":[],"edges":[],"grants":[]}`,
		`{"title":"Recovered","cards":[],"edges":[]} {}`,
		`{"title":"Recovered","cards":null,"edges":[]}`,
		`{"title":"Recovered","cards":[],"edges":null}`,
		`{"title":"Recovered","cards":[{"kind":"unknown"}],"edges":[]}`,
		`{"title":"Recovered","cards":[{"kind":"note","x":-1}],"edges":[]}`,
		`{"title":"Recovered","cards":[{"kind":"link","url":"javascript:alert(1)"}],"edges":[]}`,
		`{"title":"Recovered","cards":[{"kind":"note","secret":"unexpected"}],"edges":[]}`,
		string(mustJSON(t, map[string]any{"title": strings.Repeat("a", 121), "cards": []any{}, "edges": []any{}})),
		string(mustJSON(t, map[string]any{"title": "Recovered", "cards": tooMany, "edges": []any{}})),
		string(mustJSON(t, map[string]any{"title": "Recovered", "cards": []any{}, "edges": tooManyEdges})),
		string(mustJSON(t, map[string]any{"title": "Recovered", "cards": []any{map[string]any{"kind": "note", "body": strings.Repeat("x", 4001)}}, "edges": []any{}})),
		strings.Repeat(" ", 256<<10) + valid,
	}
	for i, raw := range cases {
		if code, out := recoveryCopyJSON(t, s, path, raw, true); code != http.StatusUnprocessableEntity || out["error"] != "invalid" {
			t.Errorf("invalid case %d: %d %#v", i, code, out)
		}
	}
	var n int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvases WHERE owner_id=?`, owner.ID).Scan(&n); err != nil || n != 1 || s.auditCount("canvas.recovery_copy") != 0 {
		t.Fatalf("invalid copies left partial boards: %d %v", n, err)
	}
}

func TestCanvasRecoveryCopyBoardLimitIsAtomic(t *testing.T) {
	s := boot(t)
	owner := s.login("recovery-limit@example.com", "admin", "en")
	path := newCanvas(t, s)
	for range 99 {
		if _, err := s.app.Deps.DB.Exec(`INSERT INTO admin_canvases(id,owner_id,title,document_json,created_at,updated_at) VALUES(?,?,?,'[]',?,?)`, db.NewID(), owner.ID, "Limit fixture", db.Now(), db.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := recoveryCopyJSON(t, s, path, `{"title":"Recovered","cards":[],"edges":[]}`, true); code != http.StatusUnprocessableEntity || out["error"] != "board_limit" {
		t.Fatalf("board limit: %d %#v", code, out)
	}
	var n int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM admin_canvases WHERE owner_id=?`, owner.ID).Scan(&n); err != nil || n != 100 || s.auditCount("canvas.recovery_copy") != 0 {
		t.Fatalf("failed limit left a partial board: %d %v", n, err)
	}
}
