package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	pngenc "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/builder"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

// gorun export writes the site as a folder a person or a diff can read:
// site facts, the identity, every item with its body, the builder layouts
// (published, and the draft only when it differs), media records and local
// files, and the public pages as rendered.
func TestSiteExportWritesAReadableFolder(t *testing.T) {
	data := t.TempDir()
	app := bootData(t, data, "")
	defer app.Close()
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "u1", Role: "admin", Status: auth.StatusActive, Name: "Admin"})
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	page, err := cs.Create(ctx, content.Input{Type: "page", Title: "About us", Slug: "about", Body: "<p>Hello <b>there</b></p>", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	post, err := cs.Create(ctx, content.Input{Type: "post", Title: "First post", Slug: "first", Body: "<p>Post body</p>", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	// A builder page: published layout plus a newer draft.
	home, err := cs.Create(ctx, content.Input{Type: "page", Title: "Services", Slug: "services", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	hero := builder.DefaultSection("hero", "en")
	hero.Title = "Services we offer"
	layouts, _ := kernel.Get[builder.Layouts](app.Deps, "builder")
	draft, err := layouts.Save(ctx, home.ID, builder.Document{Version: 1, Sections: []builder.Section{hero}}, content.EditToken(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := layouts.Publish(ctx, home.ID, draft.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	published, _ := layouts.Load(ctx, home.ID)
	hero.Title = "Services (draft)"
	if _, err := layouts.Save(ctx, home.ID, builder.Document{Version: 1, Sections: []builder.Section{hero}}, published.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	ms, _ := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
	var png bytes.Buffer
	if err := pngenc.Encode(&png, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	pngBytes := png.Bytes()
	m, err := ms.Upload(ctx, "dot.png", bytes.NewReader(pngBytes), media.UploadMeta{Title: "Dot"})
	if err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "site")
	rep, err := exportSite(context.Background(), app, "test", ExportOptions{Dest: dest, Pages: true, Media: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Items < 3 || rep.Layouts != 1 || rep.Media != 1 || rep.Files != 1 || rep.Pages < 2 {
		t.Fatalf("report: %+v", rep)
	}
	read := func(rel string) string {
		raw, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		return string(raw)
	}
	var facts map[string]any
	if err := json.Unmarshal([]byte(read("site.json")), &facts); err != nil || facts["theme"] == "" || facts["layout"] != "gorun-export/1" {
		t.Fatalf("site.json: %v %v", err, facts)
	}
	if !strings.Contains(read("content/page/about.json"), `"title": "About us"`) || strings.Contains(read("content/page/about.json"), "Hello <b>") {
		t.Fatal("about.json must carry the record without the body")
	}
	if read("content/page/about.html") != "<p>Hello <b>there</b></p>" {
		t.Fatalf("about.html = %q", read("content/page/about.html"))
	}
	if !strings.Contains(read("content/post/first.json"), `"status": "draft"`) {
		t.Fatal("drafts are part of the record")
	}
	if !strings.Contains(read("layouts/services.json"), "Services we offer") || !strings.Contains(read("layouts/services.draft.json"), "Services (draft)") {
		t.Fatal("layouts: published and differing draft expected")
	}
	if _, err := os.Stat(filepath.Join(dest, "layouts", "about.json")); err == nil {
		t.Fatal("a page without a layout must have no layout file")
	}
	if !strings.Contains(read("media/media.json"), m.ID) || read("media/files/"+m.StorageKey) != string(pngBytes) {
		t.Fatal("media record or file missing")
	}
	if !strings.Contains(read("pages/index.html"), "<html") || !strings.Contains(read("pages/about/index.html"), "there") {
		t.Fatal("rendered pages missing")
	}
	if !strings.Contains(read("export.json"), `"items"`) {
		t.Fatal("export.json missing")
	}
	_ = page
	_ = post
}
