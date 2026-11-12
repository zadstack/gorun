package content

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

type exportDoc struct {
	Format    string         `json:"format"`
	Version   int            `json:"version"`
	Items     []ExportItem   `json:"items"`
	Count     int            `json:"count"`
	NextAfter string         `json:"next_after"`
	Filters   map[string]any `json:"filters"`
}

func exportJSON(t *testing.T, s *service, ctx context.Context, opts ExportOptions) exportDoc {
	t.Helper()
	opts.Format = "json"
	var buf bytes.Buffer
	if _, err := s.Export(ctx, &buf, opts); err != nil {
		t.Fatal(err)
	}
	var doc exportDoc
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("export is not JSON: %v\n%s", err, buf.String())
	}
	return doc
}

func exportCSV(t *testing.T, s *service, ctx context.Context, opts ExportOptions) [][]string {
	t.Helper()
	opts.Format = "csv"
	var buf bytes.Buffer
	if _, err := s.Export(ctx, &buf, opts); err != nil {
		t.Fatal(err)
	}
	body := strings.TrimPrefix(buf.String(), "\xef\xbb\xbf")
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("export is not CSV: %v\n%s", err, body)
	}
	return rows
}

func ids(items []ExportItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

// Authors and contributors export exactly what the content list shows them: their own items
// and other people's published items of public types, never another author's private draft.
func TestExportRespectsReadPermissions(t *testing.T) {
	s := newTestService(t)
	mustType(t, s, Type{Slug: "invoice", Name: map[string]string{"en": "Invoice"}, Public: false})
	mustCreate(t, s, Input{Type: "post", Title: "Mine", AuthorID: "me", Status: "draft", Locale: "ar"})
	mustCreate(t, s, Input{Type: "post", Title: "Their draft", AuthorID: "other", Status: "draft", Locale: "ar"})
	mustCreate(t, s, Input{Type: "post", Title: "Their private", AuthorID: "other", Status: "private", Locale: "ar"})
	mustCreate(t, s, Input{Type: "post", Title: "Their live", AuthorID: "other", Status: "published", Locale: "ar"})
	mustCreate(t, s, Input{Type: "invoice", Title: "Their invoice", AuthorID: "other", Status: "published"})

	for _, role := range []string{"author", "contributor"} {
		ctx := auth.WithUser(context.Background(), &auth.User{ID: "me", Role: role})
		doc := exportJSON(t, s, ctx, ExportOptions{})
		got := strings.Join(ids(doc.Items), ",")
		if doc.Count != 2 || !strings.Contains(got, "Mine") || !strings.Contains(got, "Their live") {
			t.Fatalf("%s export: %d %s", role, doc.Count, got)
		}
		rows := exportCSV(t, s, ctx, ExportOptions{Status: "private"})
		if len(rows) != 1 {
			t.Fatalf("%s exported a private item: %v", role, rows)
		}
	}
	editor := auth.WithUser(context.Background(), &auth.User{ID: "ed", Role: "editor"})
	if doc := exportJSON(t, s, editor, ExportOptions{}); doc.Count != 5 {
		t.Fatalf("editor export: %d %v", doc.Count, ids(doc.Items))
	}
	// The system context (CLI) sees everything.
	if doc := exportJSON(t, s, context.Background(), ExportOptions{}); doc.Count != 5 {
		t.Fatalf("system export: %d", doc.Count)
	}
}

func TestExportCSVNeutralisesFormulas(t *testing.T) {
	s := newTestService(t)
	mustType(t, s, Type{Slug: "lead", Name: map[string]string{"en": "Lead"}, Public: true,
		Fields: []Field{{Key: "note", Type: "text"}, {Key: "score", Type: "number"}}})
	for _, title := range []string{"=HYPERLINK(\"http://x\")", "+1", "-2+3", "@SUM(A1)", "-Tab", "Plain"} {
		mustCreate(t, s, Input{Type: "lead", Title: title, Status: "draft", Locale: "ar", Fields: map[string]any{"note": "=cmd|'/c calc'!A0", "score": 7}})
	}
	rows := exportCSV(t, s, context.Background(), ExportOptions{Type: "lead", Columns: []string{"title", "fields.note", "fields.score"}})
	if len(rows) != 7 || strings.Join(rows[0], ",") != "title,fields.note,fields.score" {
		t.Fatalf("header/rows: %v", rows)
	}
	for _, row := range rows[1:] {
		if row[0] != "Plain" && !strings.HasPrefix(row[0], "'") {
			t.Errorf("formula cell not neutralised: %q", row[0])
		}
		if !strings.HasPrefix(row[1], "'=") {
			t.Errorf("custom field formula not neutralised: %q", row[1])
		}
		if row[2] != "7" {
			t.Errorf("number cell: %q", row[2])
		}
	}
}

// Exports read in keyset pages and write as they go: many rows arrive complete, in id order,
// and the HTTP limit leaves a cursor that continues exactly where it stopped.
func TestExportStreamsManyRowsInPages(t *testing.T) {
	s := newTestService(t)
	const n = 260
	for i := range n {
		mustCreate(t, s, Input{Type: "post", Title: fmt.Sprintf("Item %03d", i), Status: "draft", Locale: "ar"})
	}
	w := &countingWriter{}
	res, err := s.Export(context.Background(), w, ExportOptions{Format: "csv", Flush: w.flush})
	if err != nil || res.Count != n || res.NextAfter != "" {
		t.Fatalf("full export: %+v %v", res, err)
	}
	if w.flushes < 3 {
		t.Fatalf("expected a flush per page, got %d", w.flushes)
	}
	first := exportJSON(t, s, context.Background(), ExportOptions{Limit: 100})
	if first.Count != 100 || first.NextAfter == "" || first.NextAfter != first.Items[99].ID {
		t.Fatalf("limited export: count %d next %q", first.Count, first.NextAfter)
	}
	seen := map[string]bool{}
	after := first.NextAfter
	for _, it := range first.Items {
		seen[it.ID] = true
	}
	for after != "" {
		doc := exportJSON(t, s, context.Background(), ExportOptions{Limit: 100, After: after})
		for _, it := range doc.Items {
			if seen[it.ID] {
				t.Fatalf("duplicate %s", it.ID)
			}
			seen[it.ID] = true
		}
		after = doc.NextAfter
	}
	if len(seen) != n {
		t.Fatalf("cursor pages covered %d of %d", len(seen), n)
	}
}

type countingWriter struct {
	bytes.Buffer
	flushes int
}

func (c *countingWriter) flush() { c.flushes++ }

func TestExportFilters(t *testing.T) {
	s := newTestService(t)
	mustType(t, s, Type{Slug: "event", Name: map[string]string{"en": "Event"}, Public: true})
	a := mustCreate(t, s, Input{Type: "post", Title: "Arabic draft", AuthorID: "u1", Status: "draft", Locale: "ar"})
	mustCreate(t, s, Input{Type: "post", Title: "English live", AuthorID: "u2", Status: "published", Locale: "en"})
	mustCreate(t, s, Input{Type: "event", Title: "Event", AuthorID: "u1", Status: "draft", Locale: "ar"})
	trashed := mustCreate(t, s, Input{Type: "post", Title: "Gone", AuthorID: "u1", Status: "draft", Locale: "ar"})
	if err := s.Delete(context.Background(), trashed.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE content SET created_at = '2024-03-10T08:00:00Z' WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cases := []struct {
		name string
		opts ExportOptions
		want string
	}{
		{"all but trash", ExportOptions{}, "Arabic draft,English live,Event"},
		{"type", ExportOptions{Type: "event"}, "Event"},
		{"status list", ExportOptions{Status: "published,draft", Type: "post"}, "Arabic draft,English live"},
		{"trash on request", ExportOptions{Status: "trash"}, "Gone"},
		{"locale", ExportOptions{Locale: "en"}, "English live"},
		{"author", ExportOptions{AuthorID: "u1"}, "Arabic draft,Event"},
		{"to date", ExportOptions{DateField: "created_at", To: "2024-12-31"}, "Arabic draft"},
		{"from date", ExportOptions{From: "2025-01-01"}, "English live,Event"},
		{"published range", ExportOptions{DateField: "published_at", From: "2000-01-01T00:00:00Z"}, "English live"},
	}
	for _, tc := range cases {
		doc := exportJSON(t, s, ctx, tc.opts)
		titles := ids(doc.Items)
		got := strings.Join(sortedCopy(titles), ",")
		if got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestParseExportOptionsValidates(t *testing.T) {
	ok, err := ParseExportOptions(url.Values{"format": {"csv"}, "type": {"post"}, "status": {"draft,published"}, "locale": {"EN"},
		"from": {"2025-01-01"}, "to": {"2025-01-31T10:00:00+02:00"}, "date_field": {"updated_at"}, "columns": {"id,title,fields.price"}, "limit": {"50"}})
	if err != nil {
		t.Fatal(err)
	}
	if ok.Format != "csv" || ok.Locale != "en" || ok.Limit != 50 || len(ok.Columns) != 3 || ok.DateField != "updated_at" {
		t.Fatalf("parsed: %+v", ok)
	}
	if def, _ := ParseExportOptions(url.Values{}); def.Format != "json" || def.Limit != MaxExportLimit {
		t.Fatalf("defaults: %+v", def)
	}
	for name, bad := range map[string]url.Values{
		"format":     {"format": {"xml"}},
		"status":     {"status": {"lost"}},
		"date_field": {"date_field": {"title"}},
		"from":       {"from": {"yesterday"}},
		"limit":      {"limit": {"10001"}},
		"columns":    {"columns": {"id,password"}},
		"locale":     {"locale": {"not a tag"}},
	} {
		_, err := ParseExportOptions(bad)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Fields[name] == "" {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}
}

// The JSON item is complete enough to recreate the item: fields, SEO, terms by slug, translation
// group, author and media references with URLs.
func TestExportJSONItemIsComplete(t *testing.T) {
	s := newTestService(t)
	s.mediaFn = func() MediaURLResolver { return fakeMedia{"m1": "/uploads/a.jpg", "m2": "https://cdn.example/b.pdf"} }
	mustType(t, s, Type{Slug: "doc", Name: map[string]string{"en": "Doc"}, Public: true,
		Fields: []Field{{Key: "cover", Type: "image"}, {Key: "files", Type: "file", Multiple: true}, {Key: "level", Type: "text"}}})
	it := mustCreate(t, s, Input{Type: "doc", Title: "Guide", Slug: "guide", Body: "<p>Body</p>", Status: "draft", Locale: "ar",
		AuthorID: "me", FeaturedMediaID: "m1", SEO: map[string]any{"title": "SEO title"},
		Fields: map[string]any{"cover": "m1", "files": []any{"m2"}, "level": "basic"}})
	if _, err := s.CreateTranslation(context.Background(), it.ID, "en"); err != nil {
		t.Fatal(err)
	}
	doc := exportJSON(t, s, context.Background(), ExportOptions{Type: "doc", Locale: "ar", BaseURL: "https://site.example"})
	if doc.Format != "gorun.content-export" || doc.Version != 1 || len(doc.Items) != 1 {
		t.Fatalf("doc: %+v", doc)
	}
	got := doc.Items[0]
	if got.Body != "<p>Body</p>" || got.Fields["level"] != "basic" || got.SEO["title"] != "SEO title" || got.TranslationGroup == "" ||
		got.Author.ID != "me" || got.FeaturedMediaID != "m1" || got.CreatedAt == "" {
		t.Fatalf("item: %+v", got)
	}
	media := map[string]string{}
	for _, m := range got.Media {
		media[m.ID] = m.URL
	}
	if media["m1"] != "https://site.example/uploads/a.jpg" || media["m2"] != "https://cdn.example/b.pdf" || len(media) != 2 {
		t.Fatalf("media: %+v", got.Media)
	}
}

type fakeMedia map[string]string

func (f fakeMedia) MediaURLs(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if u, ok := f[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}
