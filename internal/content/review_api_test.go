package content_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
)

// Review fix 1: PATCH replaces the fields list exactly; new fields never
// inherit attributes of the old ones at the same position.
func TestAPIPatchTypeReplacesFieldsExactly(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app, mustUser(t, app, "admin@example.com", "admin"))
	min := 0.0
	c.do("POST", "/v1/types", content.Type{Slug: "product", Name: map[string]string{"en": "P"}, Fields: []content.Field{
		{Key: "price", Type: "number", Indexed: true, Required: true, Min: &min},
		{Key: "tier", Type: "select", Options: []content.Option{{Value: "a"}}},
	}})
	body := []content.Field{{Key: "colour", Type: "text"}}
	code, env := c.do("PATCH", "/v1/types/product", map[string]any{"fields": body})
	if code != 200 {
		t.Fatalf("patch: %d %+v", code, env)
	}
	got, _ := contentService(t, app).GetType(context.Background(), "product")
	if !reflect.DeepEqual(got.Fields, body) || got.Name["en"] != "P" {
		t.Fatalf("stored fields %+v, want %+v", got.Fields, body)
	}
}

// Review fix 3 (API): deleting a relationship target keeps the item editable.
func TestAPIPatchAfterRelationshipTargetDeleted(t *testing.T) {
	app := bootApp(t)
	c := newAPIClient(t, app, mustUser(t, app, "admin@example.com", "admin"))
	c.do("POST", "/v1/types", content.Type{Slug: "book", Name: map[string]string{"en": "Books"}, Fields: []content.Field{
		{Key: "page", Type: "relationship", TargetType: "page"}}})
	cs := contentService(t, app)
	ctx := context.Background()
	b, _ := cs.Create(ctx, content.Input{Type: "page", Title: "B"})
	a, err := cs.Create(ctx, content.Input{Type: "book", Title: "A", Fields: map[string]any{"page": b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	cs.Delete(ctx, b.ID, true)
	code, env := c.do("PATCH", "/v1/content/"+a.ID, map[string]any{"title": "A2"})
	var it map[string]any
	json.Unmarshal(env.Data, &it)
	if code != 200 || it["fields"].(map[string]any)["page"] != nil {
		t.Fatalf("patch after target deleted: %d %v", code, it)
	}
}

// Review fix 5 (API): PATCH status=trash is a validation error; DELETE trashes.
func TestAPIPatchStatusTrashRejected(t *testing.T) {
	app := bootApp(t)
	author := mustUser(t, app, "author@example.com", "author")
	c := newAPIClient(t, app, author)
	_, env := c.do("POST", "/v1/content", map[string]any{"type": "post", "title": "Mine"})
	var it map[string]any
	json.Unmarshal(env.Data, &it)
	id := it["id"].(string)
	if code, env := c.do("PATCH", "/v1/content/"+id, map[string]any{"status": "trash"}); code != 422 || env.Error.Fields["status"] == "" {
		t.Fatalf("patch to trash: %d %+v", code, env)
	}
	c.user = mustUser(t, app, "editor@example.com", "editor")
	if code, _ := c.do("DELETE", "/v1/content/"+id, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	got, _ := contentService(t, app).Get(context.Background(), id)
	if got.Status != "trash" {
		t.Fatalf("status %s", got.Status)
	}
}

// Minor: Unpublish needs content.publish in the service too.
func TestUnpublishRequiresPublishPermission(t *testing.T) {
	app := bootApp(t)
	cs := contentService(t, app)
	contrib := mustUser(t, app, "c@example.com", "contributor")
	ctx := auth.WithUser(context.Background(), contrib)
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	cs.Publish(context.Background(), it.ID) // system publishes it
	if err := cs.Unpublish(ctx, it.ID); !errors.Is(err, content.ErrForbidden) {
		t.Fatalf("contributor unpublish: %v", err)
	}
}

// Review fix 4: the parent picker always shows the current parent, however
// many items exist, and saving keeps it.
func TestAdminParentPickerKeepsDistantParent(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	var parent *content.Item
	for i := 0; i < 150; i++ {
		p, err := cs.Create(ctx, content.Input{Type: "page", Title: fmt.Sprintf("Page %03d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 140 {
			parent = p
		}
	}
	child, _ := cs.Create(ctx, content.Input{Type: "page", Title: "Child", ParentID: parent.ID})
	resp, page := s.do("GET", "/admin/content/page/"+child.ID, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `name="parent_id" value="`+parent.ID+`"`) || !strings.Contains(page, "Page 140") {
		t.Fatalf("parent chip missing: %d", resp.StatusCode)
	}
	resp, _ = s.do("POST", "/admin/content/page/"+child.ID, url.Values{"_csrf": {s.csrf(page)}, "title": {"Child 2"}, "intent": {"save"},
		"status": {"draft"}, "parent_id": {"", parent.ID}}, nil)
	if got, _ := cs.Get(ctx, child.ID); resp.StatusCode != http.StatusSeeOther || got.ParentID != parent.ID || got.Title != "Child 2" {
		t.Fatalf("save: %d parent=%q", resp.StatusCode, got.ParentID)
	}
	// Clearing the picker (only the "" marker) removes the parent.
	s.do("POST", "/admin/content/page/"+child.ID, url.Values{"_csrf": {s.csrf(page)}, "title": {"Child 3"}, "intent": {"save"},
		"status": {"draft"}, "parent_id": {""}}, nil)
	if got, _ := cs.Get(ctx, child.ID); got.ParentID != "" {
		t.Fatalf("parent not cleared: %q", got.ParentID)
	}
	_, picks := s.do("GET", "/admin/x/content/pick?type=page&q=Page+149&exclude="+child.ID, nil, map[string]string{"HX-Request": "true"})
	if !strings.Contains(picks, "Page 149") {
		t.Fatalf("search beyond the first 100: %s", picks)
	}
}

// Review fix 6: bodies with markup Quill cannot keep open on the HTML tab.
func TestAdminEditorStartsOnHTMLForUnsupportedMarkup(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	table, _ := cs.Create(ctx, content.Input{Type: "post", Title: "T", Body: "<table><tr><td>1</td></tr></table>"})
	plain, _ := cs.Create(ctx, content.Input{Type: "post", Title: "P", Body: "<p>hello</p>"})
	_, page := s.do("GET", "/admin/content/post/"+table.ID, nil, nil)
	if !strings.Contains(page, `data-editor-start="html"`) || !strings.Contains(page, "data-warn=") {
		t.Fatal("table body should open on the HTML tab")
	}
	_, page = s.do("GET", "/admin/content/post/"+plain.ID, nil, nil)
	if strings.Contains(page, `data-editor-start="html"`) {
		t.Fatal("plain body should open on the visual tab")
	}
}

// Minor: the bulk flash says whether items were trashed or deleted.
func TestAdminBulkFlashDistinguishesDelete(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	a, _ := cs.Create(ctx, content.Input{Type: "post", Title: "A"})
	_, page := s.do("GET", "/admin/content/post", nil, nil)
	token := s.csrf(page)
	resp, _ := s.do("POST", "/admin/content/post/bulk", url.Values{"_csrf": {token}, "action": {"trash"}, "ids": {a.ID}}, nil)
	if !strings.Contains(resp.Header.Get("Set-Cookie"), "bulk_trashed") {
		t.Fatalf("trash flash: %s", resp.Header.Get("Set-Cookie"))
	}
	resp, _ = s.do("POST", "/admin/content/post/bulk", url.Values{"_csrf": {token}, "action": {"delete"}, "ids": {a.ID}}, nil)
	if !strings.Contains(resp.Header.Get("Set-Cookie"), "bulk_deleted") {
		t.Fatalf("delete flash: %s", resp.Header.Get("Set-Cookie"))
	}
	if _, err := cs.Get(ctx, a.ID); !errors.Is(err, content.ErrNotFound) {
		t.Fatal("not deleted")
	}
}

// Un-trashing via Update (trash → another status) restores a deleted item:
// like deleting, it needs content.delete.
func TestUntrashViaUpdateRequiresDeletePermission(t *testing.T) {
	app := bootApp(t)
	cs := contentService(t, app)
	author := mustUser(t, app, "au@example.com", "author") // no content.delete
	editor := mustUser(t, app, "ed@example.com", "editor") // has content.delete
	actx := auth.WithUser(context.Background(), author)
	it, err := cs.Create(actx, content.Input{Type: "post", Title: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Delete(context.Background(), it.ID, false); err != nil { // system trashes it
		t.Fatal(err)
	}
	trashed, _ := cs.Get(context.Background(), it.ID)
	in := content.InputFromItem(trashed)
	in.Status = "draft"
	_, err = cs.Update(actx, it.ID, in)
	var ve *content.ValidationError
	if !errors.As(err, &ve) || ve.Fields["status"] != "content.validation.untrash_forbidden" {
		t.Fatalf("author un-trash: %v", err)
	}
	if got, _ := cs.Get(context.Background(), it.ID); got.Status != "trash" {
		t.Fatalf("rejected un-trash changed the status to %s", got.Status)
	}
	// Keeping it in the trash (e.g. editing the title) stays allowed.
	keep := content.InputFromItem(trashed)
	keep.Title = "Renamed in trash"
	if _, err := cs.Update(actx, it.ID, keep); err != nil {
		t.Fatalf("editing a trashed item without restoring it: %v", err)
	}
	if got, err := cs.Update(auth.WithUser(context.Background(), editor), it.ID, in); err != nil || got.Status != "draft" {
		t.Fatalf("editor un-trash: %v %+v", err, got)
	}
}
