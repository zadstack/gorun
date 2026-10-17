package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUploadJPEGCreatesVariants(t *testing.T) {
	s, h := testService(t, false)
	var emitted atomic.Int32
	h.Action(HookUploaded, 0, func(context.Context, any) error { emitted.Add(1); return nil })
	data := jpegBytes(t, 1600, 800)
	m, err := s.Upload(context.Background(), "Photo One.JPEG", bytes.NewReader(data), UploadMeta{Alt: "a photo"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Mime != "image/jpeg" || m.Width != 1600 || m.Height != 800 || m.Alt != "a photo" || m.Title != "Photo One" {
		t.Fatalf("media: %+v", m)
	}
	if !strings.HasPrefix(m.StorageKey, "2026/10/") || !strings.HasSuffix(m.StorageKey, ".jpg") || m.URL != "/media/"+m.StorageKey {
		t.Fatalf("key/url: %q %q", m.StorageKey, m.URL)
	}
	// The fixture's original JPEG is already compact: its 1536px re-encode
	// costs more bytes, so the original remains the largest srcset candidate.
	want := map[string][2]int{"thumbnail": {300, 150}, "medium": {768, 384}}
	if len(m.Variants) != len(want) {
		t.Fatalf("variants: %+v", m.Variants)
	}
	for name, wh := range want {
		v := m.Variants[name]
		if v.Width != wh[0] || v.Height != wh[1] || !strings.HasSuffix(v.URL, "-"+name+".jpg") {
			t.Errorf("%s: %+v", name, v)
		}
		rc, err := s.storage.Open(context.Background(), strings.TrimPrefix(v.URL, URLPrefix))
		if err != nil {
			t.Fatalf("%s not stored: %v", name, err)
		}
		cfg, err := imageConfig(mustRead(t, rc))
		if err != nil || cfg.Width != wh[0] {
			t.Errorf("%s stored dims: %+v %v", name, cfg, err)
		}
	}
	if s.URL(m, "medium") != m.Variants["medium"].URL || s.URL(m, "full") != m.URL {
		t.Fatal("URL(variant)")
	}
	got, err := s.Get(context.Background(), m.ID)
	if err != nil || got.Checksum != m.Checksum || len(got.Variants) != len(want) || got.Variants["medium"].Width != 768 {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	if emitted.Load() != 1 || auditCount(t, s.db, "media.upload") != 1 {
		t.Fatalf("hook=%d audit=%d", emitted.Load(), auditCount(t, s.db, "media.upload"))
	}
	// small images are never upscaled
	small, err := s.Upload(context.Background(), "s.jpg", bytes.NewReader(jpegBytes(t, 400, 200)), UploadMeta{})
	if err != nil || len(small.Variants) != 1 || small.Variants["thumbnail"].Width != 300 {
		t.Fatalf("small: %+v %v", small, err)
	}
}

func mustRead(t *testing.T, rc io.ReadCloser) []byte {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUploadRejectsDisallowedMime(t *testing.T) {
	s, _ := testService(t, false)
	ctx := context.Background()
	// HTML disguised with an allowed extension: the sniffed type wins.
	_, err := s.Upload(ctx, "evil.jpg", strings.NewReader("<html><script>alert(1)</script></html>"), UploadMeta{})
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("html as .jpg: %v", err)
	}
	// A real JPEG renamed to .png is a mismatch too.
	if _, err := s.Upload(ctx, "x.png", bytes.NewReader(jpegBytes(t, 20, 20)), UploadMeta{}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("jpeg as .png: %v", err)
	}
	// Unknown extension.
	if _, err := s.Upload(ctx, "x.exe", bytes.NewReader(jpegBytes(t, 20, 20)), UploadMeta{}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf(".exe: %v", err)
	}
	// Allowlist setting narrows accepted types.
	if err := s.settings.Set(ctx, SettingAllowedMimes, []string{"image/png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, "ok.jpg", bytes.NewReader(jpegBytes(t, 20, 20)), UploadMeta{}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("jpeg not in allowlist: %v", err)
	}
	// Size limit from media.max_upload_mb.
	if err := s.settings.Set(ctx, SettingMaxUploadMB, 1); err != nil {
		t.Fatal(err)
	}
	big := append([]byte("%PDF-1.4\n"), make([]byte, 1<<20)...)
	if _, err := s.Upload(ctx, "big.pdf", bytes.NewReader(big), UploadMeta{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	if _, err := s.Upload(ctx, "e.pdf", strings.NewReader(""), UploadMeta{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM media`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected uploads stored %d rows", n)
	}
}

const evilSVG = `<?xml version="1.0"?>
<!DOCTYPE svg [<!ENTITY x "y">]>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="10" height="10" onload="alert(1)">
  <!-- comment -->
  <script type="text/javascript">alert('x')</script>
  <a xlink:href="javascript:alert(2)"><rect width="5" height="5" fill="red" onclick="alert(3)"/></a>
  <a href=" JaVa&#x09;ScRiPt:alert(4)"><circle r="2"/></a>
  <foreignObject><iframe src="https://evil.example"></iframe></foreignObject>
  <use href="https://evil.example/x.svg#a"/>
  <use xlink:href="#ok"/>
  <set attributeName="href" to="javascript:alert(5)"/>
  <style>@import url(https://evil.example/x.css);</style>
  <g style="fill:url(#grad)"><path d="M0 0L5 5" style="background:url(javascript:alert(6))"/></g>
  <text>5 &lt; 6 &amp; ok</text>
</svg>`

func TestUploadSVGSanitizedNoScript(t *testing.T) {
	s, _ := testService(t, false)
	m, err := s.Upload(context.Background(), "logo.svg", strings.NewReader(evilSVG), UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Mime != "image/svg+xml" || len(m.Variants) != 0 {
		t.Fatalf("media: %+v", m)
	}
	rc, err := s.storage.Open(context.Background(), m.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	out := string(mustRead(t, rc))
	lower := strings.ToLower(out)
	for _, bad := range []string{"<script", "onload", "onclick", "javascript", "foreignobject", "iframe", "evil.example",
		"<!--", "<!doctype", "<set", "@import"} {
		if strings.Contains(lower, bad) {
			t.Errorf("sanitized SVG still contains %q:\n%s", bad, out)
		}
	}
	for _, good := range []string{`<svg xmlns="http://www.w3.org/2000/svg"`, `<rect width="5" height="5" fill="red">`,
		`xlink:href="#ok"`, `style="fill:url(#grad)"`, `5 &lt; 6 &amp; ok`, `<path d="M0 0L5 5">`} {
		if !strings.Contains(out, good) {
			t.Errorf("sanitized SVG lost %q:\n%s", good, out)
		}
	}
	// Reviewer bypass cases: each must lose its external reference.
	bypasses := map[string]string{
		"style child element": `<svg xmlns="http://www.w3.org/2000/svg"><style><g/>@import 'https://evil.example/x.css'; a{background:url(https://evil.example/t)}</style><rect width="1" height="1"/></svg>`,
		"style comment split": `<svg xmlns="http://www.w3.org/2000/svg"><style>@im<!-- -->port 'https://evil.example/x.css';</style></svg>`,
		"style cdata split":   `<svg xmlns="http://www.w3.org/2000/svg"><style>a{background:u<![CDATA[rl(https://evil.example/t)}]]></style></svg>`,
		"presentation attrs":  `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url(https://evil.example/t)" filter="url(https://evil.example/f)" mask="url( 'https://evil.example/m')" clip-path="url(https://evil.example/c)" marker-end="url(https://evil.example/e)" width="1" height="1"/></svg>`,
		"foreign namespace":   `<svg xmlns="http://www.w3.org/2000/svg" xmlns:evil="https://evil.example/ns"><g xmlns="http://www.w3.org/1999/xhtml"><rect width="1" height="1"/></g></svg>`,
		"css escape in style": `<svg xmlns="http://www.w3.org/2000/svg"><style>a{background:u\72 l(https://evil.example/t)}</style></svg>`,
		"css escape in attr":  `<svg xmlns="http://www.w3.org/2000/svg"><rect style="fill:u\72 l(https://evil.example/t)" fill="\75 rl(https://evil.example/f)" width="1" height="1"/></svg>`,
		"escaped import":      `<svg xmlns="http://www.w3.org/2000/svg"><style>@\69 mport 'https://evil.example/x.css';</style></svg>`,
		"image-set":           `<svg xmlns="http://www.w3.org/2000/svg"><style>a{background-image:image-set("https://evil.example/a.png" 1x)}</style></svg>`,
		"image-set attr":      `<svg xmlns="http://www.w3.org/2000/svg"><rect style="background:-webkit-image-set('https://evil.example/a.png' 1x)" width="1" height="1"/></svg>`,
		"src()":               `<svg xmlns="http://www.w3.org/2000/svg"><style>a{background:src("https://evil.example/s")}</style></svg>`,
		"image()":             `<svg xmlns="http://www.w3.org/2000/svg"><style>a{background:image("https://evil.example/i")}</style></svg>`,
		"cross-fade()":        `<svg xmlns="http://www.w3.org/2000/svg"><rect style="background:cross-fade('https://evil.example/a', 'https://evil.example/b', 50%)" width="1" height="1"/></svg>`,
		// I11: escapes without the trailing space, and XML entities that
		// decode to a backslash (attribute values and <style> text).
		"escape no space":         `<svg xmlns="http://www.w3.org/2000/svg"><style>a{background:\75rl(https://evil.example/t)}</style><rect style="fill:\75rl(https://evil.example/f)" width="1" height="1"/></svg>`,
		"escaped import no space": `<svg xmlns="http://www.w3.org/2000/svg"><style>@\69mport 'https://evil.example/x.css';</style></svg>`,
		"entity backslash attr":   `<svg xmlns="http://www.w3.org/2000/svg"><rect style="fill:&#92;75rl(https://evil.example/t)" fill="&#x5c;75 rl(https://evil.example/f)" width="1" height="1"/></svg>`,
		"entity backslash style":  `<svg xmlns="http://www.w3.org/2000/svg"><style>@&#92;69mport 'https://evil.example/x.css'; a{background:u&#x5C;72l(https://evil.example/t)}</style></svg>`,
		"entity letters + escape": `<svg xmlns="http://www.w3.org/2000/svg"><rect style="fill:&#117;\72&#108;(https://evil.example/t)" width="1" height="1"/></svg>`,
		"entity image-set":        `<svg xmlns="http://www.w3.org/2000/svg"><rect style="background:image&#45;set('https://evil.example/a.png' 1x)" width="1" height="1"/></svg>`,
		"upper and spaced":        `<svg xmlns="http://www.w3.org/2000/svg"><style>a{background:IMAGE-SET( "https://evil.example/a.png" 1x);b:Cross-Fade ('https://evil.example/c')}</style></svg>`,
	}
	for name, doc := range bypasses {
		m, err := s.Upload(context.Background(), "b.svg", strings.NewReader(doc), UploadMeta{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rc, _ := s.storage.Open(context.Background(), m.StorageKey)
		got := string(mustRead(t, rc))
		if strings.Contains(got, "evil.example") || strings.Contains(got, "@import") || strings.Contains(got, "xhtml") {
			t.Errorf("%s survived:\n%s", name, got)
		}
		if !strings.HasPrefix(got, `<svg xmlns="http://www.w3.org/2000/svg"`) {
			t.Errorf("%s: root lost its namespace:\n%s", name, got)
		}
	}
	// A safe <style> is kept intact; a root without the SVG namespace is rejected.
	m2, err := s.Upload(context.Background(), "ok.svg", strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg"><style>.a{fill:url(#g)}</style></svg>`), UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	rc2, _ := s.storage.Open(context.Background(), m2.StorageKey)
	if got := string(mustRead(t, rc2)); !strings.Contains(got, "<style>.a{fill:url(#g)}</style>") {
		t.Fatalf("safe style lost: %s", got)
	}
	if _, err := s.Upload(context.Background(), "nons.svg", strings.NewReader(`<svg><rect/></svg>`), UploadMeta{}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("svg without namespace: %v", err)
	}
	// Design tools often start with a comment (sniffs as text/html).
	if m, err := s.Upload(context.Background(), "gen.svg", strings.NewReader(`<!-- Generator: X --><svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`), UploadMeta{}); err != nil || m.Mime != "image/svg+xml" {
		t.Fatalf("svg with leading comment: %v", err)
	}
	// HTML that merely contains an svg element is still rejected.
	if _, err := s.Upload(context.Background(), "x.svg", strings.NewReader(`<!-- x --><html><svg></svg></html>`), UploadMeta{}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("html with svg inside: %v", err)
	}
	// Not an SVG root → rejected.
	if _, err := s.Upload(context.Background(), "x.svg", strings.NewReader(`<html><body/></html>`), UploadMeta{}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("non-svg xml: %v", err)
	}
}

func TestFromURLBlocksPrivateHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jpegBytes(t, 10, 10))
	}))
	defer srv.Close()
	s, _ := testService(t, false)
	if _, err := s.FromURL(context.Background(), srv.URL+"/a.jpg", UploadMeta{}); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("loopback fetch must be blocked: %v", err)
	}
	for _, bad := range []string{"file:///etc/passwd", "ftp://x/y.jpg", "http:///nohost", "::bad"} {
		if _, err := s.FromURL(context.Background(), bad, UploadMeta{}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFromURLDedupesBySourceURL(t *testing.T) {
	var hits atomic.Int32
	img := jpegBytes(t, 10, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/missing.jpg" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(img)
	}))
	defer srv.Close()
	s, _ := testService(t, true)
	ctx := context.Background()
	u := srv.URL + "/wp-content/uploads/photo"
	a, err := s.FromURL(ctx, u, UploadMeta{Alt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if a.SourceURL != u || a.Filename != "photo.jpg" || a.Mime != "image/jpeg" {
		t.Fatalf("first: %+v", a)
	}
	b, err := s.FromURL(ctx, u, UploadMeta{})
	if err != nil || b.ID != a.ID || hits.Load() != 1 {
		t.Fatalf("dedupe: %v id=%s/%s hits=%d", err, a.ID, b.ID, hits.Load())
	}
	if f, err := s.FindBySourceURL(ctx, u); err != nil || f.ID != a.ID {
		t.Fatalf("FindBySourceURL: %v", err)
	}
	if _, err := s.FindBySourceURL(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("empty source url must not match uploads")
	}
	if _, err := s.FromURL(ctx, srv.URL+"/missing.jpg", UploadMeta{}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("non-200: %v", err)
	}
}

func TestUpdateListDelete(t *testing.T) {
	s, h := testService(t, false)
	ctx := context.Background()
	var deleted atomic.Int32
	h.Action(HookDeleted, 0, func(_ context.Context, p any) error {
		var m Media
		if err := json.Unmarshal(p.(json.RawMessage), &m); err != nil || m.ID == "" {
			t.Errorf("deleted payload: %v", err)
		}
		deleted.Add(1)
		return nil
	})
	a, _ := s.Upload(ctx, "alpha.jpg", bytes.NewReader(jpegBytes(t, 500, 400)), UploadMeta{})
	b, _ := s.Upload(ctx, "beta_100%.pdf", strings.NewReader("%PDF-1.4\nhello"), UploadMeta{})
	alt := "  New alt "
	m, err := s.Update(ctx, a.ID, Patch{Alt: &alt})
	if err != nil || m.Alt != "New alt" || auditCount(t, s.db, "media.update") != 1 {
		t.Fatalf("update: %+v %v", m, err)
	}
	items, total, err := s.List(ctx, ListQuery{Search: "100%"})
	if err != nil || total != 1 || items[0].ID != b.ID {
		t.Fatalf("search: %v %d %+v", err, total, items)
	}
	items, total, _ = s.List(ctx, ListQuery{Sort: "filename", Order: "asc"})
	if total != 2 || items[0].ID != a.ID {
		t.Fatalf("sort: %+v", items)
	}
	if err := s.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("still there")
	}
	if _, err := s.storage.Open(ctx, a.StorageKey); err == nil {
		t.Fatal("original file not removed")
	}
	if _, err := s.storage.Open(ctx, strings.TrimPrefix(a.Variants["thumbnail"].URL, URLPrefix)); err == nil {
		t.Fatal("variant not removed")
	}
	if deleted.Load() != 1 || auditCount(t, s.db, "media.delete") != 1 {
		t.Fatal("delete hook/audit")
	}
	if err := s.Delete(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// A revised file (source URL + RevisionMarker) is what the plain source URL
// resolves to: FindBySourceURL and FromURL return the newest revision.
func TestSourceURLLookupFindsNewestRevision(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(jpegBytes(t, 4, 4))
	}))
	defer srv.Close()
	s, _ := testService(t, true)
	ctx := context.Background()
	plain := "https://wp.example/wp-content/uploads/a.jpg"
	orig, err := s.Upload(ctx, "a.jpg", bytes.NewReader(jpegBytes(t, 6, 6)), UploadMeta{SourceURL: plain})
	if err != nil {
		t.Fatal(err)
	}
	if f, err := s.FindBySourceURL(ctx, plain); err != nil || f.ID != orig.ID {
		t.Fatalf("plain before revision: %v", err)
	}
	rev, err := s.Upload(ctx, "a.jpg", bytes.NewReader(jpegBytes(t, 8, 8)), UploadMeta{SourceURL: plain + RevisionMarker + "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if f, err := s.FindBySourceURL(ctx, plain); err != nil || f.ID != rev.ID {
		t.Fatalf("plain must resolve to the newest revision: %v %+v", err, f)
	}
	if f, err := s.FindBySourceURL(ctx, plain+RevisionMarker+"abc123"); err != nil || f.ID != rev.ID {
		t.Fatalf("exact revision lookup: %v", err)
	}
	if _, err := s.FindBySourceURL(ctx, plain+RevisionMarker+"zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown revision: %v", err)
	}
	if _, err := s.FindBySourceURL(ctx, "https://wp.example/wp-content/uploads/a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a URL prefix must not match: %v", err)
	}
	if f, err := s.FromURL(ctx, plain, UploadMeta{}); err != nil || f.ID != rev.ID || hits.Load() != 0 {
		t.Fatalf("FromURL must reuse the current revision without downloading: %v hits=%d", err, hits.Load())
	}
	// An older revision made current again wins until a newer upload.
	if err := s.MarkCurrent(ctx, orig.ID); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.FindBySourceURL(ctx, plain); f.ID != orig.ID {
		t.Fatalf("MarkCurrent: plain resolves to %s, want %s", f.ID, orig.ID)
	}
	rev2, err := s.Upload(ctx, "a.jpg", bytes.NewReader(jpegBytes(t, 9, 9)), UploadMeta{SourceURL: plain + RevisionMarker + "def456"})
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := s.FindBySourceURL(ctx, plain); f.ID != rev2.ID {
		t.Fatalf("a newer upload must win over a marked revision: %s", f.ID)
	}
	if err := s.MarkCurrent(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkCurrent(missing): %v", err)
	}
}
