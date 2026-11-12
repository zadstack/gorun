package taxonomy

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAdminTermTranslations(t *testing.T) {
	s := loggedIn(t, "editor")
	_, ts := services(t, s.app)
	ctx := context.Background()
	news := mustTerm(t, ts, Term{Taxonomy: "category", Name: "News", Locale: "en"})
	other := mustTerm(t, ts, Term{Taxonomy: "category", Name: "سياسة", Locale: "ar"})
	base := "/admin/taxonomies/category"

	resp, page := s.do("GET", base+"?edit="+news.ID, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit page: %d", resp.StatusCode)
	}
	for _, want := range []string{`name="locale"`, `data-term-translations`, base + "/terms/" + news.ID + "/translations", `value="` + other.ID + `"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("edit page misses %q", want)
		}
	}
	csrf := s.csrf(page)

	// Add an Arabic translation from the panel.
	resp, _ = s.do("POST", base+"/terms/"+news.ID+"/translations", url.Values{"_csrf": {csrf}, "locale": {"ar"}, "name": {"أخبار"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("translate: %d", resp.StatusCode)
	}
	members, _ := ts.TermTranslations(ctx, news.ID)
	if len(members) != 2 || members[0].Name != "أخبار" {
		t.Fatalf("members after translate: %+v", members)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "edit="+members[0].ID) {
		t.Fatalf("redirect opens the new translation: %q", loc)
	}
	// A taken locale comes back as an error flash, nothing created.
	resp, _ = s.do("POST", base+"/terms/"+news.ID+"/translations", url.Values{"_csrf": {csrf}, "locale": {"ar"}, "name": {"ثاني"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("taken locale: %d", resp.StatusCode)
	}
	if m, _ := ts.TermTranslations(ctx, news.ID); len(m) != 2 {
		t.Fatalf("taken locale created a term: %+v", m)
	}
	// Unlink, then link the other Arabic term instead.
	if resp, _ = s.do("POST", base+"/terms/"+members[0].ID+"/translations/unlink", url.Values{"_csrf": {csrf}}, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("unlink: %d", resp.StatusCode)
	}
	if resp, _ = s.do("POST", base+"/terms/"+news.ID+"/translations/link", url.Values{"_csrf": {csrf}, "term_id": {other.ID}}, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("link: %d", resp.StatusCode)
	}
	if m, _ := ts.TermTranslations(ctx, other.ID); len(m) != 2 {
		t.Fatalf("linked: %+v", m)
	}
	// The term form saves a chosen language.
	resp, _ = s.do("POST", base+"/terms", url.Values{"_csrf": {csrf}, "name": {"Sport"}, "locale": {"en"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save with locale: %d", resp.StatusCode)
	}
	if sp, err := ts.GetTermBySlug(ctx, "category", "sport"); err != nil || sp.Locale != "en" {
		t.Fatalf("saved locale: %+v %v", sp, err)
	}
	// The list shows each term's language.
	_, page = s.do("GET", base, nil, nil)
	if !strings.Contains(page, `data-term-locale="en"`) {
		t.Fatal("term list has no language column")
	}
}

func TestAdminTermTranslationsNeedTaxonomyManage(t *testing.T) {
	s := loggedIn(t, "author")
	_, ts := services(t, s.app)
	news := mustTerm(t, ts, Term{Taxonomy: "category", Name: "News", Locale: "en"})
	_, page := s.do("GET", "/admin/content/post", nil, nil)
	resp, _ := s.do("POST", "/admin/taxonomies/category/terms/"+news.ID+"/translations", url.Values{"_csrf": {s.csrf(page)}, "locale": {"ar"}, "name": {"x"}}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("author translated a term: %d", resp.StatusCode)
	}
}
