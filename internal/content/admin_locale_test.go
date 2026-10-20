package content_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// AR admin on an English-default site, and EN admin on an Arabic-default site,
// must store new content in the admin chrome language unless the picker overrides.
func TestAdminNewContentLocaleFollowsAdminNotSite(t *testing.T) {
	cases := []struct {
		name       string
		adminLoc   string
		siteLoc    string
		title      string
		pick       string // empty = accept default from picker/chrome
		wantLocale string
	}{
		{name: "en-admin-ar-site", adminLoc: "en", siteLoc: "ar", title: "Soak EN Post", wantLocale: "en"},
		{name: "ar-admin-en-site", adminLoc: "ar", siteLoc: "en", title: "مقال اختبار النقع", wantLocale: "ar"},
		{name: "en-admin-picks-ar", adminLoc: "en", siteLoc: "ar", title: "Arabic body from EN chrome", pick: "ar", wantLocale: "ar"},
		{name: "ar-admin-picks-en", adminLoc: "ar", siteLoc: "en", title: "English body from AR chrome", pick: "en", wantLocale: "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := loggedIn(t, "admin")
			ctx := context.Background()
			us, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
			u, err := us.GetByEmail(ctx, "admin@example.com")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := us.Update(ctx, u.ID, auth.UserPatch{Locale: &tc.adminLoc}); err != nil {
				t.Fatal(err)
			}
			if err := s.app.Deps.Settings.Set(ctx, "site.locale", tc.siteLoc); err != nil {
				t.Fatal(err)
			}

			resp, page := s.do("GET", "/admin/content/post/new", nil, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("new editor: %d", resp.StatusCode)
			}
			if !strings.Contains(page, `id="editor-locale"`) || !strings.Contains(page, `data-content-locale`) || !strings.Contains(page, `name="locale"`) {
				t.Fatalf("new editor must show content language picker:\n%s", page)
			}
			wantSelected := `value="` + tc.adminLoc + `" selected`
			if !strings.Contains(page, wantSelected) {
				t.Fatalf("picker should default to admin locale %q:\n%s", tc.adminLoc, page)
			}

			form := url.Values{"_csrf": {s.csrf(page)}, "title": {tc.title}, "status": {"draft"}, "intent": {"save"}}
			if tc.pick != "" {
				form.Set("locale", tc.pick)
			} else {
				form.Set("locale", tc.adminLoc) // same as the preselected option
			}
			resp, _ = s.do("POST", "/admin/content/post", form, nil)
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("create: %d", resp.StatusCode)
			}
			loc := resp.Header.Get("Location")
			id := loc[strings.LastIndex(loc, "/")+1:]
			it, err := contentService(t, s.app).Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if it.Locale != tc.wantLocale {
				t.Fatalf("stored locale=%q want %q (admin=%s site=%s)", it.Locale, tc.wantLocale, tc.adminLoc, tc.siteLoc)
			}
		})
	}
}
