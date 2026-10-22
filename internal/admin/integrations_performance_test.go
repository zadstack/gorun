package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/observe"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// Saved inventory is deliberately populated directly: GET must render even
// when Cloudflare is unavailable, and must never start provider work itself.
func integrationsPerformanceFixture(t testing.TB, resources int, identities bool) (*handlers, *http.Request, *atomic.Int64) {
	t.Helper()
	app, err := kernel.BootWithOptions(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}, []kernel.Module{settingsmod.New(), auth.New(), New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	svc, _ := kernel.Get[*observe.Service](app.Deps, observe.ServiceName)
	tpl, _ := kernel.Get[*httpx.Templates](app.Deps, kernel.ServiceAdminTemplates)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	mail, _ := kernel.Get[*mailer.Mailer](app.Deps, mailer.ServiceName)
	h := &handlers{d: app.Deps, tpl: tpl, users: users, observe: svc, mail: mail}
	ctx := context.Background()
	cipher, err := db.Encrypt(app.Deps.Config.SecretKey, []byte(fakeCFToken), cfTokenAAD)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{settingCFToken: cipher, settingCFTokenHint: "ghij", settingCFAccount: fakeCFAccount} {
		if err := app.Deps.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	save := func(kind, key, revision string, data any) {
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.Deps.DB.Exec(`INSERT INTO observations(kind,key,success_at,success_revision,success_scope,result_json,updated_at) VALUES(?,?,?,?,?,?,?)`, kind, key, now, revision, "discovery", string(raw), now); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range cfInventoryKinds {
		items := make([]map[string]any, resources)
		for i := range items {
			name := fmt.Sprintf("%s-%04d", kind, i)
			items[i] = map[string]any{"id": name, "name": name, "location": "WEUR", "tables": 7, "size_bytes": 4096}
		}
		save(kindCFInventory, kind, snapshot.revision(true), map[string]any{"kind": kind, "items": items, "count": resources, "complete": true})
	}
	if identities {
		for i := 0; i < resources; i++ {
			key := fmt.Sprintf("workers-%04d", i)
			rev, err := h.cfEdgeRevision(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			save(kindCFEdge, key, rev, map[string]any{"site_id": key, "origin": "https://example.com", "manager_id": key})
		}
	}
	calls := &atomic.Int64{}
	previous := cfNewClient
	cfNewClient = func() *http.Client { calls.Add(1); panic("integrations GET attempted a Cloudflare request") }
	t.Cleanup(func() { cfNewClient = previous })
	ctx = auth.WithUser(ctx, &auth.User{ID: "performance-fixture", Role: "admin", Status: "active", Locale: "en"})
	req := httptest.NewRequest(http.MethodGet, "/admin/settings/integrations", nil).WithContext(i18n.WithLocale(ctx, "en"))
	return h, req, calls
}

func TestIntegrationsLargeSavedInventoryRendersWithoutProviderRequests(t *testing.T) {
	h, req, calls := integrationsPerformanceFixture(t, 1000, true)
	w := httptest.NewRecorder()
	h.integrationsGet(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("saved inventory: status %d", w.Code)
	}
	body := w.Body.String()
	for _, name := range []string{"r2-0000", "d1-0000", "workers-0000"} {
		if !strings.Contains(body, name) {
			t.Errorf("missing first-page resource %s", name)
		}
	}
	if strings.Contains(body, "r2-0999") {
		t.Error("first page rendered final resource")
	}
	if len(body) > 500_000 {
		t.Errorf("first-page HTML is unbounded: %d bytes", len(body))
	}
	query := url.Values{}
	for _, kind := range cfInventoryKinds {
		query.Set("inv_page_"+kind, "10")
	}
	last := req.Clone(req.Context())
	last.URL.RawQuery = query.Encode()
	lastPage := httptest.NewRecorder()
	h.integrationsGet(lastPage, last)
	if lastPage.Code != http.StatusOK {
		t.Fatalf("last page: %d", lastPage.Code)
	}
	for _, name := range []string{"r2-0999", "d1-0999", "workers-0999"} {
		if !strings.Contains(lastPage.Body.String(), name) {
			t.Errorf("missing last-page resource %s", name)
		}
	}
	search := req.Clone(req.Context())
	search.URL.RawQuery = url.Values{"inv_q_r2": {"r2-0999"}}.Encode()
	searchPage := httptest.NewRecorder()
	h.integrationsGet(searchPage, search)
	if searchPage.Code != http.StatusOK || !strings.Contains(searchPage.Body.String(), "r2-0999") || strings.Contains(searchPage.Body.String(), `data-use-bucket="r2-0000"`) {
		t.Error("search did not find resource beyond first page")
	}
	if strings.Contains(body, fakeCFToken) {
		t.Fatal("Cloudflare token leaked")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("GET constructed %d provider clients", got)
	}
	var queued int
	if err := h.d.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind LIKE 'observe.integrations.cloudflare.%'`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("GET queued provider work: %d (%v)", queued, err)
	}
}

// Measures handler+SQLite+template cost (not network transport or login).
// No hard timing threshold: use the same host and benchtime for comparisons.
func BenchmarkIntegrationsGETSavedInventory(b *testing.B) {
	for _, scenario := range []struct {
		name       string
		count      int
		identities bool
	}{{"empty", 0, false}, {"100_per_kind", 100, false}, {"1000_per_kind", 1000, false}, {"1000_per_kind_with_identities", 1000, true}} {
		b.Run(scenario.name, func(b *testing.B) {
			h, req, calls := integrationsPerformanceFixture(b, scenario.count, scenario.identities)
			warm := httptest.NewRecorder()
			h.integrationsGet(warm, req)
			if warm.Code != http.StatusOK {
				b.Fatalf("warm GET: %d", warm.Code)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				w := httptest.NewRecorder()
				h.integrationsGet(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("GET: %d", w.Code)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(warm.Body.Len()), "response_bytes")
			if calls.Load() != 0 {
				b.Fatal("GET contacted Cloudflare")
			}
		})
	}
}

// Separates saved-data loading from HTML rendering, particularly revision
// checks for accounts with many previously identified Workers.
func BenchmarkIntegrationsReportSavedInventory(b *testing.B) {
	for _, scenario := range []struct {
		name       string
		count      int
		identities bool
	}{{"empty", 0, false}, {"1000_per_kind", 1000, false}, {"1000_per_kind_with_identities", 1000, true}} {
		b.Run(scenario.name, func(b *testing.B) {
			h, req, calls := integrationsPerformanceFixture(b, scenario.count, scenario.identities)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				report, err := h.integrationsReport(req.Context())
				if err != nil {
					b.Fatal(err)
				}
				if len(report.Inventory) != len(cfInventoryKinds) {
					b.Fatal("incomplete report")
				}
			}
			b.StopTimer()
			if calls.Load() != 0 {
				b.Fatal("report contacted Cloudflare")
			}
		})
	}
}
