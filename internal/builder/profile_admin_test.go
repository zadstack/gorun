package builder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestProfileSubmitPreservesInvalidFieldsAndShowsNativeMediaPicker(t *testing.T) {
	h, _, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := starterInput(issued.Token, "company")
	form := url.Values{"token": {f.Token}, "starter": {f.Starter}, "title": {f.Title}, "tagline": {f.Tagline}, "locale": {f.Locale}, "accent": {f.Accent}, "background": {f.Background}, "text": {f.Text}, "font": {f.Font}, "width": {f.Width}, "about": {"Keep my project story"}, "description": {"Keep my description"}, "services": {"Consulting"}, "contact_email": {"Name <owner@example.test>"}, "contact_phone": {"+20 100 123 4567"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/site-builder", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.submit(rec, req)
	if rec.Code != 422 {
		t.Fatalf("invalid profile HTTP%d: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Keep my project story", "Keep my description", "Consulting", "Name &lt;owner@example.test&gt;", "starter-logo", "starter-hero-image", "gp-media-picker-body"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("invalid form lost %q", want)
		}
	}
	run, err := h.run(ctx, issued.Token)
	if err != nil || run.Kind != "" || len(run.Pages) != 0 {
		t.Fatal("bad HTTP profile captured")
	}
}
func TestProfileDeletedImageActivationShowsActionableReview(t *testing.T) {
	h, cs, ctx := starterTest(t)
	ms, im := profileMedia(t, h, ctx)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := starterInput(issued.Token, "company")
	f.HeroImage = im.ID
	run, err := h.create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	review, err := json.Marshal(reviewFor(t, h, cs, ctx, run))
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Delete(ctx, im.ID); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/admin/site-builder/{token}/activate", h.publish)
	req := httptest.NewRequest(http.MethodPost, "/admin/site-builder/"+run.Token+"/activate", strings.NewReader(url.Values{"expected": {string(review)}}.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 422 {
		t.Fatalf("deleted image HTTP%d: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`role="alert"`, `href="/admin/site-builder"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("missing retry affordance%s", want)
		}
	}
	for _, id := range run.Pages {
		item, err := cs.Get(ctx, id)
		if err != nil || item.Status != "draft" {
			t.Fatal("partial deleted image activation")
		}
	}
}
