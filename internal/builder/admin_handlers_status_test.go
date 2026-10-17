package builder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

func adminFixture(t *testing.T) (*adminHandlers, content.Service, context.Context) {
	t.Helper()
	s, cs, ctx := builderTest(t)
	tpl, ok := kernel.Get[*httpx.Templates](s.d, kernel.ServiceAdminTemplates)
	if !ok {
		t.Fatal("admin templates unavailable")
	}
	return &adminHandlers{svc: s, tpl: tpl, d: s.d}, cs, ctx
}

func adminCall(ctx context.Context, handler http.HandlerFunc, id, body string) *httptest.ResponseRecorder {
	route := chi.NewRouteContext()
	route.URLParams.Add("id", id)
	req := httptest.NewRequest(http.MethodPost, "/admin/builder/"+id, strings.NewReader(body)).WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func contributorCtx() context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: "contributor", Role: "contributor", Status: auth.StatusActive})
}

func heroDocJSON(t *testing.T, title string) string {
	t.Helper()
	sec := DefaultSection("hero", "en")
	sec.Title = title
	raw, err := json.Marshal(Document{Version: 1, Sections: []Section{sec}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func decodeDraft(t *testing.T, rec *httptest.ResponseRecorder) Draft {
	t.Helper()
	var d Draft
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode draft: %v: %s", err, rec.Body.String())
	}
	return d
}

func TestAdminEditorRendersAndMapsLoadFailuresToStatus(t *testing.T) {
	h, cs, ctx := adminFixture(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Legacy", Body: "<p>Legacy body</p>", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	rec := adminCall(ctx, h.editor, it.ID, "")
	if rec.Code != 200 {
		t.Fatalf("editor HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-id="`+it.ID+`"`) {
		t.Fatal("editor missing page id")
	}
	if !strings.Contains(body, `class="flex gap-2 items-center text-red"`) {
		t.Fatal("existing legacy body warning missing")
	}
	if rec := adminCall(ctx, h.editor, "missing-page", ""); rec.Code != 404 {
		t.Fatalf("missing page HTTP %d", rec.Code)
	}
	if rec := adminCall(contributorCtx(), h.editor, it.ID, ""); rec.Code != 403 {
		t.Fatalf("foreign page HTTP %d", rec.Code)
	}
	h.d.Services.Set(kernel.ServiceContent, nil)
	if rec := adminCall(ctx, h.editor, it.ID, ""); rec.Code != 422 {
		t.Fatalf("content service unavailable HTTP %d", rec.Code)
	}
}

func TestAdminSavePublishResetStatusCodes(t *testing.T) {
	h, cs, ctx := adminFixture(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Page", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	for name, handler := range map[string]http.HandlerFunc{"save": h.save, "publish": h.publish, "reset": h.reset} {
		if rec := adminCall(ctx, handler, it.ID, `{"unknown":1}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "builder.error") {
			t.Fatalf("%s bad JSON HTTP %d %s", name, rec.Code, rec.Body.String())
		}
	}
	saveBody := `{"document":` + heroDocJSON(t, "First") + `,"base_updated_at":"stale"}`
	if rec := adminCall(ctx, h.save, it.ID, saveBody); rec.Code != 409 {
		t.Fatalf("stale save HTTP %d", rec.Code)
	}
	saveBody = `{"document":` + heroDocJSON(t, "First") + `,"base_updated_at":"` + content.EditToken(it) + `"}`
	rec := adminCall(ctx, h.save, it.ID, saveBody)
	if rec.Code != 200 {
		t.Fatalf("save HTTP %d %s", rec.Code, rec.Body.String())
	}
	saved := decodeDraft(t, rec)
	if len(saved.Document.Sections) != 1 || saved.Document.Sections[0].Title != "First" {
		t.Fatalf("save did not store document: %+v", saved)
	}
	if rec := adminCall(ctx, h.publish, it.ID, `{"updated_at":"stale"}`); rec.Code != 409 {
		t.Fatalf("stale publish HTTP %d", rec.Code)
	}
	if rec := adminCall(contributorCtx(), h.publish, it.ID, `{"updated_at":"`+saved.UpdatedAt+`"}`); rec.Code != 403 {
		t.Fatalf("foreign publish HTTP %d", rec.Code)
	}
	rec = adminCall(ctx, h.publish, it.ID, `{"updated_at":"`+saved.UpdatedAt+`"}`)
	if rec.Code != 200 {
		t.Fatalf("publish HTTP %d %s", rec.Code, rec.Body.String())
	}
	published := decodeDraft(t, rec)
	if !published.HasPublished {
		t.Fatal("publish response does not report published state")
	}
	live, err := cs.Get(ctx, it.ID)
	if err != nil || !strings.Contains(live.Body, "First") {
		t.Fatalf("publish did not update page body: %v", err)
	}
	saveBody = `{"document":` + heroDocJSON(t, "Private") + `,"base_updated_at":"` + published.UpdatedAt + `"}`
	rec = adminCall(ctx, h.save, it.ID, saveBody)
	if rec.Code != 200 {
		t.Fatalf("second save HTTP %d", rec.Code)
	}
	second := decodeDraft(t, rec)
	if rec := adminCall(ctx, h.reset, it.ID, `{"updated_at":"stale"}`); rec.Code != 409 {
		t.Fatalf("stale reset HTTP %d", rec.Code)
	}
	rec = adminCall(ctx, h.reset, it.ID, `{"updated_at":"`+second.UpdatedAt+`"}`)
	if rec.Code != 200 {
		t.Fatalf("reset HTTP %d %s", rec.Code, rec.Body.String())
	}
	restored := decodeDraft(t, rec)
	if restored.Document.Sections[0].Title != "First" {
		t.Fatalf("reset did not restore published document: %+v", restored)
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "flash") {
		t.Fatalf("reset did not set flash: %q", rec.Header().Get("Set-Cookie"))
	}
}

func TestAdminPreviewRejectsInvalidRequests(t *testing.T) {
	h, cs, ctx := adminFixture(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Preview", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := adminCall(ctx, h.preview, "missing-page", `{"document":{"version":1,"sections":[]}}`); rec.Code != 404 {
		t.Fatalf("missing preview HTTP %d", rec.Code)
	}
	if rec := adminCall(ctx, h.preview, it.ID, `not json`); rec.Code != 400 {
		t.Fatalf("bad preview JSON HTTP %d", rec.Code)
	}
	if rec := adminCall(ctx, h.preview, it.ID, `{"document":{"version":2,"sections":[]}}`); rec.Code != 422 {
		t.Fatalf("invalid preview document HTTP %d", rec.Code)
	}
	if err := h.d.Settings.Set(ctx, "site.locale", "fr"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.DB.ExecContext(ctx, `UPDATE content SET locale='' WHERE id=?`, it.ID); err != nil {
		t.Fatal(err)
	}
	rec := adminCall(ctx, h.preview, it.ID, `{"document":`+heroDocJSON(t, "Fallback locale")+`}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `<html lang="`+i18n.DefaultLocale+`" dir="rtl"`) {
		t.Fatalf("unsupported locale fallback HTTP %d: %.120s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("preview missing isolation headers")
	}
}

type fakePreviewRenderer struct {
	themedErr, bodyErr error
	themed             theme.BuilderPreviewOptions
}

func (f *fakePreviewRenderer) RenderBuilderThemePreview(_ context.Context, it *content.Item, o theme.BuilderPreviewOptions) (string, error) {
	f.themed = o
	if f.themedErr != nil {
		return "", f.themedErr
	}
	return "<main>themed:" + it.Body + "</main>", nil
}

func (f *fakePreviewRenderer) RenderBuilderBody(_ context.Context, it *content.Item) (string, error) {
	if f.bodyErr != nil {
		return "", f.bodyErr
	}
	return "<div>dynamic:" + it.Body + "</div>", nil
}

func TestAdminPreviewUsesDynamicRendererAndReportsFailures(t *testing.T) {
	h, cs, ctx := adminFixture(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Dynamic", Status: "draft", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	renderer := &fakePreviewRenderer{}
	h.d.Services.Set(kernel.ServiceTheme, renderer)
	rec := adminCall(ctx, h.preview, it.ID, `{"document":`+heroDocJSON(t, "Dynamic hero")+`}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<div>dynamic:") || !strings.Contains(rec.Body.String(), "Dynamic hero") {
		t.Fatalf("dynamic preview HTTP %d: %s", rec.Code, rec.Body.String())
	}
	renderer.bodyErr = errors.New("template exploded")
	rec = adminCall(ctx, h.preview, it.ID, `{"document":`+heroDocJSON(t, "Dynamic hero")+`}`)
	if rec.Code != 422 || strings.Contains(rec.Body.String(), "exploded") {
		t.Fatalf("dynamic failure HTTP %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminPreviewRendersPendingStarterWithProposedTheme(t *testing.T) {
	sh, _, ctx := starterTest(t)
	tpl, _ := kernel.Get[*httpx.Templates](sh.d, kernel.ServiceAdminTemplates)
	h := &adminHandlers{svc: sh.svc, tpl: tpl, d: sh.d}
	issued, err := sh.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := sh.create(ctx, starterInput(issued.Token, "portfolio"))
	if err != nil {
		t.Fatal(err)
	}
	renderer := &fakePreviewRenderer{}
	h.d.Services.Set(kernel.ServiceTheme, renderer)
	home := run.Pages["home"]
	rec := adminCall(ctx, h.preview, home, `{"document":`+heroDocJSON(t, "Themed hero")+`}`)
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "<main>themed:") || !strings.Contains(rec.Body.String(), "Themed hero") {
		t.Fatalf("themed preview HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if renderer.themed.Theme != "portfolio" || renderer.themed.Title != run.Title || renderer.themed.Menu == nil {
		t.Fatalf("themed preview options wrong: %+v", renderer.themed)
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("themed preview missing isolation headers")
	}
	renderer.themedErr = errors.New("theme secret failure")
	rec = adminCall(ctx, h.preview, home, `{"document":`+heroDocJSON(t, "Themed hero")+`}`)
	if rec.Code != 422 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("themed failure HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := h.d.DB.ExecContext(ctx, `UPDATE settings SET value_json=json_set(value_json,'$.identity','not-an-object') WHERE key=?`, "builder.starter."+run.Token); err != nil {
		t.Fatal(err)
	}
	if rec := adminCall(ctx, h.preview, home, `{"document":`+heroDocJSON(t, "Themed hero")+`}`); rec.Code != 422 {
		t.Fatalf("corrupt starter run preview HTTP %d", rec.Code)
	}
}
