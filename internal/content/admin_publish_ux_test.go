package content_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

// Publishing from the editor needs a title; a draft may stay untitled.
// The rejected form keeps what was typed and names the title field.
func TestAdminPublishNeedsTitle(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	_, page := s.do("GET", "/admin/content/page/new", nil, nil)
	tok := s.csrf(page)

	resp, body := s.do("POST", "/admin/content/page", url.Values{"_csrf": {tok}, "title": {"  "}, "body": {"<p>Kept body</p>"}, "intent": {"publish"}}, nil)
	// The test admin renders in Arabic (the default dashboard language).
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "أضف عنوانًا قبل النشر") || !strings.Contains(body, "Kept body") {
		t.Fatalf("untitled publish: %d", resp.StatusCode)
	}
	if got, err := cs.Query(ctx, content.Query{Type: "page"}); err != nil || got.Total != 0 {
		t.Fatalf("untitled publish created %d items: %v", got.Total, err)
	}

	resp, _ = s.do("POST", "/admin/content/page", url.Values{"_csrf": {tok}, "title": {""}, "status": {"draft"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("untitled draft: %d", resp.StatusCode)
	}

	live, err := cs.Create(ctx, content.Input{Type: "page", Title: "About", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	_, page = s.do("GET", "/admin/content/page/"+live.ID, nil, nil)
	form := url.Values{"_csrf": {s.csrf(page)}, "title": {""}, "status": {"published"}}
	if m := regexpValue(page, "edit_token"); m != "" {
		form.Set("edit_token", m)
	}
	if m := regexpValue(page, "terms_token"); m != "" {
		form.Set("terms_token", m)
	}
	resp, _ = s.do("POST", "/admin/content/page/"+live.ID, form, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("clearing a live title: %d", resp.StatusCode)
	}
	if got, _ := cs.Get(ctx, live.ID); got.Title != "About" {
		t.Fatalf("title changed to %q", got.Title)
	}
}

// A published item's editor links to its public page through the
// content.public_path filter; drafts and unresolved paths show no link.
func TestAdminEditorViewOnSiteLink(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	s.app.Deps.Hooks.Filter(content.FilterPublicPath, 10, func(_ context.Context, v any) (any, error) {
		if p, ok := v.(*content.PublicPath); ok {
			p.Path = "/" + p.Item.Slug
		}
		return v, nil
	})
	live, _ := cs.Create(ctx, content.Input{Type: "page", Title: "About us", Slug: "about-us", Status: "published"})
	draft, _ := cs.Create(ctx, content.Input{Type: "page", Title: "Later", Slug: "later"})

	_, page := s.do("GET", "/admin/content/page/"+live.ID, nil, nil)
	if !strings.Contains(page, `href="/about-us" target="_blank" rel="noopener" data-view-live`) || !strings.Contains(page, "عرض على الموقع") {
		t.Fatal("published page has no View on site link")
	}
	if _, page = s.do("GET", "/admin/content/page/"+draft.ID, nil, nil); strings.Contains(page, "data-view-live") {
		t.Fatal("draft shows a View on site link")
	}
}

func regexpValue(page, name string) string {
	marker := `name="` + name + `" value="`
	i := strings.Index(page, marker)
	if i < 0 {
		return ""
	}
	rest := page[i+len(marker):]
	return rest[:strings.IndexByte(rest, '"')]
}
