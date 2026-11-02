package saas

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
)

func TestSaaSPublicPageUsesSharedIdentityAndPreservesAnonymousBoundary(t *testing.T) {
	app := apitest.Boot(t, New())
	owner := apitest.New(t, app, apitest.Options{Role: "customer", Scopes: []string{"saas.read"}})
	identity := brand.Identity{Accent: "#d7af44", Background: "#142d36", Text: "#f7f5ed", Font: "system", Width: "standard"}
	if err := app.Deps.Settings.Set(context.Background(), "builder.identity", identity); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/saas", nil).WithContext(auth.WithUser(context.Background(), owner.User))
	app.Handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "--standalone-accent:#d7af44") || !strings.Contains(w.Body.String(), "لا توجد تطبيقات مرتبطة بحسابك") {
		t.Fatalf("standalone response did not retain identity and empty state: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "<table>") || !strings.Contains(w.Body.String(), "لا توجد اشتراكات لهذا الحساب.") || !strings.Contains(w.Body.String(), "لم يسجّل استخدام بعد.") {
		t.Fatal("empty states must remain readable outside horizontally scrolling tables")
	}
	w = httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/saas", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "--standalone-accent:#d7af44") {
		t.Fatal("presentation must not change the unavailable customer sign-in boundary")
	}
}
