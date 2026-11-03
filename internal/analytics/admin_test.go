package analytics

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/theme/fake"
)

// bootAdmin is bootCollect plus api and admin (dashboard widgets, API
// mount). before runs after boot and before the server serves.
func bootAdmin(t *testing.T, before func(*Module)) *collectEnv {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: []byte("0123456789abcdef0123456789abcdef"),
		BaseURL: "http://site.test", Theme: "default"}
	mod := New()
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), fake.NewModule(),
		theme.New(theme.WithThemesDir("../../themes")), site.New(), api.New(), admin.New(), mod})
	if err != nil {
		t.Fatal(err)
	}
	if before != nil {
		before(mod)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return &collectEnv{t: t, app: app, mod: mod, srv: srv, c: newJarClient(), lang: "en"}
}

// seed stores events directly (no buffer).
func (e *collectEnv) seed(evs ...Event) {
	e.t.Helper()
	if err := e.mod.store.Insert(context.Background(), evs); err != nil {
		e.t.Fatal(err)
	}
}

func viewAt(ts time.Time, path, visitor string) Event {
	return Event{TS: ts, Kind: KindView, Path: path, Visitor: visitor, RefHost: "direct", Browser: "Chrome", OS: "macOS",
		Device: "desktop", Country: "AE", Locale: "en"}
}

// as returns a second env client signed in with role (a fresh cookie jar).
func (e *collectEnv) as(email, role string) *collectEnv {
	e.t.Helper()
	other := *e
	other.c = newJarClient()
	other.login(email, role)
	return &other
}

func (e *collectEnv) post(path string, form url.Values) (*http.Response, string) {
	e.t.Helper()
	return e.do("POST", path, strings.NewReader(form.Encode()), "Content-Type", "application/x-www-form-urlencoded")
}

// csrf reads the _csrf value of the page at path.
func (e *collectEnv) csrf(path string) string {
	e.t.Helper()
	_, page := e.get(path)
	m := csrfFieldRe.FindStringSubmatch(page)
	if m == nil {
		e.t.Fatalf("no _csrf on %s:\n%s", path, page)
	}
	return m[1]
}

func (e *collectEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.mod.store.With(context.Background(), func(d *sql.DB) error { return d.QueryRow(q, args...).Scan(&n) }); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestDashboardRequiresPermission(t *testing.T) {
	e := bootAdmin(t, nil)
	ed := e.as("ed@example.com", "editor")
	for _, p := range []string{"/admin/analytics", "/admin/analytics/now", "/admin/analytics/export.csv?table=page", "/admin/settings/analytics"} {
		if resp, _ := ed.get(p); resp.StatusCode != http.StatusForbidden {
			t.Errorf("editor %s: %d, want 403", p, resp.StatusCode)
		}
	}
	roles, _ := kernel.Get[auth.RoleService](e.app.Deps, kernel.ServiceRoles)
	if _, err := roles.Create(context.Background(), "analyst", map[string]string{"en": "Analyst", "ar": "محلل"}, []string{PermView}); err != nil {
		t.Fatal(err)
	}
	an := e.as("an@example.com", "analyst")
	resp, body := an.get("/admin/analytics")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("analyst dashboard: %d", resp.StatusCode)
	}
	if !strings.Contains(body, `href="/admin/analytics"`) {
		t.Error("nav has no analytics link for a role with analytics.view")
	}
	e.seed(viewAt(time.Now(), "/a", "v1"))
	resp, body = an.get("/admin/analytics/now")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "json") || !strings.Contains(body, `"visitors":1`) {
		t.Fatalf("now: %d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if resp, _ := an.get("/admin/settings/analytics"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("analyst settings: %d, want 403 (settings.manage)", resp.StatusCode)
	}
}

var (
	cardRe = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`(?s)data-card="` + name + `".*?data-value[^>]*>([^<]*)<.*?data-change="([a-z]+)"[^>]*>(.*?)</`)
	}
	pointsRe = regexp.MustCompile(`points="([^"]+)"`)
)

func TestDashboardShowsCardsChartTables(t *testing.T) {
	e := bootAdmin(t, nil)
	e.login("admin@example.com", "admin")
	now := time.Now().UTC()
	e.seed(viewAt(now, "/a", "v1"), viewAt(now, "/a", "v2"), viewAt(now, "/b", "v1"),
		viewAt(now, "/caf%C3%A9", "v1"),
		viewAt(now.AddDate(0, 0, -7), "/a", "v3"))
	resp, body := e.get("/admin/analytics?period=7d")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for name, want := range map[string][2]string{"visitors": {"2", "up"}, "views": {"4", "up"}} {
		m := cardRe(name).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("card %s not found", name)
		}
		if strings.TrimSpace(m[1]) != want[0] || m[2] != want[1] {
			t.Errorf("card %s = %q %q, want %v", name, m[1], m[2], want)
		}
		m[3] = html.UnescapeString(m[3])
		if !strings.Contains(m[3], "+") || !strings.Contains(m[3], "%") {
			t.Errorf("card %s change %q is not a signed percent", name, m[3])
		}
	}
	if m := cardRe("visitors").FindStringSubmatch(body); !strings.Contains(html.UnescapeString(m[3]), "+100%") {
		t.Errorf("visitors change = %q, want +100%%", m[3])
	}
	for _, want := range []string{"<svg", "<title", "<desc", `data-series`, `<bdi dir="auto">/a</bdi>`, `<bdi dir="auto">/b</bdi>`,
		`<bdi dir="auto">/café</bdi>`, `href="/admin/analytics?page=%2Fcaf%25C3%25A9&amp;period=7d"`, "analytics.js", `data-now`} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard lacks %q", want)
		}
	}
	// The hidden table holds one row per day of the 7-day series.
	tbl := regexp.MustCompile(`(?s)<table[^>]*data-series.*?</table>`).FindString(body)
	if n := strings.Count(tbl, "<tr"); n != 8 { // header + 7 days
		t.Errorf("series table rows = %d, want 8", n)
	}
	if strings.Contains(body, "data-forms") {
		t.Error("forms section shown without forms")
	}
}

func TestFilterChipsInURL(t *testing.T) {
	e := bootAdmin(t, nil)
	e.login("admin@example.com", "admin")
	now := time.Now().UTC()
	a, b := viewAt(now, "/a", "v1"), viewAt(now, "/b", "v2")
	a.RefHost, b.RefHost = "google.com", "bing.com"
	e.seed(a, b, Event{TS: now, Kind: KindSubmit, Path: "/a", Form: "contact"})
	_, body := e.get("/admin/analytics?period=7d&page=%2Fa")
	chip := regexp.MustCompile(`(?s)data-chip="page".*?</li>`).FindString(body)
	if chip == "" || !strings.Contains(chip, "/a") {
		t.Fatalf("no page chip: %q", chip)
	}
	if !strings.Contains(body, `data-remove="page" href="/admin/analytics?period=7d"`) {
		t.Error("chip has no remove link without page=")
	}
	if !strings.Contains(body, "google.com") || strings.Contains(body, "bing.com") {
		t.Error("sources table not filtered by page")
	}
	if !strings.Contains(body, "data-forms") || !strings.Contains(body, "contact") {
		t.Error("forms section missing under a page filter")
	}
	_, body = e.get("/admin/analytics?period=7d&ref=google.com")
	if !strings.Contains(body, `data-chip="ref"`) || !strings.Contains(body, "data-forms-unfiltered") {
		t.Error("ref filter: want a chip and the forms note")
	}
	if !strings.Contains(body, "Form numbers can&#39;t be filtered by source, country or device") {
		t.Error("forms note text missing")
	}
	if strings.Contains(body, `<bdi dir="auto">/b</bdi>`) {
		t.Error("pages not filtered by ref")
	}
}

func TestDashboardRTL(t *testing.T) {
	e := bootAdmin(t, nil)
	e.login("admin@example.com", "admin")
	now := time.Now().UTC()
	e.seed(viewAt(now.AddDate(0, 0, -6), "/a", "v1"), viewAt(now, "/a", "v1"))
	_, body := e.do("GET", "/admin/analytics?period=7d", nil, "Accept-Language", "ar")
	if !strings.Contains(body, `dir="rtl"`) {
		t.Fatal("not rendered in rtl")
	}
	m := pointsRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no chart line")
	}
	first, last := pointXs(t, m[1])
	if first <= last {
		t.Errorf("rtl chart: first x %v, last x %v; want the first point at the inline start (right)", first, last)
	}
}

func TestCSVExport(t *testing.T) {
	e := bootAdmin(t, nil)
	e.login("admin@example.com", "admin")
	now := time.Now().UTC()
	u := viewAt(now, "/a", "v1")
	u.UTMSource, u.UTMMedium = "=cmd", "email"
	e.seed(u, viewAt(now, "/b", "v2"), viewAt(now.AddDate(0, 0, -40), "/old", "v3"))

	rows := func(q string) [][]string {
		t.Helper()
		resp, body := e.get("/admin/analytics/export.csv?" + q)
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
			t.Fatalf("%s: %d %s", q, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
			t.Errorf("%s: not an attachment", q)
		}
		recs, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\xef\xbb\xbf"))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		return recs
	}
	keys := func(recs [][]string) string {
		var ks []string
		for _, r := range recs[1:] {
			ks = append(ks, r[0])
		}
		return strings.Join(ks, ",")
	}
	if got := keys(rows("table=page&period=7d")); got != "/a,/b" {
		t.Errorf("pages 7d = %q", got)
	}
	if got := keys(rows("table=page&period=12m")); !strings.Contains(got, "/old") {
		t.Errorf("pages 12m = %q, want /old", got)
	}
	if got := keys(rows("table=page&period=7d&page=%2Fb")); got != "/b" {
		t.Errorf("pages filtered = %q", got)
	}
	if got := keys(rows("table=utm&period=7d")); got != "'=cmd/email/" {
		t.Errorf("utm = %q, want the formula-escaped key", got)
	}
	if recs := rows("table=series&period=7d"); len(recs) != 8 {
		t.Errorf("series rows = %d, want 8", len(recs))
	}
	if h := rows("table=ref&period=7d")[0]; strings.Join(h, ",") != "key,visitors,views" {
		t.Errorf("header = %v", h)
	}
	if resp, _ := e.get("/admin/analytics/export.csv?table=nope"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown table: %d", resp.StatusCode)
	}
}

func TestWidget(t *testing.T) {
	e := bootAdmin(t, nil)
	now := time.Now().UTC()
	e.seed(viewAt(now, "/a", "v1"), viewAt(now, "/b", "v1"), viewAt(now, "/a", "v2"), viewAt(now.AddDate(0, 0, -30), "/a", "v9"))
	e.login("admin@example.com", "admin")
	_, body := e.get("/admin")
	w := regexp.MustCompile(`(?s)data-widget="analytics".*?</section>`).FindString(body)
	if w == "" {
		t.Fatal("no analytics widget on the dashboard")
	}
	for _, want := range []string{`data-stat="visitors">2<`, `data-stat="views">3<`, `href="/admin/analytics"`, "<svg"} {
		if !strings.Contains(w, want) {
			t.Errorf("widget lacks %q:\n%s", want, w)
		}
	}
	ed := e.as("ed@example.com", "editor")
	if _, body := ed.get("/admin"); strings.Contains(body, `data-widget="analytics"`) {
		t.Error("editor sees the analytics widget")
	}
}

func TestSettings(t *testing.T) {
	e := bootAdmin(t, nil)
	e.login("admin@example.com", "admin")
	ctx := context.Background()
	anon := func() string {
		c := *e
		c.c = newJarClient()
		_, body := c.get("/")
		return body
	}
	if !strings.Contains(anon(), wantTag) {
		t.Fatal("precondition: script injected by default")
	}
	_, page := e.get("/admin/settings/analytics")
	for _, want := range []string{"data-geo=\"missing\"", GeoFile, "/shopping"} {
		if !strings.Contains(page, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}

	save := func(form url.Values) *http.Response {
		form.Set("_csrf", e.csrf("/admin/settings/analytics"))
		resp, _ := e.post("/admin/settings/analytics", form)
		return resp
	}
	if resp := save(url.Values{"exclude": {"/private\r\n\r\n /shop \n"}, "backup_exclude": {"1"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", resp.StatusCode)
	}
	if got := e.app.Deps.Settings.GetString(ctx, SettingExclude, ""); got != "/private\n/shop" {
		t.Errorf("exclude = %q", got)
	}
	var on, backup bool
	_ = e.app.Deps.Settings.Get(ctx, SettingEnabled, &on)
	_ = e.app.Deps.Settings.Get(ctx, SettingBackupExclude, &backup)
	if on || !backup {
		t.Errorf("enabled=%v backup_exclude=%v, want false true", on, backup)
	}
	if strings.Contains(anon(), wantTag) {
		t.Error("tracking off still injects")
	}
	if resp := save(url.Values{"enabled": {"1"}, "exclude": {"nope"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("bad save: %d", resp.StatusCode)
	}
	if got := e.app.Deps.Settings.GetString(ctx, SettingExclude, ""); got != "/private\n/shop" {
		t.Errorf("an invalid prefix was saved: %q", got)
	}
	save(url.Values{"enabled": {"1"}})
	if !strings.Contains(anon(), wantTag) {
		t.Error("tracking back on does not inject")
	}

	// Delete before date.
	now := time.Now().UTC()
	e.seed(viewAt(now.AddDate(0, 0, -40), "/old", "v1"), viewAt(now, "/new", "v2"))
	if err := e.mod.store.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	cut := now.AddDate(0, 0, -10).Format(time.DateOnly)
	del := func(form url.Values) {
		form.Set("_csrf", e.csrf("/admin/settings/analytics"))
		if resp, _ := e.post("/admin/settings/analytics/delete", form); resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
	}
	before := e.count(`SELECT COUNT(*) FROM events`)
	del(url.Values{"mode": {"before"}, "date": {cut}, "confirm": {"nope"}})
	if n := e.count(`SELECT COUNT(*) FROM events`); n != before {
		t.Fatalf("delete without the typed confirmation removed rows: %d -> %d", before, n)
	}
	del(url.Values{"mode": {"before"}, "date": {cut}, "confirm": {"DELETE"}})
	if n := e.count(`SELECT COUNT(*) FROM events WHERE path = '/old'`); n != 0 {
		t.Errorf("old events left: %d", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM events WHERE path = '/new'`); n != 1 {
		t.Errorf("new events = %d, want 1", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM daily WHERE day < ?`, cut); n != 0 {
		t.Errorf("daily rows before the date left: %d", n)
	}
	var audits int
	e.app.Deps.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'analytics.delete'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("analytics.delete audits = %d, want 1", audits)
	}

	// Delete all keeps only today's salt.
	if _, err := e.mod.store.VisitorHash(ctx, now, "s", "1.2.3.4", "ua"); err != nil {
		t.Fatal(err)
	}
	del(url.Values{"mode": {"all"}, "confirm": {"DELETE"}})
	if n := e.count(`SELECT COUNT(*) FROM events`) + e.count(`SELECT COUNT(*) FROM daily`); n != 0 {
		t.Errorf("rows left after delete all: %d", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM salts WHERE day = ?`, dayKey(now)); n != 1 {
		t.Errorf("today's salt kept = %d, want 1", n)
	}
	e.app.Deps.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'analytics.delete'`).Scan(&audits)
	if audits != 2 {
		t.Errorf("analytics.delete audits = %d, want 2", audits)
	}
}

func TestSettingsGeoFound(t *testing.T) {
	e := bootAdmin(t, func(m *Module) { m.geo = mmdbGeo{} })
	e.login("admin@example.com", "admin")
	if _, page := e.get("/admin/settings/analytics"); !strings.Contains(page, `data-geo="found"`) {
		t.Error("GeoIP status is not found")
	}
}

func TestDashboardEmptyAfterFileLoss(t *testing.T) {
	e := bootAdmin(t, nil)
	e.login("admin@example.com", "admin")
	e.seed(viewAt(time.Now(), "/a", "v1"))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(e.mod.store.Path() + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	resp, body := e.get("/admin/analytics")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if m := cardRe("visitors").FindStringSubmatch(body); m == nil || strings.TrimSpace(m[1]) != "0" {
		t.Errorf("visitors card after file loss: %v", m)
	}
}

func TestShowAllCapped(t *testing.T) {
	e := bootAdmin(t, nil)
	e.login("admin@example.com", "admin")
	now := time.Now().UTC()
	evs := make([]Event, 0, showAllLimit+2)
	for i := range showAllLimit + 2 {
		evs = append(evs, viewAt(now, fmt.Sprintf("/p%04d", i), "v1"))
	}
	e.seed(evs...)
	panelOf := func(body string) string {
		return regexp.MustCompile(`(?s)id="t-page".*?data-tab-panel.*?</table>.*?</p>`).FindString(body)
	}
	_, body := e.get("/admin/analytics?period=7d")
	p := panelOf(body)
	if n := strings.Count(p, "<tr class=\"border-t"); n != topLimit {
		t.Errorf("default rows = %d, want %d", n, topLimit)
	}
	if !strings.Contains(p, "all=page") || strings.Contains(p, "data-show-all-hint") {
		t.Error("default view: want a Show all link and no cap hint")
	}
	_, body = e.get("/admin/analytics?period=7d&all=page")
	p = panelOf(body)
	if n := strings.Count(p, "<tr class=\"border-t"); n != showAllLimit {
		t.Errorf("show all rows = %d, want %d", n, showAllLimit)
	}
	if !strings.Contains(p, "data-capped") || !strings.Contains(p, "Download the CSV for every row") || !strings.Contains(p, "table=page") {
		t.Error("capped show all: want the hint and the CSV link")
	}
	resp, csvBody := e.get("/admin/analytics/export.csv?period=7d&table=page")
	if resp.StatusCode != 200 {
		t.Fatalf("csv: %d", resp.StatusCode)
	}
	if n := strings.Count(strings.TrimSpace(csvBody), "\n") + 1; n != showAllLimit+3 { // header + every row
		t.Errorf("csv lines = %d, want %d", n, showAllLimit+3)
	}
}
