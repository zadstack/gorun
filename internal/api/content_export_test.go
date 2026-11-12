package api_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func TestContentExportREST(t *testing.T) {
	app := apitest.Boot(t, content.New(), taxonomy.New(), media.New())
	admin := apitest.NewClient(t, app)
	ctx := context.Background()
	ms, _ := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
	img, err := ms.Upload(ctx, "pixel.png", bytes.NewReader(pngPixel), media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	status, env := admin.Post("/content", map[string]any{"type": "post", "title": "=1+1 Story", "body": "<p>Hi</p>", "locale": "ar",
		"featured_media_id": img.ID, "terms": map[string]any{"tag": []string{"go"}}})
	if status != 201 {
		t.Fatalf("fixture: %d %v", status, env)
	}
	id := apitest.Data(env)["id"].(string)

	// JSON: an attachment with the whole item and absolute media URLs.
	w := admin.Do("GET", "/content/export?format=json&type=post", nil, nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("json export: %d %v\n%s", w.Code, w.Header(), w.Body.String())
	}
	var doc struct {
		Format string               `json:"format"`
		Items  []content.ExportItem `json:"items"`
		Count  int                  `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, w.Body.String())
	}
	if doc.Format != content.ExportFormatName || doc.Count != 1 || doc.Items[0].ID != id || doc.Items[0].Terms["tag"][0] != "go" {
		t.Fatalf("doc: %+v", doc)
	}
	if m := doc.Items[0].Media; len(m) != 1 || m[0].ID != img.ID || !strings.HasPrefix(m[0].URL, "http://localhost/") {
		t.Fatalf("media: %+v", m)
	}
	if doc.Items[0].Author.Email == "" {
		t.Fatal("an administrator's export lacks the author email")
	}

	// CSV with chosen columns, formula-safe.
	w = admin.Do("GET", "/content/export?format=csv&columns=id,title,featured_media_url,terms", nil, nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("csv export: %d %v", w.Code, w.Header())
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\xef\xbb\xbf"))).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][1] != "'=1+1 Story" || !strings.HasPrefix(rows[1][2], "http://localhost/") || rows[1][3] != "tag:go" {
		t.Fatalf("csv rows: %v %v", rows, err)
	}

	// Validation happens before anything streams.
	if w = admin.Do("GET", "/content/export?format=xml", nil, nil); w.Code != 422 || !strings.Contains(w.Body.String(), "validation_failed") {
		t.Fatalf("bad format: %d %s", w.Code, w.Body.String())
	}
	if w = admin.Do("GET", "/content/export?type=nope", nil, nil); w.Code != 422 {
		t.Fatalf("unknown type: %d %s", w.Code, w.Body.String())
	}
	// Scope and authentication.
	if w = admin.Anonymous().Do("GET", "/content/export", nil, nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	if w = apitest.NewClient(t, app, "media.upload").Do("GET", "/content/export", nil, nil); w.Code != 403 {
		t.Fatalf("missing content.view: %d", w.Code)
	}

	// An author's key exports only what the author may read, without other people's emails.
	author := apitest.New(t, app, apitest.Options{Role: "author", Scopes: []string{"content.view", "content.create"}})
	if status, env = author.Post("/content", map[string]any{"type": "post", "title": "Author draft", "locale": "ar"}); status != 201 {
		t.Fatalf("author draft: %d %v", status, env)
	}
	if status, _ = admin.Post("/content", map[string]any{"type": "post", "title": "Admin private", "status": "private", "locale": "ar"}); status != 201 {
		t.Fatal("admin private")
	}
	if status, _ = admin.Post("/content/"+id+"/publish", nil); status != 200 {
		t.Fatal("publish")
	}
	w = author.Do("GET", "/content/export?format=json", nil, nil)
	doc.Items = nil
	_ = json.Unmarshal(w.Body.Bytes(), &doc)
	titles := []string{}
	for _, it := range doc.Items {
		titles = append(titles, it.Title)
		if it.Author.ID != author.User.ID && it.Author.Email != "" {
			t.Fatalf("author export leaks another author's email: %+v", it.Author)
		}
		if it.Author.ID == author.User.ID && it.Author.Email == "" {
			t.Fatalf("author export lacks the author's own email: %+v", it.Author)
		}
	}
	if w.Code != 200 || len(doc.Items) != 2 || strings.Contains(strings.Join(titles, ","), "Admin private") {
		t.Fatalf("author export: %d %v", w.Code, titles)
	}

	// Exports are audited with filters and counts, never contents.
	var details string
	if err := app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'content.export' ORDER BY at DESC, rowid DESC LIMIT 1`).Scan(&details); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(details, `"count":2`) || !strings.Contains(details, `"via":"api"`) || strings.Contains(details, "Author draft") {
		t.Fatalf("audit details: %s", details)
	}
}

// pngPixel is a 1×1 transparent PNG.
var pngPixel = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01,
	0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78,
	0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82}
