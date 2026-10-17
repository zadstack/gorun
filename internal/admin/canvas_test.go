package admin_test

import (
	"context"
	"encoding/json"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func newCanvas(t *testing.T, s *site) string {
	t.Helper()
	r, _ := s.post("/admin/canvases", url.Values{"title": {"Personal board"}})
	if r.StatusCode != 303 {
		t.Fatalf("create %d", r.StatusCode)
	}
	return r.Header.Get("Location")
}
func canvasGet(t *testing.T, s *site, path string) map[string]any {
	t.Helper()
	r, body := s.do("GET", path+"/document", nil)
	if r.StatusCode != 200 {
		t.Fatalf("get %d", r.StatusCode)
	}
	var out map[string]any
	if e := json.Unmarshal([]byte(body), &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func canvasInput(cards ...map[string]any) map[string]any {
	return map[string]any{"title": "Saved board", "version": 1, "cards": cards}
}
func TestCanvasOwnershipCASAndPersistence(t *testing.T) {
	s := boot(t)
	u := s.login("canvas-owner@example.com", "admin", "en")
	path := newCanvas(t, s)
	note := map[string]any{"id": "12345678-1234-4123-8123-123456789abc", "kind": "note", "title": "<script>bad</script>", "body": "Private note", "x": 20, "y": 40}
	in := canvasInput(note)
	if code, _ := studioJSON(t, s, path+"/document", in, false); code != 403 {
		t.Fatal("csrf", code)
	}
	if code, out := studioJSON(t, s, path+"/document", in, true); code != 200 || out["version"] != float64(2) {
		t.Fatalf("save %d %#v", code, out)
	}
	if code, _ := studioJSON(t, s, path+"/document", in, true); code != 409 {
		t.Fatal("stale", code)
	}
	s.signIn(u.Email)
	doc := canvasGet(t, s, path)
	if doc["version"] != float64(2) || len(doc["cards"].([]any)) != 1 {
		t.Fatal("not persisted")
	}
	s.login("canvas-other@example.com", "admin", "en")
	for _, route := range []string{path, path + "/document"} {
		if r, _ := s.do("GET", route, nil); r.StatusCode != 404 {
			t.Fatal("read leaked", r.StatusCode)
		}
	}
	in["version"] = 2
	if code, _ := studioJSON(t, s, path+"/document", in, true); code != 404 {
		t.Fatal("write leaked", code)
	}
	_, page := s.do("GET", "/admin/canvases", nil)
	if strings.Contains(page, "Saved board") {
		t.Fatal("list leaked")
	}
}
func TestCanvasValidationAndContentPermissions(t *testing.T) {
	s := boot(t, content.New())
	owner := s.login("canvas-content-owner@example.com", "admin", "en")
	item := studioPost(t, s, owner)
	path := newCanvas(t, s)
	cases := []map[string]any{
		{"id": "bad", "kind": "note"},
		{"id": "12345678-1234-4123-8123-123456789abc", "kind": "unknown"},
		{"id": "12345678-1234-4123-8123-123456789abc", "kind": "note", "x": -1},
		{"id": "12345678-1234-4123-8123-123456789abc", "kind": "note", "body": strings.Repeat("x", 4001)},
	}
	for _, u := range []string{"javascript:alert(1)", "//evil.test", "https://user:pass@host.test", "/\\evil.test", "https://x.test/\n"} {
		cases = append(cases, map[string]any{"id": "12345678-1234-4123-8123-123456789abc", "kind": "link", "url": u})
	}
	for _, card := range cases {
		if code, _ := studioJSON(t, s, path+"/document", canvasInput(card), true); code != 422 {
			t.Fatalf("invalid accepted %d %#v", code, card)
		}
	}
	ref := map[string]any{"id": "12345678-1234-4123-8123-123456789abc", "kind": "content", "content_id": item.ID}
	if code, out := studioJSON(t, s, path+"/document", canvasInput(ref), true); code != 200 || !strings.Contains(string(mustJSON(t, out)), "Saved article") {
		t.Fatalf("content %d", code)
	}
	s.login("canvas-author@example.com", "author", "en")
	p2 := newCanvas(t, s)
	if code, _ := studioJSON(t, s, p2+"/document", canvasInput(ref), true); code != 422 {
		t.Fatal("foreign content ref", code)
	}
	ref["content_id"] = "01AAAAAAAAAAAAAAAAAAAAAAAA"
	if code, _ := studioJSON(t, s, p2+"/document", canvasInput(ref), true); code != 422 {
		t.Fatal("unknown content", code)
	}
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestCanvasConcurrentCASAndStrictDocument(t *testing.T) {
	s := boot(t)
	s.login("canvas-cas@example.com", "admin", "en")
	path := newCanvas(t, s)
	base := map[string]any{"id": "12345678-1234-4123-8123-123456789abc", "kind": "note"}
	if code, _ := studioJSON(t, s, path+"/document", canvasInput(base, base), true); code != 422 {
		t.Fatal("duplicate", code)
	}
	unknown := canvasInput(base)
	unknown["owner_id"] = "another-user"
	if code, _ := studioJSON(t, s, path+"/document", unknown, true); code != 422 {
		t.Fatal("unknown field", code)
	}
	checklist := map[string]any{"id": "12345678-1234-4123-8123-123456789abc", "kind": "checklist", "items": []any{map[string]any{"text": "First task", "checked": true}}}
	statuses := make(chan int, 2)
	for range 2 {
		go func() { code, _ := studioJSON(t, s, path+"/document", canvasInput(checklist), true); statuses <- code }()
	}
	a, b := <-statuses, <-statuses
	if !(a == 200 && b == 409 || a == 409 && b == 200) {
		t.Fatalf("CAS winners %d %d", a, b)
	}
	doc := canvasGet(t, s, path)
	card := doc["cards"].([]any)[0].(map[string]any)
	if card["items"].([]any)[0].(map[string]any)["checked"] != true {
		t.Fatal("checklist not persisted")
	}
}

func TestCanvasReferencePickerAndRevokedAccess(t *testing.T) {
	s := boot(t, content.New())
	u := s.login("canvas-ref@example.com", "admin", "en")
	it := studioPost(t, s, u)
	path := newCanvas(t, s)
	cs, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	ctx := auth.WithUser(context.Background(), u)
	if e := cs.SaveType(ctx, content.Type{Slug: "private_note", Public: false, Name: map[string]string{"en": "Private"}}); e != nil {
		t.Fatal(e)
	}
	private, e := cs.Create(ctx, content.Input{Type: "private_note", Title: "INTERNAL_SENTINEL", AuthorID: u.ID})
	if e != nil {
		t.Fatal(e)
	}
	ref := map[string]any{"id": "12345678-1234-4123-8123-123456789abc", "kind": "content", "content_id": private.ID}
	if code, _ := studioJSON(t, s, path+"/document", canvasInput(ref), true); code != 422 {
		t.Fatal("internaltype reference accepted", code)
	}
	r, body := s.do("GET", path+"/references?q=Saved", nil)
	if r.StatusCode != 200 || !strings.Contains(body, it.ID) || strings.Contains(body, "INTERNAL_SENTINEL") {
		t.Fatal("picker", r.StatusCode, body)
	}
	ref["content_id"] = it.ID
	if code, _ := studioJSON(t, s, path+"/document", canvasInput(ref), true); code != 200 {
		t.Fatal(code)
	}
	manager := s.login("canvas-ref-manager@example.com", "admin", "en")
	if r, _ := s.post("/admin/users/"+u.ID, url.Values{"name": {"Author"}, "email": {u.Email}, "role": {"author"}, "locale": {"en"}, "status": {"active"}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	// Reassigning the linked article removes current read access without touching the board.
	if _, e := s.app.Deps.DB.Exec(`UPDATE content SET author_id=? WHERE id=?`, manager.ID, it.ID); e != nil {
		t.Fatal(e)
	}
	s.signIn(u.Email)
	doc := canvasGet(t, s, path)
	if len(doc["references"].(map[string]any)) != 0 {
		t.Fatal("revoked content title leaked")
	}
	r, body = s.do("GET", path+"/references", nil)
	if r.StatusCode != 200 || strings.Contains(body, it.ID) {
		t.Fatal("picker ignored ownership")
	}
	s.login("canvas-picker-other@example.com", "admin", "en")
	if r, _ := s.do("GET", path+"/references", nil); r.StatusCode != 404 {
		t.Fatal("picker foreignboard", r.StatusCode)
	}
}
