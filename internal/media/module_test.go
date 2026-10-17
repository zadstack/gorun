package media_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

type site struct {
	t    *testing.T
	srv  *httptest.Server
	app  *kernel.App
	c    *http.Client
	csrf string
}

// boot starts settings + auth + media, creates an admin (or the given role)
// and logs in.
func boot(t *testing.T, role string) *site {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), media.New()})
	if err != nil {
		t.Fatal(err)
	}
	// Mount the API operations the way the api module will (Task 9).
	api := chi.NewRouter()
	api.Use(auth.APIKeyMiddleware(app.Deps))
	for _, op := range app.Deps.API.Operations() {
		api.Method(op.Method, op.Path, op.Handler)
	}
	root := chi.NewRouter()
	root.Mount("/api", api)
	root.Mount("/", app.Handler)
	srv := httptest.NewServer(root)
	t.Cleanup(func() { srv.Close(); app.Close() })
	jar, _ := cookiejar.New(nil)
	s := &site{t: t, srv: srv, app: app, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	svc, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "u@example.com", "U", "correct horse battery", role); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/login", nil, "")
	s.csrf = csrfFrom(t, page)
	resp, _ := s.do("POST", "/admin/login", strings.NewReader(url.Values{"_csrf": {s.csrf}, "email": {"u@example.com"}, "password": {"correct horse battery"}}.Encode()), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	_, page = s.do("GET", "/admin", nil, "")
	s.csrf = csrfFrom(t, page)
	return s
}

var csrfRe = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

func csrfFrom(t *testing.T, html string) string {
	t.Helper()
	m := csrfRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no csrf meta in:\n%s", html)
	}
	return m[1]
}

func (s *site) do(method, path string, body io.Reader, ctype string, hdr ...string) (*http.Response, string) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.srv.URL+path, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if s.csrf != "" {
		req.Header.Set("X-CSRF-Token", s.csrf)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, x%h, color.NRGBA{200, 10, 10, 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func multipartBody(t *testing.T, fields map[string]string, files map[string][]byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	for name, data := range files {
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write(data)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func mediaSvc(t *testing.T, s *site) media.Service {
	svc, ok := kernel.Get[media.Service](s.app.Deps, kernel.ServiceMedia)
	if !ok {
		t.Fatal("media service not registered")
	}
	return svc
}

func TestMediaServeCacheHeaders(t *testing.T) {
	s := boot(t, "admin")
	svc := mediaSvc(t, s)
	m, err := svc.Upload(context.Background(), "dot.png", bytes.NewReader(pngBytes(t, 400, 40)), media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	resp, body := s.do("GET", m.URL, nil, "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" ||
		resp.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("serve: %d %v", resp.StatusCode, resp.Header)
	}
	if !bytes.Equal([]byte(body), pngBytes(t, 400, 40)) {
		t.Fatal("body differs")
	}
	if resp, _ := s.do("GET", svc.URL(m, "thumbnail"), nil, ""); resp.StatusCode != 200 {
		t.Fatalf("variant: %d", resp.StatusCode)
	}
	// Range requests (video seeking) work.
	if resp, body := s.do("GET", m.URL, nil, "", "Range", "bytes=0-3"); resp.StatusCode != http.StatusPartialContent || body != "\x89PNG" {
		t.Fatalf("range: %d %q", resp.StatusCode, body)
	}
	svg, err := svc.Upload(context.Background(), "a.svg", strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`), media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	resp, _ = s.do("GET", svg.URL, nil, "")
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/svg+xml") || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("svg headers: %v", resp.Header)
	}
	for _, p := range []string{"/media/../gorun.db", "/media/2026/10/missing.png", "/media/x", "/media/2026/10/a.PNG"} {
		if resp, _ := s.do("GET", p, nil, ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestAdminLibraryUploadEditDelete(t *testing.T) {
	s := boot(t, "admin")
	resp, page := s.do("GET", "/admin/media", nil, "")
	if resp.StatusCode != 200 || !strings.Contains(page, `id="media-upload"`) || !strings.Contains(page, `hx-encoding="multipart/form-data"`) {
		t.Fatalf("library: %d\n%s", resp.StatusCode, page)
	}
	body, ct := multipartBody(t, nil, map[string][]byte{"red.png": pngBytes(t, 50, 50), "bad.png": []byte("<html></html>")})
	resp, page = s.do("POST", "/admin/media", body, ct, "HX-Request", "true")
	if resp.StatusCode != 200 || !strings.Contains(page, `id="media-results"`) || !strings.Contains(page, "bad.png") || !strings.Contains(page, `role="alert"`) {
		t.Fatalf("upload: %d\n%s", resp.StatusCode, page)
	}
	if strings.Contains(page, "<html lang") {
		t.Fatal("HTMX upload response must not include the layout")
	}
	items, total, _ := mediaSvc(t, s).List(context.Background(), media.ListQuery{})
	if total != 1 {
		t.Fatalf("stored %d", total)
	}
	id := items[0].ID
	// Upload without the CSRF header is rejected.
	csrf := s.csrf
	s.csrf = ""
	body, ct = multipartBody(t, nil, map[string][]byte{"x.png": pngBytes(t, 5, 5)})
	if resp, _ := s.do("POST", "/admin/media", body, ct, "HX-Request", "true"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no csrf: %d", resp.StatusCode)
	}
	s.csrf = csrf
	if resp, page := s.do("GET", "/admin/media/"+id, nil, ""); resp.StatusCode != 200 || !strings.Contains(page, "red.png") {
		t.Fatalf("edit page: %d", resp.StatusCode)
	}
	form := url.Values{"alt": {"Red square"}, "title": {"Red"}, "caption": {""}}
	if resp, _ := s.do("POST", "/admin/media/"+id, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update: %d", resp.StatusCode)
	}
	if m, _ := mediaSvc(t, s).Get(context.Background(), id); m.Alt != "Red square" {
		t.Fatalf("alt not saved: %+v", m)
	}
	resp, page = s.do("GET", "/admin/x/media/picker?target=og&q=Red", nil, "", "HX-Request", "true")
	if resp.StatusCode != 200 || !strings.Contains(page, `data-target="og"`) || !strings.Contains(page, "media:selected") || !strings.Contains(page, `data-id="`+id+`"`) {
		t.Fatalf("picker: %d\n%s", resp.StatusCode, page)
	}
	if resp, _ := s.do("GET", "/admin/media/nope", nil, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing: %d", resp.StatusCode)
	}
	if resp, _ := s.do("POST", "/admin/media/"+id+"/delete", strings.NewReader(""), "application/x-www-form-urlencoded"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, total, _ := mediaSvc(t, s).List(context.Background(), media.ListQuery{}); total != 0 {
		t.Fatal("not deleted")
	}
}

func TestAdminMediaRequiresPermission(t *testing.T) {
	s := boot(t, "contributor")
	if resp, _ := s.do("GET", "/admin/media", nil, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("contributor: %d", resp.StatusCode)
	}
}

func TestMediaAPI(t *testing.T) {
	s := boot(t, "admin")
	body, ct := multipartBody(t, map[string]string{"alt": "api alt"}, map[string][]byte{"api.png": pngBytes(t, 20, 20)})
	resp, out := s.do("POST", "/api/v1/media", body, ct)
	var env struct {
		OK   bool        `json:"ok"`
		Data media.Media `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || resp.StatusCode != http.StatusCreated || env.Data.Alt != "api alt" {
		t.Fatalf("upload: %d %s", resp.StatusCode, out)
	}
	id := env.Data.ID
	if resp, out := s.do("GET", "/api/v1/media?per_page=5", nil, ""); resp.StatusCode != 200 || !strings.Contains(out, `"total":1`) {
		t.Fatalf("list: %d %s", resp.StatusCode, out)
	}
	if resp, out := s.do("PATCH", "/api/v1/media/"+id, strings.NewReader(`{"title":"T2"}`), "application/json"); resp.StatusCode != 200 || !strings.Contains(out, `"title":"T2"`) {
		t.Fatalf("patch: %d %s", resp.StatusCode, out)
	}
	body, ct = multipartBody(t, nil, map[string][]byte{"x.pdf": []byte("<html>")})
	if resp, _ := s.do("POST", "/api/v1/media", body, ct); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("bad type: %d", resp.StatusCode)
	}
	if resp, _ := s.do("DELETE", "/api/v1/media/"+id, nil, ""); resp.StatusCode != 200 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp, _ := s.do("GET", "/api/v1/media/"+id, nil, ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted: %d", resp.StatusCode)
	}
}

func TestAdminMediaThumbEndpoint(t *testing.T) {
	s := boot(t, "admin")
	ctx := context.Background()
	m, err := mediaSvc(t, s).Upload(ctx, "red.png", bytes.NewReader(pngBytes(t, 40, 40)), media.UploadMeta{Alt: "Red"})
	if err != nil {
		t.Fatal(err)
	}
	resp, body := s.do("GET", "/admin/x/media/"+m.ID+"/thumb", nil, "")
	var env struct {
		OK   bool `json:"ok"`
		Data struct{ ID, URL, Thumb, Alt, Filename, Mime string }
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || resp.StatusCode != 200 || !env.OK {
		t.Fatalf("thumb: %d %s", resp.StatusCode, body)
	}
	if env.Data.ID != m.ID || env.Data.Alt != "Red" || env.Data.Mime != "image/png" || env.Data.URL != m.URL || env.Data.Thumb == "" {
		t.Fatalf("thumb data: %+v", env.Data)
	}
	if resp, body := s.do("GET", "/admin/x/media/nope/thumb", nil, ""); resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "not_found") {
		t.Fatalf("missing: %d %s", resp.StatusCode, body)
	}
	// A contributor (content.edit, no media.upload) may preview saved values.
	c := boot(t, "contributor")
	m2, _ := mediaSvc(t, c).Upload(ctx, "c.png", bytes.NewReader(pngBytes(t, 8, 8)), media.UploadMeta{})
	if resp, _ := c.do("GET", "/admin/x/media/"+m2.ID+"/thumb", nil, ""); resp.StatusCode != 200 {
		t.Fatalf("contributor thumb: %d", resp.StatusCode)
	}

	// content.view alone is enough (the editor page needs no more); a staff
	// role with none of media.upload / content.edit / content.view gets 403.
	db := c.app.Deps.DB
	db.Exec(`INSERT INTO roles (slug, name_json, system) VALUES ('viewer', '{"en":"Viewer"}', 0), ('auditor', '{"en":"Auditor"}', 0)`)
	db.Exec(`INSERT INTO role_permissions (role, permission) VALUES ('viewer', 'content.view'), ('auditor', 'system.health')`)
	if err := auth.ReloadRoles(ctx, db); err != nil {
		t.Fatal(err)
	}
	for role, want := range map[string]int{"viewer": http.StatusOK, "auditor": http.StatusForbidden} {
		db.Exec(`UPDATE users SET role = ? WHERE email = 'u@example.com'`, role)
		resp, body := c.do("GET", "/admin/x/media/"+m2.ID+"/thumb", nil, "")
		if resp.StatusCode != want {
			t.Fatalf("%s thumb: %d %s", role, resp.StatusCode, body)
		}
		if want == http.StatusForbidden && !strings.Contains(body, `"forbidden"`) {
			t.Fatalf("auditor 403 must be the JSON envelope: %s", body)
		}
	}
}
