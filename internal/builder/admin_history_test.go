package builder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/content"
)

func withBuilderRoute(ctx context.Context, id, rev string) context.Context {
	route := chi.NewRouteContext()
	route.URLParams.Add("id", id)
	if rev != "" {
		route.URLParams.Add("rev", rev)
	}
	return context.WithValue(ctx, chi.RouteCtxKey, route)
}

func TestAdminEditorListsLayoutHistory(t *testing.T) {
	h, cs, ctx := adminFixture(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "History UI", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := h.svc.Save(ctx, it.ID, heroDoc("First"), content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.Save(ctx, it.ID, heroDoc("Second"), first.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	rec := adminCall(ctx, h.editor, it.ID, "")
	if rec.Code != 200 {
		t.Fatalf("editor HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="builder-history-heading"`) || !strings.Contains(body, "First") {
		t.Fatalf("history panel missing: %s", body)
	}
	if !strings.Contains(body, "/admin/builder/"+it.ID+"/revisions/") || !strings.Contains(body, "/preview") {
		t.Fatal("history preview/restore links missing")
	}
}

func TestAdminRestoreFormAndSubmit(t *testing.T) {
	h, cs, ctx := adminFixture(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Restore UI", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := h.svc.Save(ctx, it.ID, heroDoc("Old"), content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.svc.Save(ctx, it.ID, heroDoc("New"), first.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	revs, err := h.svc.ListRevisions(ctx, it.ID)
	if err != nil || len(revs) == 0 {
		t.Fatalf("revisions: %v", err)
	}
	revID := revs[len(revs)-1].ID

	req := httptest.NewRequest(http.MethodGet, "/admin/builder/"+it.ID+"/revisions/"+revID+"/restore", nil)
	req = req.WithContext(withBuilderRoute(ctx, it.ID, revID))
	rec := httptest.NewRecorder()
	h.restoreForm(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="updated_at"`) {
		t.Fatalf("restore form %d %s", rec.Code, rec.Body.String())
	}

	form := url.Values{"updated_at": {second.UpdatedAt}}
	req = httptest.NewRequest(http.MethodPost, "/admin/builder/"+it.ID+"/revisions/"+revID+"/restore", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(withBuilderRoute(ctx, it.ID, revID))
	rec = httptest.NewRecorder()
	h.restoreSubmit(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/builder/"+it.ID {
		t.Fatalf("restore submit %d %q", rec.Code, rec.Header().Get("Location"))
	}
	draft, err := h.svc.Load(ctx, it.ID)
	if err != nil || draft.Document.Sections[0].Title != "Old" {
		t.Fatalf("draft after restore: %+v %v", draft, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/builder/"+it.ID+"/revisions/"+revID+"/preview", nil)
	req = req.WithContext(withBuilderRoute(ctx, it.ID, revID))
	rec = httptest.NewRecorder()
	h.revisionPreview(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Old") {
		t.Fatalf("revision preview %d %s", rec.Code, rec.Body.String())
	}
}
