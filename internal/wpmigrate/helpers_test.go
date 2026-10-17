package wpmigrate

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

const (
	fixtureUser = "sampleadmin"
	fixturePass = "abcd efgh ijkl mnop qrst uvwx"
)

// fixtureRoute is one routes.json entry.
type fixtureRoute struct {
	File    string            `json:"file"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	dir     string
}

// fixtureServer replays the fictional Example Press REST responses (and the
// synthetic authenticated overlay) by canonical path+query, rewriting the
// example.org host to itself. Uploads are served as tiny valid files.
type fixtureServer struct {
	t      *testing.T
	srv    *httptest.Server
	routes map[string]fixtureRoute
	authed map[string]fixtureRoute

	mu        sync.Mutex
	overrides map[string]func([]byte) []byte
	failPaths map[string]int
	hits      map[string]int
	uploads   map[string][]byte
}

func newFixtureServer(t *testing.T) *fixtureServer {
	t.Helper()
	fs := &fixtureServer{t: t, routes: loadRoutes(t, "testdata/example-press"), authed: loadRoutes(t, "testdata/example-press-auth-synthetic"),
		overrides: map[string]func([]byte) []byte{}, failPaths: map[string]int{}, hits: map[string]int{}, uploads: map[string][]byte{}}
	// The old recording contains published rows only. Give its native
	// content/media collections explicit edit routes; permission denial is
	// covered by separate live-controller and transport tests.
	for key, rt := range fs.routes {
		if strings.Contains(key, "context=") || !(strings.HasPrefix(key, "/wp-json/wp/v2/posts") || strings.HasPrefix(key, "/wp-json/wp/v2/media")) {
			continue
		}
		u, _ := url.Parse(key)
		q := u.Query()
		q.Set("context", "edit")
		u.RawQuery = q.Encode()
		fs.authed[canonicalKey(u)] = rt
	}
	fs.srv = httptest.NewServer(fs)
	t.Cleanup(fs.srv.Close)
	return fs
}

func loadRoutes(t *testing.T, dir string) map[string]fixtureRoute {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]fixtureRoute
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for k, r := range m {
		r.dir = dir
		m[k] = r
	}
	return m
}

func (f *fixtureServer) URL() string { return f.srv.URL }

// override transforms the body served for a canonical key.
func (f *fixtureServer) override(key string, fn func([]byte) []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides[key] = fn
}

// fail makes a path answer status (0 clears).
func (f *fixtureServer) fail(p string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if status == 0 {
		delete(f.failPaths, p)
		return
	}
	f.failPaths[p] = status
}

// uploadBytes serves data for an upload path (nil restores the default).
func (f *fixtureServer) uploadBytes(p string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if data == nil {
		delete(f.uploads, p)
		return
	}
	f.uploads[p] = data
}

func (f *fixtureServer) hitCount(p string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[p]
}

// canonicalKey mirrors tools/wp-record.CanonicalKey.
func canonicalKey(u *url.URL) string {
	q := u.Query()
	q.Del("per_page")
	q.Del("_embed")
	q.Del("_fields")
	q.Del("status")
	if q.Get("orderby") == "id" && q.Get("order") == "asc" {
		q.Del("orderby")
		q.Del("order")
	}
	if q.Get("page") == "1" {
		q.Del("page")
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	if len(parts) == 0 {
		return u.Path
	}
	return u.Path + "?" + strings.Join(parts, "&")
}

func (f *fixtureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	failStatus := f.failPaths[r.URL.Path]
	f.mu.Unlock()
	if failStatus != 0 {
		http.Error(w, "fixture failure", failStatus)
		return
	}
	authed := false
	if user, pass, ok := r.BasicAuth(); ok {
		if user != fixtureUser || pass != fixturePass {
			writeJSONStatus(w, http.StatusUnauthorized, `{"code":"incorrect_password","message":"The provided password is an invalid application password.","data":{"status":401}}`)
			return
		}
		authed = true
	}
	if strings.HasPrefix(r.URL.Path, "/wp-content/uploads/") {
		f.mu.Lock()
		data, ok := f.uploads[r.URL.Path]
		f.mu.Unlock()
		if ok {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
			return
		}
		serveUpload(w, r)
		return
	}
	key := canonicalKey(r.URL)
	rt, ok := f.authed[key]
	if !ok || !authed {
		rt, ok = f.routes[key]
	}
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, `{"code":"rest_no_route","message":"No route","data":{"status":404}}`)
		return
	}
	body, err := os.ReadFile(filepath.Join(rt.dir, rt.File))
	if err != nil {
		f.t.Errorf("fixture %s: %v", key, err)
		http.Error(w, err.Error(), 500)
		return
	}
	host := f.srv.URL
	body = bytes.ReplaceAll(body, []byte("https://example.org"), []byte(host))
	body = bytes.ReplaceAll(body, []byte(`https:\/\/example.org`), []byte(strings.ReplaceAll(host, "/", `\/`)))
	f.mu.Lock()
	fn := f.overrides[key]
	if fn == nil {
		u := *r.URL
		q := u.Query()
		q.Del("context")
		u.RawQuery = q.Encode()
		fn = f.overrides[canonicalKey(&u)]
	}
	f.mu.Unlock()
	if fn != nil {
		body = fn(body)
	}
	for k, v := range rt.Headers {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	}
	status := rt.Status
	if status == 0 {
		status = 200
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func writeJSONStatus(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// serveUpload answers with a tiny valid file of the extension's type.
func serveUpload(w http.ResponseWriter, r *http.Request) {
	var data []byte
	switch strings.ToLower(path.Ext(r.URL.Path)) {
	case ".png", ".jpg", ".jpeg":
		data = tinyPNG()
	case ".gif":
		var buf bytes.Buffer
		img := image.NewPaletted(image.Rect(0, 0, 2, 2), []color.Color{color.White, color.Black})
		_ = gif.Encode(&buf, img, nil)
		data = buf.Bytes()
	case ".svg":
		data = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><rect width="2" height="2"/></svg>`)
	case ".pdf":
		data = []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")
	case ".zip":
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		fw, _ := zw.Create("a.txt")
		_, _ = fw.Write([]byte("a"))
		_ = zw.Close()
		data = buf.Bytes()
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Length", strconvI(len(data)))
	w.WriteHeader(200)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func strconvI(n int) string { return strconv.Itoa(n) }

func tinyPNG() []byte {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// limitMedia serves only the first n media items of page 1 and an empty
// page 2 (keeps pipeline tests fast; recorded files stay untouched).
func (f *fixtureServer) limitMedia(n int, mimes ...string) {
	filter := func(body []byte) []byte {
		var list []map[string]any
		if json.Unmarshal(body, &list) != nil {
			return body
		}
		var out []map[string]any
		for _, m := range list {
			mt, _ := m["mime_type"].(string)
			if len(mimes) > 0 && !contains(mimes, mt) {
				continue
			}
			if len(out) < n {
				out = append(out, m)
			}
		}
		b, _ := json.Marshal(out)
		return b
	}
	f.override("/wp-json/wp/v2/media", filter)
	f.override("/wp-json/wp/v2/media?page=2", func([]byte) []byte { return []byte("[]") })
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// env is a booted GoRun with wpmigrate and a synchronous pipeline.
type env struct {
	t   *testing.T
	app *kernel.App
	svc *service // runs jobs on q
	q   *inlineQueue
	wp  *fixtureServer
	srv *httptest.Server
	adm *auth.User
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t)
}

// newEnvWith boots the migrator stack plus extra modules that boot between
// admin and wpmigrate (e.g. commerce.New()).
func newEnvWith(t *testing.T, extra ...kernel.Module) *env {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: bytes.Repeat([]byte{7}, 32),
		BaseURL: "", AllowPrivateNet: true, Theme: "default"}
	mods := []kernel.Module{settingsmod.New(), auth.New(), content.New(), taxonomy.New(), media.New(), menus.New(),
		redirects.New(), seo.New(), theme.New(theme.WithThemesDir("../../themes")), site.New(), api.New(), admin.New()}
	mods = append(append(mods, extra...), New())
	app, err := kernel.Boot(context.Background(), cfg, mods)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	svc, ok := kernel.Get[Service](app.Deps, ServiceName)
	if !ok {
		t.Fatal("wpmigrate service not registered")
	}
	q := newInlineQueue()
	run := q.bind(svc.(*service))
	run.clientBackoff = 0
	// Outbound rate: unthrottled in tests (ratelimit_test covers the bucket).
	if err := app.Deps.Settings.Set(context.Background(), SettingRatePerSecond, maxRatePerSecond); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, app: app, svc: run, q: q, srv: srv}
	// Render checks go through the real handler (site + theme).
	run.check = func(ctx context.Context, p string) int {
		req := httptest.NewRequest(http.MethodGet, (&url.URL{Path: p}).EscapedPath(), nil)
		rec := httptest.NewRecorder()
		app.Handler.ServeHTTP(rec, req)
		return rec.Code
	}
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if e.adm, err = users.Create(context.Background(), "owner@example.com", "Owner", "correct horse battery", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) ctx() context.Context { return context.Background() }

// restMigration creates a REST source on the fixture server (auth when
// asked) and a migration, and scans it.
func (e *env) restMigration(withAuth bool, opts Options) string {
	e.t.Helper()
	if e.wp == nil {
		e.wp = newFixtureServer(e.t)
	}
	in := SourceInput{Name: "example-press", URL: e.wp.URL()}
	if withAuth {
		in.Username, in.AppPassword = fixtureUser, fixturePass
	}
	src, err := e.svc.CreateSource(e.ctx(), in)
	if err != nil {
		e.t.Fatal(err)
	}
	m, err := e.svc.CreateMigration(e.ctx(), src.ID, opts, e.adm.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.Discover(e.ctx(), src.ID); err != nil {
		e.t.Fatal(err)
	}
	return m.ID
}

// wxrMigration imports testdata/wxr/sample.xml rewritten to point at the
// fixture server (for attachments).
func (e *env) wxrMigration(opts Options) string {
	e.t.Helper()
	if e.wp == nil {
		e.wp = newFixtureServer(e.t)
	}
	raw, err := os.ReadFile("testdata/wxr/sample.xml")
	if err != nil {
		e.t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("https://example.org"), []byte(e.wp.URL()))
	p := filepath.Join(e.t.TempDir(), "export.xml")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		e.t.Fatal(err)
	}
	src, err := e.svc.createWXRSource(e.ctx(), "export.xml", p)
	if err != nil {
		e.t.Fatal(err)
	}
	m, err := e.svc.CreateMigration(e.ctx(), src.ID, opts, e.adm.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.Discover(e.ctx(), src.ID); err != nil {
		e.t.Fatal(err)
	}
	return m.ID
}

// run starts the migration and drains every job.
func (e *env) run(id string) *Status {
	e.t.Helper()
	if err := e.svc.Start(e.ctx(), id); err != nil {
		e.t.Fatal(err)
	}
	return e.drain(id)
}

func (e *env) drain(id string) *Status {
	e.t.Helper()
	if _, err := e.q.drain(e.ctx(), 0); err != nil {
		e.t.Fatal(err)
	}
	st, err := e.svc.Status(e.ctx(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e *env) contentSvc() content.Service {
	c, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	return c
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.app.Deps.DB.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) failedItems(id string) []itemRow {
	list, _ := e.svc.items(e.ctx(), id, "", ItemFailed)
	return list
}

func (e *env) item(id, kind, wpType, wpID string) *itemRow {
	e.t.Helper()
	it, err := scanItem(e.app.Deps.DB.QueryRow(`SELECT `+itemColumns+` FROM migration_items WHERE migration_id = ? AND kind = ?
		AND wp_type = ? AND wp_id = ?`, id, kind, wpType, wpID))
	if err != nil {
		e.t.Fatalf("item %s/%s/%s: %v", kind, wpType, wpID, err)
	}
	return it
}
