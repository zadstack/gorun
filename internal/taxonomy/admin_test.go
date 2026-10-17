package taxonomy

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

type site struct {
	t   *testing.T
	srv *httptest.Server
	app *kernel.App
	c   *http.Client
}

var csrfRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func loggedIn(t *testing.T, role string) *site {
	t.Helper()
	app := bootApp(t)
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	s := &site{t: t, srv: srv, app: app, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	us, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := us.Create(context.Background(), role+"@example.com", "U", "correct horse battery", role); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/login", nil, nil)
	resp, _ := s.do("POST", "/admin/login", url.Values{"_csrf": {s.csrf(page)}, "email": {role + "@example.com"}, "password": {"correct horse battery"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	return s
}

func (s *site) csrf(page string) string {
	s.t.Helper()
	m := csrfRe.FindStringSubmatch(page)
	if m == nil {
		s.t.Fatalf("no csrf in:\n%s", page)
	}
	return m[1]
}

func (s *site) do(method, path string, form url.Values, hdr map[string]string) (*http.Response, string) {
	s.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, s.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestAdminTaxonomyScreensAndPickers(t *testing.T) {
	s := loggedIn(t, "editor")
	_, ts := services(t, s.app)
	ctx := context.Background()

	resp, page := s.do("GET", "/admin/taxonomies", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "/admin/taxonomies/category") {
		t.Fatalf("taxonomies list: %d", resp.StatusCode)
	}
	token := s.csrf(page)
	resp, _ = s.do("POST", "/admin/taxonomies", url.Values{"_csrf": {token}, "slug": {"genre"}, "name_en": {"Genres"}, "name_ar": {"الأنواع"},
		"hierarchical": {"1"}, "public": {"1"}, "types": {"post"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create taxonomy: %d", resp.StatusCode)
	}
	if g, err := ts.Get(ctx, "genre"); err != nil || !g.Hierarchical || len(g.Types) != 1 {
		t.Fatalf("genre: %+v %v", g, err)
	}
	if resp, page = s.do("POST", "/admin/taxonomies", url.Values{"_csrf": {token}, "slug": {"Bad"}}, nil); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "field-error") {
		t.Fatalf("invalid taxonomy: %d", resp.StatusCode)
	}

	resp, _ = s.do("POST", "/admin/taxonomies/genre/terms", url.Values{"_csrf": {token}, "name": {"روايات"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create term: %d", resp.StatusCode)
	}
	parent, err := ts.GetTermBySlug(ctx, "genre", "روايات")
	if err != nil {
		t.Fatal(err)
	}
	s.do("POST", "/admin/taxonomies/genre/terms", url.Values{"_csrf": {token}, "name": {"Sci-fi"}, "parent_id": {parent.ID}}, nil)
	resp, page = s.do("GET", "/admin/taxonomies/genre", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "روايات") || !strings.Contains(page, "ps-4") {
		t.Fatalf("terms tree: %d", resp.StatusCode)
	}
	if resp, _ = s.do("POST", "/admin/taxonomies/genre/terms", url.Values{"_csrf": {token}, "name": {""}}, nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("empty term name: %d", resp.StatusCode)
	}

	// Pickers for the content editor.
	hx := map[string]string{"HX-Request": "true"}
	resp, page = s.do("GET", "/admin/x/taxonomy/genre/picker?selected=sci-fi", nil, hx)
	if resp.StatusCode != 200 || !strings.Contains(page, `name="terms_present[genre]"`) || !strings.Contains(page, `value="sci-fi" checked`) {
		t.Fatalf("hierarchical picker: %d %s", resp.StatusCode, page)
	}
	ts.SaveTerm(ctx, Term{Taxonomy: "tag", Name: "golang"})
	resp, page = s.do("GET", "/admin/x/taxonomy/tag/picker?selected=golang", nil, hx)
	if resp.StatusCode != 200 || !strings.Contains(page, "x-data") || !strings.Contains(page, "golang") {
		t.Fatalf("tag picker: %d %s", resp.StatusCode, page)
	}
	resp, page = s.do("GET", "/admin/x/taxonomy/tag/terms?q=gol", nil, hx)
	if resp.StatusCode != 200 || !strings.Contains(page, `data-slug="golang"`) {
		t.Fatalf("suggestions: %d %s", resp.StatusCode, page)
	}

	// The content editor saves picked terms; an unloaded picker keeps them.
	cs, _ := services(t, s.app)
	_, page = s.do("GET", "/admin/content/post/new", nil, nil)
	if !strings.Contains(page, "/admin/x/taxonomy/genre/picker") {
		t.Fatal("editor does not load the genre picker")
	}
	resp, _ = s.do("POST", "/admin/content/post", url.Values{"_csrf": {token}, "title": {"Book"}, "intent": {"publish"},
		"terms_present[genre]": {"1"}, "terms[genre]": {"sci-fi"}, "terms_present[tag]": {"1"}, "terms[tag]": {"golang", "New One"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("editor save: %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	id := loc[strings.LastIndex(loc, "/")+1:]
	it, _ := cs.Get(ctx, id)
	if strings.Join(it.Terms["genre"], ",") != "sci-fi" || !slices.Equal(slices.Sorted(slices.Values(it.Terms["tag"])), []string{"golang", "new-one"}) || it.Status != "published" {
		t.Fatalf("terms after save: %v (%s)", it.Terms, it.Status)
	}
	s.do("POST", loc, url.Values{"_csrf": {token}, "title": {"Book 2"}, "intent": {"save"}, "status": {"published"}}, nil)
	if it, _ = cs.Get(ctx, id); it.Title != "Book 2" || len(it.Terms["tag"]) != 2 {
		t.Fatalf("terms must survive a save without pickers: %+v", it.Terms)
	}
	parentTerm, _ := ts.GetTerm(ctx, parent.ID)
	if page, _ := cs.Query(ctx, content.Query{Type: "post", Taxonomy: map[string][]string{"genre": {parentTerm.Slug}}}); page.Total != 1 {
		t.Fatalf("parent term includes children's content: %d", page.Total)
	}

	resp, _ = s.do("POST", "/admin/taxonomies/genre/terms/"+parent.ID+"/delete", url.Values{"_csrf": {token}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete term: %d", resp.StatusCode)
	}
	if resp, _ = s.do("POST", "/admin/taxonomies/category/delete", url.Values{"_csrf": {token}}, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("system delete redirects with an error flash: %d", resp.StatusCode)
	}
	if _, err := ts.Get(ctx, "category"); err != nil {
		t.Fatal("system taxonomy deleted")
	}
}
