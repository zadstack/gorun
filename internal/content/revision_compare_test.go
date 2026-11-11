package content_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func TestRevisionCompareShowsFieldAndBodyChanges(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "First title", Body: "<p>Shared line</p><p>Old words here</p>", SEO: map[string]any{"title": "Old SEO"}})
	if err != nil {
		t.Fatal(err)
	}
	in := content.InputFromItem(it)
	in.Title, in.Body, in.SEO = "Second title", "<p>Shared line</p><p>New words here</p><p>Added paragraph</p>", map[string]any{"title": "New SEO"}
	if _, err := cs.Update(ctx, it.ID, in); err != nil {
		t.Fatal(err)
	}
	revs, err := cs.Revisions(ctx, it.ID)
	if err != nil || len(revs) < 2 {
		t.Fatalf("revisions %d %v", len(revs), err)
	}
	oldest := revs[len(revs)-1]
	path := "/admin/content/post/" + it.ID
	_, editor := s.do("GET", path, nil, nil)
	if !strings.Contains(editor, `href="`+path+`/revisions/`+oldest.ID+`/compare"`) {
		t.Fatal("editor misses the compare link")
	}
	resp, page := s.do("GET", path+"/revisions/"+oldest.ID+"/compare", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compare %d", resp.StatusCode)
	}
	for _, want := range []string{`data-diff-field="title"`, `data-diff-field="seo.title"`, `<del><span class="sr-only">`, `>First</del>`, `Second</ins>`,
		`data-op="change"`, `data-op="insert"`, `Added paragraph</ins>`, `data-op="equal"`, `action="` + path + `/revisions/` + oldest.ID + `/restore"`} {
		if !strings.Contains(page, want) {
			t.Errorf("compare page misses %s", want)
		}
	}
	if strings.Contains(page, `data-diff-field="slug"`) {
		t.Error("unchanged slug listed")
	}
	// Body text is escaped, never rendered as HTML.
	if regexp.MustCompile(`<p>Shared line</p>`).MatchString(page) {
		t.Error("body HTML rendered raw")
	}

	// Against the previous revision; the oldest has none.
	if resp, _ = s.do("GET", path+"/revisions/"+revs[0].ID+"/compare?against=previous", nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("against previous %d", resp.StatusCode)
	}
	if resp, _ = s.do("GET", path+"/revisions/"+oldest.ID+"/compare?against=previous", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("oldest against previous %d", resp.StatusCode)
	}
	if resp, _ = s.do("GET", path+"/revisions/nope/compare", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown revision %d", resp.StatusCode)
	}
}

func TestRevisionCompareRespectsOwnership(t *testing.T) {
	s := loggedIn(t, "author")
	cs := contentService(t, s.app)
	other := mustUser(t, s.app, "other@example.com", "editor")
	it, err := cs.Create(context.Background(), content.Input{Type: "post", Title: "Private draft", AuthorID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	revs, _ := cs.Revisions(context.Background(), it.ID)
	resp, _ := s.do("GET", "/admin/content/post/"+it.ID+"/revisions/"+revs[0].ID+"/compare", nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign compare %d", resp.StatusCode)
	}
}
