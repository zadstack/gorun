package theme

import (
	"context"
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// TestRenderMarksThemePage: a successful theme render sets the request's
// page mark (analytics injects its script only into such pages); a failed
// render (the 500 page) does not.
func TestRenderMarksThemePage(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "base", "", baseTheme(map[string]string{
		"templates/archive.html": `{{part "header"}}{{ index .Items 5 }}`,
	}))
	e := newEnv(t, root, func(c *config.Config) { c.Theme = "base" })

	ctx, mark := httpx.WithPageMark(context.Background())
	rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil, ctx)
	if err != nil || rec.Code != http.StatusOK {
		t.Fatalf("index: %d %v", rec.Code, err)
	}
	if !mark.Themed() {
		t.Error("a theme render did not mark the page")
	}

	ctx, mark = httpx.WithPageMark(context.Background())
	rec, _ = e.render(kernel.KindRequest{Kind: KindArchive, Type: "post"}, nil, ctx)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("archive: %d, want 500", rec.Code)
	}
	if mark.Themed() {
		t.Error("the 500 page was marked as a theme page")
	}

	// Without a mark installed, rendering is unaffected.
	if rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("index without mark: %d %v", rec.Code, err)
	}
}
