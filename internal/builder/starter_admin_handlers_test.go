package builder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

func starterFormValues(f starterForm) url.Values {
	return url.Values{"token": {f.Token}, "starter": {f.Starter}, "title": {f.Title}, "tagline": {f.Tagline}, "locale": {f.Locale}, "accent": {f.Accent}, "background": {f.Background}, "text": {f.Text}, "font": {f.Font}, "width": {f.Width}, "contact_email": {f.ContactEmail}}
}

func starterPost(ctx context.Context, handler http.HandlerFunc, values url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/admin/site-builder", strings.NewReader(values.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func starterActivate(ctx context.Context, h *starterHandler, token, expected string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Post("/admin/site-builder/{token}/activate", h.publish)
	req := httptest.NewRequest(http.MethodPost, "/admin/site-builder/"+token+"/activate", strings.NewReader(url.Values{"expected": {expected}}.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func editorCtx() context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: "editor", Role: "editor", Status: auth.StatusActive})
}

func TestStarterHomeIssuesRunAndGuardsReviewTokens(t *testing.T) {
	h, _, ctx := starterTest(t)
	rec := httptest.NewRecorder()
	h.home(rec, httptest.NewRequest(http.MethodGet, "/admin/site-builder", nil).WithContext(ctx))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="token"`) {
		t.Fatalf("home HTTP %d", rec.Code)
	}
	var count int
	if err := h.d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key LIKE 'builder.starter.%'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("home did not issue exactly one run: %d %v", count, err)
	}
	rec = httptest.NewRecorder()
	h.home(rec, httptest.NewRequest(http.MethodGet, "/admin/site-builder", nil).WithContext(editorCtx()))
	if rec.Code != 403 {
		t.Fatalf("non-admin home HTTP %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.home(rec, httptest.NewRequest(http.MethodGet, "/admin/site-builder?run=short", nil).WithContext(ctx))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("bad run token HTTP %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStarterSubmitValidatesPreparesAndReviews(t *testing.T) {
	h, cs, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/site-builder", strings.NewReader("%zz")).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.submit(rec, req)
	if rec.Code != 400 {
		t.Fatalf("malformed form HTTP %d", rec.Code)
	}
	bad := starterInput(issued.Token, "company")
	bad.Text = bad.Background
	rec = starterPost(ctx, h.submit, starterFormValues(bad))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `role="alert"`) {
		t.Fatalf("low contrast HTTP %d", rec.Code)
	}
	rec = starterPost(editorCtx(), h.submit, starterFormValues(starterInput(issued.Token, "company")))
	if rec.Code != 403 {
		t.Fatalf("non-admin submit HTTP %d", rec.Code)
	}
	rec = starterPost(ctx, h.submit, starterFormValues(starterInput(issued.Token, "company")))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/site-builder?run="+issued.Token {
		t.Fatalf("submit HTTP %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	run, err := h.run(ctx, issued.Token)
	if err != nil || run.Kind != "company" || len(run.Pages) == 0 {
		t.Fatalf("submit did not prepare drafts: %+v %v", run, err)
	}
	rec = httptest.NewRecorder()
	h.home(rec, httptest.NewRequest(http.MethodGet, "/admin/site-builder?run="+issued.Token, nil).WithContext(ctx))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="expected"`) {
		t.Fatalf("review HTTP %d", rec.Code)
	}
	for _, id := range run.Pages {
		if !strings.Contains(rec.Body.String(), "/admin/builder/"+id+"#builder-preview") {
			t.Fatalf("review missing preview link for %s", id)
		}
		item, err := cs.Get(ctx, id)
		if err != nil || item.Status != "draft" {
			t.Fatal("review published a page")
		}
	}
}

func TestStarterSubmitProfileErrorIsLocalized(t *testing.T) {
	h, _, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := starterInput(issued.Token, "company")
	f.ContactEmail = "not an email"
	rec := starterPost(i18n.WithLocale(ctx, "en"), h.submit, starterFormValues(f))
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter a valid email address without a display name.") {
		t.Fatalf("profile error HTTP %d", rec.Code)
	}
}

func TestStarterSubmitInterruptedPreparationOffersResume(t *testing.T) {
	for _, locale := range []string{"en", "ar"} {
		t.Run(locale, func(t *testing.T) {
			h, _, ctx := starterTest(t)
			issued, err := h.issue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			h.d.Services.Set(kernel.ServiceContent, nil)
			rec := starterPost(i18n.WithLocale(ctx, locale), h.submit, starterFormValues(starterInput(issued.Token, "company")))
			want := "Retry this form to resume"
			if locale == "ar" {
				want = "أعد إرسال هذا النموذج"
			}
			if rec.Code != 500 || !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("interrupted preparation HTTP %d", rec.Code)
			}
		})
	}
}

func TestStarterPublishRejectsBadReviewAndActivates(t *testing.T) {
	h, cs, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.create(ctx, starterInput(issued.Token, "company"))
	if err != nil {
		t.Fatal(err)
	}
	if rec := starterActivate(ctx, h, run.Token, "not-json"); rec.Code != 400 {
		t.Fatalf("invalid review HTTP %d", rec.Code)
	}
	review := reviewFor(t, h, cs, ctx, run)
	stale := append([]starterExpectation(nil), review...)
	stale[0].UpdatedAt = "stale"
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if rec := starterActivate(ctx, h, run.Token, string(raw)); rec.Code != 409 {
		t.Fatalf("stale review HTTP %d: %s", rec.Code, rec.Body.String())
	}
	raw, err = json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	if rec := starterActivate(editorCtx(), h, run.Token, string(raw)); rec.Code != 403 {
		t.Fatalf("non-admin activation HTTP %d", rec.Code)
	}
	if rec := starterActivate(ctx, h, run.Token, `[]`); rec.Code != 500 {
		t.Fatalf("incomplete review HTTP %d", rec.Code)
	}
	rec := starterActivate(ctx, h, run.Token, string(raw))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/site-builder?run="+run.Token {
		t.Fatalf("activation HTTP %d: %s", rec.Code, rec.Body.String())
	}
	for _, id := range run.Pages {
		item, err := cs.Get(ctx, id)
		if err != nil || item.Status != "published" {
			t.Fatalf("page %s not published: %v", id, err)
		}
	}
}

func TestStarterReviewWithoutContentServiceFails(t *testing.T) {
	h, _, ctx := starterTest(t)
	h.d.Services.Set(kernel.ServiceContent, nil)
	rec := httptest.NewRecorder()
	h.reviewStatus(rec, httptest.NewRequest(http.MethodGet, "/admin/site-builder", nil).WithContext(ctx), &starterRun{Kind: "company", Pages: map[string]string{}}, 200, "")
	if rec.Code != 500 {
		t.Fatalf("review without content HTTP %d", rec.Code)
	}
}

func TestStarterReviewMissingPageFailsClosed(t *testing.T) {
	h, _, ctx := starterTest(t)
	rec := httptest.NewRecorder()
	h.reviewStatus(rec, httptest.NewRequest(http.MethodGet, "/admin/site-builder", nil).WithContext(ctx), &starterRun{Kind: "company", Title: "T", Locale: "en", Pages: map[string]string{"home": "vanished"}}, 200, "")
	if rec.Code != 500 {
		t.Fatalf("missing page review HTTP %d", rec.Code)
	}
	if _, ok := kernel.Get[content.Service](h.d, kernel.ServiceContent); !ok {
		t.Fatal("content service unexpectedly removed")
	}
}
