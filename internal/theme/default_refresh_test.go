package theme

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestDefaultHomeKeepsIdentityAndDoesNotInventArticles(t *testing.T) {
	e := newEnv(t, "../../themes")
	e.setting(SettingTitle, "My independent journal")
	e.setting(SettingTagline, "My own description, unchanged.")
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	mustContain(t, body, "My independent journal", "My own description, unchanged.", "dt-home-empty")
	if strings.Contains(body, `id="dt-featured-title"`) || strings.Contains(body, `class="dt-home-guide-list"`) {
		t.Fatal("empty site must not invent featured content or pages")
	}
	post := e.fake.Content.Add(content.Item{Type: "post", Slug: "my-article", Title: "My actual article", Status: "published", Body: "<p>Author-owned text.</p>"})
	rec, err = e.render(kernel.KindRequest{Kind: KindHome}, &Context{Items: []content.Item{post}, Pagination: NewPagination(1, 1, 1)})
	if err != nil {
		t.Fatal(err)
	}
	body = rec.Body.String()
	mustContain(t, body, `id="dt-featured-title"`, "My actual article", "Author-owned text.")
	if n := strings.Count(body, "My actual article"); n != 1 {
		t.Fatalf("single featured article repeated in reading grid: %d", n)
	}
	if strings.Contains(body, "dt-home-empty-number") {
		t.Fatal("published article rendered as empty")
	}
}
