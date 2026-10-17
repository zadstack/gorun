package admin_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestPageAssistantReviewAndAtomicPartialEdit(t *testing.T) {
	s := boot(t, content.New())
	u := s.login("page-assistant@example.com", "admin", "en")
	it := studioPost(t, s, u)
	path := "/admin/x/page-assistant/" + it.ID
	r, body := s.do("GET", path, nil)
	if r.StatusCode != 200 {
		t.Fatalf("bootstrap %d", r.StatusCode)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(body), &meta); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"version": meta["version"], "field": "body", "previous": "Original", "replacement": "Reviewed", "reviewed": true}
	if code, _ := studioJSON(t, s, path+"/apply", in, false); code != 403 {
		t.Fatalf("CSRF %d", code)
	}
	in["reviewed"] = false
	if code, _ := studioJSON(t, s, path+"/apply", in, true); code != 422 {
		t.Fatalf("review %d", code)
	}
	in["reviewed"] = true
	if code, _ := studioJSON(t, s, path+"/apply", in, true); code != 200 {
		t.Fatalf("apply %d", code)
	}
	if code, _ := studioJSON(t, s, path+"/apply", in, true); code != 409 {
		t.Fatalf("stale %d", code)
	}
	svc, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	after, err := svc.Get(context.Background(), it.ID)
	if err != nil || after.Body != "Reviewed saved body" || after.Status != "draft" || after.Title != it.Title {
		t.Fatalf("unrelated content changed: %v", err)
	}
	s.login("other-author@example.com", "author", "en")
	if r, _ := s.do("GET", path, nil); r.StatusCode != 403 {
		t.Fatalf("other owner %d", r.StatusCode)
	}
}

func TestPageAssistantStudioQuestionWaitsForHumanApply(t *testing.T) {
	s := boot(t, content.New())
	u := s.login("assistant-provider@example.com", "admin", "en")
	it := studioPost(t, s, u)
	fake := newFakeAI(t, 200, openAIReply("Reviewed excerpt"), nil)
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {fake.srv.URL}, "model": {"test-model"}})
	path := "/admin/x/studio/" + it.ID
	in := map[string]any{"provider": "native-text", "kind": "text", "prompt": "Suggest an excerpt, do not publish", "title": it.Title, "text": it.Body, "reviewed": true}
	code, review := studioJSON(t, s, path+"/preview", in, true)
	if code != 200 || fake.calls.Load() != 0 {
		t.Fatal("preview must not call provider")
	}
	in["request_id"] = review["request_id"]
	in["review_hash"] = review["review_hash"]
	code, candidate := studioJSON(t, s, path+"/generate", in, true)
	if code != 202 {
		t.Fatalf("generate %d", code)
	}
	deadline := time.Now().Add(8 * time.Second)
	status := ""
	for time.Now().Before(deadline) {
		if err := s.app.Deps.DB.QueryRow("SELECT status FROM admin_studio_candidates WHERE id=?", candidate["id"]).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "ready" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if status != "ready" || fake.calls.Load() != 1 {
		t.Fatal("candidate failed or duplicate outbound request")
	}
	svc, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	before, err := svc.Get(context.Background(), it.ID)
	if err != nil || content.EditToken(before) != content.EditToken(it) {
		t.Fatal("answer mutated page without human review")
	}
	_, raw := s.do("GET", path, nil)
	var list struct{ Items []struct{ ID, Text string } }
	if err = json.Unmarshal([]byte(raw), &list); err != nil || len(list.Items) != 1 {
		t.Fatal("private answer missing")
	}
	apply := map[string]any{"version": content.EditToken(it), "field": "excerpt", "previous": it.Excerpt, "replacement": list.Items[0].Text, "reviewed": true}
	if code, _ = studioJSON(t, s, "/admin/x/page-assistant/"+it.ID+"/apply", apply, true); code != 200 {
		t.Fatalf("human apply %d", code)
	}
	after, err := svc.Get(context.Background(), it.ID)
	if err != nil || after.Excerpt != "Reviewed excerpt" || after.Body != it.Body || after.Status != "draft" {
		t.Fatal("explicit edit did not preserve unrelated fields")
	}
}

func TestPageAssistantRejectsAmbiguousSelectionAndSanitizesHTML(t *testing.T) {
	s := boot(t, content.New())
	u := s.login("assistant-selection@example.com", "admin", "en")
	svc, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	it, err := svc.Create(auth.WithUser(context.Background(), u), content.Input{Type: "post", Title: "Selection", Body: "<p>Repeat</p><p>Repeat</p>", Status: "draft", AuthorID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/x/page-assistant/" + it.ID + "/apply"
	in := map[string]any{"version": content.EditToken(it), "field": "body", "previous": "Repeat", "replacement": "Only once", "reviewed": true}
	if code, _ := studioJSON(t, s, path, in, true); code != 422 {
		t.Fatalf("ambiguous %d", code)
	}
	in["previous"] = it.Body
	in["replacement"] = "<p>Approved</p><script>alert(1)</script>"
	if code, _ := studioJSON(t, s, path, in, true); code != 200 {
		t.Fatalf("reviewed HTML %d", code)
	}
	after, err := svc.Get(context.Background(), it.ID)
	if err != nil || strings.Contains(after.Body, "script") || !strings.Contains(after.Body, "Approved") {
		t.Fatal("unsafe or missing saved body")
	}
	changed := content.InputFromItem(after)
	changed.Title = "Concurrent human title"
	if _, err = svc.Update(auth.WithUser(context.Background(), u), it.ID, changed); err != nil {
		t.Fatal(err)
	}
	in["version"] = content.EditToken(after)
	in["previous"] = after.Body
	in["replacement"] = "Late AI replacement"
	if code, _ := studioJSON(t, s, path, in, true); code != 409 {
		t.Fatalf("human edit overwritten %d", code)
	}
}

func TestPageAssistantBuilderAndPublishedGuards(t *testing.T) {
	s := boot(t, content.New())
	u := s.login("assistant-builder@example.com", "admin", "en")
	svc, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	it, err := svc.Create(auth.WithUser(context.Background(), u), content.Input{Type: "page", Title: "Builder page", Body: "Rendered layout", Template: "builder", Status: "published", AuthorID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/x/page-assistant/" + it.ID
	in := map[string]any{"version": content.EditToken(it), "field": "body", "previous": it.Body, "replacement": "Replacement", "reviewed": true}
	if code, _ := studioJSON(t, s, path+"/apply", in, true); code != 422 {
		t.Fatalf("builder body %d", code)
	}
	in["field"] = "title"
	in["previous"] = it.Title
	in["replacement"] = "Approved title"
	if code, _ := studioJSON(t, s, path+"/apply", in, true); code != 200 {
		t.Fatalf("builder metadata %d", code)
	}
	after, err := svc.Get(context.Background(), it.ID)
	if err != nil || after.Body != it.Body || after.Status != "published" {
		t.Fatal("builder layout or publication changed")
	}
	// A contributor can ask about their assigned page but cannot change live content.
	c := s.login("assistant-contributor@example.com", "contributor", "en")
	_, err = s.app.Deps.DB.Exec("UPDATE content SET author_id=? WHERE id=?", c.ID, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, body := s.do("GET", path, nil)
	if r.StatusCode != 200 {
		t.Fatalf("contributor read %d", r.StatusCode)
	}
	var meta map[string]any
	_ = json.Unmarshal([]byte(body), &meta)
	if meta["can_apply"] != false {
		t.Fatal("contributor may apply live changes")
	}
	in["version"] = meta["version"]
	in["previous"] = "Approved title"
	if code, _ := studioJSON(t, s, path+"/apply", in, true); code != 403 {
		t.Fatalf("contributor live edit %d", code)
	}
}
