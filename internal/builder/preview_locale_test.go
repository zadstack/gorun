package builder

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
)

func TestBuilderPreviewUsesPageLanguageInsteadOfAdminLanguage(t *testing.T) {
	for _, test := range []struct{ page, admin, dir string }{{"en", "ar", "ltr"}, {"ar", "en", "rtl"}} {
		t.Run(test.page, func(t *testing.T) {
			s, cs, ctx := builderTest(t)
			item, err := cs.Create(ctx, content.Input{Type: "page", Title: "Localized preview", Locale: test.page, Status: "draft"})
			if err != nil {
				t.Fatal(err)
			}
			route := chi.NewRouteContext()
			route.URLParams.Add("id", item.ID)
			ctx = i18n.WithLocale(ctx, test.admin)
			request := httptest.NewRequest("POST", "/admin/builder/"+item.ID+"/preview", strings.NewReader(`{"document":{"version":1,"sections":[]}}`)).WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
			recorder := httptest.NewRecorder()
			handler := &adminHandlers{svc: s, d: s.d}
			handler.preview(recorder, request)
			if recorder.Code != 200 {
				t.Fatalf("preview status %d: %s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `<html lang="`+test.page+`" dir="`+test.dir+`"`) {
				t.Fatalf("admin language leaked into page preview: %s", recorder.Body.String()[:100])
			}
		})
	}
}
