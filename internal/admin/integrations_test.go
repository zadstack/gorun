package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/observe"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

const (
	fakeCFToken   = "cf-token-TESTONLY-0123456789abcdefghij"
	fakeCFAccount = "0123456789abcdef0123456789abcdef"
	fakeCFOther   = "fedcba9876543210fedcba9876543210"
)

// fakeCloudflare is an httptest stand-in for the Cloudflare v4 API with
// page-numbered and cursor-based listings, per-scope failure injection
// and a request log. No real account is involved.
type fakeCloudflare struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	requests []string
	// status per path prefix overrides the success answer.
	fail   map[string]int
	r2     []string
	d1     int
	worker []string
	calls  atomic.Int64
}

func newFakeCloudflare(t *testing.T) *fakeCloudflare {
	t.Helper()
	f := &fakeCloudflare{t: t, fail: map[string]int{}, r2: []string{"media-bucket", "backups-bucket"}, d1: 3, worker: []string{"edge-worker", "other-worker"}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	prevBase, prevClient, prevPages := cfAPIBase, cfNewClient, cfMaxPages
	cfAPIBase = f.srv.URL
	cfNewClient = func() *http.Client {
		return httpx.NewSafeClientOpts(httpx.SafeClientOptions{AllowPrivate: true, Timeout: 5 * time.Second})
	}
	t.Cleanup(func() { cfAPIBase, cfNewClient, cfMaxPages = prevBase, prevClient, prevPages })
	return f
}

func (f *fakeCloudflare) handle(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+fakeCFToken {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"result":null}`)
		return
	}
	for prefix, status := range f.fail {
		if strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(status)
			code := 0
			if status == http.StatusForbidden || status == http.StatusUnauthorized {
				code = 10000
			}
			fmt.Fprintf(w, `{"success":false,"errors":[{"code":%d,"message":"injected failure for %s"}],"result":null}`, code, prefix)
			return
		}
	}
	ok := func(result any, info any) {
		b, _ := json.Marshal(map[string]any{"success": true, "errors": []any{}, "result": result, "result_info": info})
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	switch {
	case r.URL.Path == "/user/tokens/verify":
		ok(map[string]any{"id": "tok", "status": "active", "expires_on": "2027-01-01T00:00:00Z"}, nil)
	case r.URL.Path == "/accounts":
		ok([]map[string]any{{"id": fakeCFAccount, "name": "Demo account"}, {"id": fakeCFOther, "name": "Second <b>account</b>"}}, map[string]any{"page": 1, "per_page": perPage, "count": 2, "total_count": 2})
	case strings.HasSuffix(r.URL.Path, "/r2/buckets"):
		// Cursor-based: one bucket per page.
		cursor, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		var buckets []map[string]any
		next := ""
		if cursor < len(f.r2) {
			buckets = append(buckets, map[string]any{"name": f.r2[cursor], "creation_date": "2026-01-02T03:04:05Z", "location": "WEUR", "storage_class": "Standard"})
			if cursor+1 < len(f.r2) {
				next = strconv.Itoa(cursor + 1)
			}
		}
		ok(map[string]any{"buckets": buckets}, map[string]any{"cursor": next, "per_page": perPage})
	case strings.HasSuffix(r.URL.Path, "/d1/database"):
		// Page-numbered: two databases per page.
		if page < 1 {
			page = 1
		}
		size := 2
		if perPage == 1 {
			size = 1
		}
		var list []map[string]any
		for i := (page - 1) * size; i < page*size && i < f.d1; i++ {
			list = append(list, map[string]any{"uuid": fmt.Sprintf("uuid-%d", i), "name": fmt.Sprintf("db-%d", i), "created_at": "2026-01-01T00:00:00Z", "version": "production", "num_tables": i + 1, "file_size": 1024 * (i + 1)})
		}
		ok(list, map[string]any{"page": page, "per_page": size, "count": len(list), "total_count": f.d1})
	case strings.HasSuffix(r.URL.Path, "/workers/scripts"):
		var list []map[string]any
		for _, name := range f.worker {
			list = append(list, map[string]any{"id": name, "created_on": "2026-01-01T00:00:00Z", "modified_on": "2026-02-01T00:00:00Z"})
		}
		ok(list, nil)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":7003,"message":"no route"}],"result":null}`)
	}
}

func (f *fakeCloudflare) seen(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.Contains(r, substr) {
			n++
		}
	}
	return n
}

// integrationsHandlers boots the admin module without job workers so
// refreshes can be driven deterministically with runObserveJobs.
func integrationsHandlers(t *testing.T) (*handlers, *kernel.App) {
	t.Helper()
	app, err := kernel.BootWithOptions(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"},
		[]kernel.Module{settingsmod.New(), auth.New(), New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	svc, ok := kernel.Get[*observe.Service](app.Deps, observe.ServiceName)
	if !ok {
		t.Fatal("observe service missing")
	}
	tpl, _ := kernel.Get[*httpx.Templates](app.Deps, kernel.ServiceAdminTemplates)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	m, _ := kernel.Get[*mailer.Mailer](app.Deps, mailer.ServiceName)
	h := &handlers{d: app.Deps, tpl: tpl, users: users, observe: svc, mail: m}
	return h, app
}

// runObserveJobs runs every queued observe.* job once, like a worker.
func runObserveJobs(t *testing.T, app *kernel.App) {
	t.Helper()
	for i := 0; i < 20; i++ {
		ran, err := app.Deps.Jobs.RunOne(context.Background())
		if err != nil {
			t.Fatalf("job: %v", err)
		}
		if !ran {
			return
		}
	}
}

func storeCloudflare(t *testing.T, h *handlers, token, account string) {
	t.Helper()
	ctx := context.Background()
	enc, err := db.Encrypt(h.d.Config.SecretKey, []byte(token), cfTokenAAD)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{settingCFToken: enc, settingCFTokenHint: token[len(token)-4:], settingCFAccount: account, settingCFAccountName: ""} {
		if err := h.d.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloudflareScopesAndInventoryWithFake(t *testing.T) {
	f := newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)

	refs := h.queueCloudflareChecks(ctx)
	if len(refs) != 2 {
		t.Fatalf("queued %d checks", len(refs))
	}
	// A second request while the first is queued is deduplicated.
	again := h.queueCloudflareChecks(ctx)
	if len(again) != 2 || !again[0].Deduped || !again[1].Deduped {
		t.Fatalf("second request was not deduplicated: %+v", again)
	}
	for _, kind := range cfInventoryKinds {
		if _, err := h.observe.Refresh(ctx, kindCFInventory, kind); err != nil {
			t.Fatal(err)
		}
	}
	runObserveJobs(t, app)

	report, err := h.integrationsReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cf := report.Cloudflare
	if !cf.Configured || cf.TokenHint != "ghij" || cf.AccountID != fakeCFAccount {
		t.Fatalf("connection view: %+v", cf)
	}
	if len(cf.Accounts) != 2 || !cf.AccountsFull || cf.Accounts[1].Name != "Second baccount/b" {
		t.Fatalf("accounts: %+v", cf.Accounts)
	}
	for _, s := range cf.Scopes {
		if s.Status != "passed" {
			t.Fatalf("scope %s: %+v", s.ID, s)
		}
	}
	byKind := map[string]inventoryView{}
	for _, iv := range report.Inventory {
		byKind[iv.Kind] = iv
	}
	if r2 := byKind["r2"]; r2.Count != 2 || !r2.Complete || r2.Items[0].Name != "backups-bucket" {
		t.Fatalf("r2: %+v", r2)
	}
	if d1 := byKind["d1"]; d1.Count != 3 || !d1.Complete || d1.Items[2].Tables != 3 {
		t.Fatalf("d1: %+v", d1)
	}
	if w := byKind["workers"]; w.Count != 2 || !w.Complete || w.Items[0].Checked != "none" || len(w.Items[0].Bound) != 0 {
		t.Fatalf("workers: %+v", w)
	}
	if f.seen("cursor=1") != 1 || f.seen("/d1/database?page=2") != 1 {
		t.Fatalf("pagination was not followed: %v", f.requests)
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), fakeCFToken) {
		t.Fatal("report leaked the token")
	}
	// The job rows and observation rows never hold the token either.
	var leaked int
	if err := app.Deps.DB.QueryRow(`SELECT count(*) FROM observations WHERE result_json LIKE ? OR attempt_error LIKE ?`, "%"+fakeCFToken+"%", "%"+fakeCFToken+"%").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("observation rows leaked the token: %d %v", leaked, err)
	}
}

func TestCloudflareScopeLampsClassifyFailures(t *testing.T) {
	f := newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	f.fail["/accounts/"+fakeCFAccount+"/d1"] = http.StatusForbidden
	f.fail["/accounts/"+fakeCFAccount+"/workers"] = http.StatusBadGateway
	h.queueCloudflareChecks(ctx)
	runObserveJobs(t, app)
	report, err := h.integrationsReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"token": "passed", "accounts": "passed", "r2": "passed", "d1": "incomplete", "workers": "could_not_verify"}
	for _, s := range report.Cloudflare.Scopes {
		if s.Status != want[s.ID] {
			t.Fatalf("scope %s = %s, want %s (%s)", s.ID, s.Status, want[s.ID], s.Error)
		}
		if s.Status != "passed" && (s.Repair == "" || s.Error == "") {
			t.Fatalf("failed scope without repair/error: %+v", s)
		}
	}
	// Without an account the per-service scopes are not checked, not guessed.
	if err := h.d.Settings.Set(ctx, settingCFAccount, ""); err != nil {
		t.Fatal(err)
	}
	h.queueCloudflareChecks(ctx)
	runObserveJobs(t, app)
	report, _ = h.integrationsReport(ctx)
	for _, s := range report.Cloudflare.Scopes {
		if (s.ID == "r2" || s.ID == "d1" || s.ID == "workers") && s.Status != "not_checked" {
			t.Fatalf("scope %s without account = %s", s.ID, s.Status)
		}
	}
	// A wrong token fails the token lamp as incomplete and keeps nothing.
	storeCloudflare(t, h, "wrong-token-TESTONLY-0123456789abcdefghij", fakeCFAccount)
	h.queueCloudflareChecks(ctx)
	runObserveJobs(t, app)
	report, _ = h.integrationsReport(ctx)
	if report.Cloudflare.Scopes[0].Status != "incomplete" || report.Cloudflare.ScopesMeta.Status != "failed" {
		t.Fatalf("wrong token: %+v %+v", report.Cloudflare.Scopes[0], report.Cloudflare.ScopesMeta)
	}
}

func TestCloudflareInventoryIncompleteBeyondPageCap(t *testing.T) {
	f := newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	f.d1 = 10
	cfMaxPages = 2
	if _, err := h.observe.Refresh(ctx, kindCFInventory, "d1"); err != nil {
		t.Fatal(err)
	}
	runObserveJobs(t, app)
	report, _ := h.integrationsReport(ctx)
	for _, iv := range report.Inventory {
		if iv.Kind == "d1" && (iv.Complete || iv.Count != 4 || iv.Meta.Status != "fresh") {
			t.Fatalf("capped inventory: %+v", iv)
		}
	}
}

func TestCloudflareRevisionFencesSavedResults(t *testing.T) {
	newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	if _, err := h.observe.Refresh(ctx, kindCFInventory, "r2"); err != nil {
		t.Fatal(err)
	}
	runObserveJobs(t, app)
	// Changing the account makes the saved inventory stale: it is for
	// another revision and is reported so, never as the new account's.
	if err := h.d.Settings.Set(ctx, settingCFAccount, fakeCFOther); err != nil {
		t.Fatal(err)
	}
	report, _ := h.integrationsReport(ctx)
	for _, iv := range report.Inventory {
		if iv.Kind == "r2" && (iv.Meta.Status != "stale" || iv.Meta.RevisionMatch == nil || *iv.Meta.RevisionMatch) {
			t.Fatalf("stale inventory not reported: %+v", iv.Meta)
		}
	}
}

func TestCloudflareSavePreservesPreviousConfiguration(t *testing.T) {
	newFakeCloudflare(t)
	h, _ := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	before, _ := h.d.Settings.All(ctx, "integrations.")
	snapshot, _ := h.cloudflareSnapshot(ctx)
	for _, in := range []cloudflareInput{{Token: "short"}, {Token: "has space in it which is invalid 0123456789"}, {AccountID: "not-hex"}} {
		vals, fields, _ := h.cloudflareValues(ctx, in, snapshot)
		if len(fields) == 0 {
			t.Fatalf("accepted %+v: %+v", in, vals)
		}
	}
	after, _ := h.d.Settings.All(ctx, "integrations.")
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("invalid input changed settings")
	}
	// Compare-and-swap: a concurrent change rejects the write.
	vals, fields, _ := h.cloudflareValues(ctx, cloudflareInput{AccountID: fakeCFOther}, snapshot)
	if len(fields) != 0 {
		t.Fatal(fields)
	}
	if err := h.d.Settings.Set(ctx, settingCFAccount, fakeCFOther); err != nil {
		t.Fatal(err)
	}
	if err := h.saveCloudflare(ctx, snapshot, vals); err != errCFConflict {
		t.Fatalf("conflict not detected: %v", err)
	}
	// Blank token keeps the stored one; clear removes everything.
	snapshot, _ = h.cloudflareSnapshot(ctx)
	vals, fields, replaced := h.cloudflareValues(ctx, cloudflareInput{AccountID: fakeCFAccount}, snapshot)
	if len(fields) != 0 || replaced || vals[settingCFToken] != nil {
		t.Fatalf("blank token replaced the stored token: %+v %+v", vals, fields)
	}
	if err := h.saveCloudflare(ctx, snapshot, vals); err != nil {
		t.Fatal(err)
	}
	if h.d.Settings.GetString(ctx, settingCFToken, "") != snapshot.Cipher {
		t.Fatal("token changed")
	}
	snapshot, _ = h.cloudflareSnapshot(ctx)
	vals, _, _ = h.cloudflareValues(ctx, cloudflareInput{Clear: true}, snapshot)
	if err := h.saveCloudflare(ctx, snapshot, vals); err != nil {
		t.Fatal(err)
	}
	if s, _ := h.cloudflareSnapshot(ctx); s.configured() || s.AccountID != "" {
		t.Fatalf("clear left %+v", s)
	}
}

func TestWorkerIdentityNeedsManagerContract(t *testing.T) {
	newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	const key = "edge-manager-key-value-0123456789ABCDEFGHIJ"
	edge := &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/manager" {
			return jsonResponse(404, `{"ok":false}`), nil
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			return jsonResponse(401, `{"ok":false}`), nil
		}
		if r.URL.Host == "ordinary.example" {
			return jsonResponse(200, `<html>not gorun</html>`), nil
		}
		return jsonResponse(200, `{"ok":true,"data":{"site_id":"demo","manager_id":"manager-1","label":"Demo manager","primary":true}}`), nil
	})}
	// A Worker named like GoRun is not identified by its name.
	if _, err := h.identifyWorker(ctx, edge, "gorun-edge", "https://ordinary.example", key); err == nil {
		t.Fatal("ordinary origin identified as GoRun Edge")
	}
	if _, err := h.identifyWorker(ctx, edge, "edge-worker", "https://10.0.0.5", key); err != errEdgeOrigin {
		t.Fatalf("private origin accepted: %v", err)
	}
	if _, err := h.identifyWorker(ctx, edge, "edge-worker", "https://edge.example", "wrong-key"); err != errEdgeCheck {
		t.Fatalf("malformed key accepted: %v", err)
	}
	id, err := h.identifyWorker(ctx, edge, "edge-worker", "https://edge.example", key)
	if err != nil || id.SiteID != "demo" || id.ManagerID != "manager-1" {
		t.Fatalf("identity: %+v %v", id, err)
	}
	o, err := h.observe.Get(ctx, kindCFEdge, "edge-worker")
	if err != nil || o.Saved == nil || o.Saved.Scope != "identity" {
		t.Fatalf("identity not recorded: %+v %v", o, err)
	}
	if strings.Contains(string(o.Saved.Data), key) {
		t.Fatal("manager key stored")
	}
	var meta string
	if err := h.d.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'integrations.worker_identify' ORDER BY id DESC LIMIT 1`).Scan(&meta); err != nil || strings.Contains(meta, key) {
		t.Fatalf("audit: %q %v", meta, err)
	}
	// Bound once the Edge settings point at the identified origin.
	if err := h.d.Settings.Set(ctx, settingEdgeOrigin, "https://edge.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.observe.Refresh(ctx, kindCFInventory, "workers"); err != nil {
		t.Fatal(err)
	}
	runObserveJobs(t, app)
	report, _ := h.integrationsReport(ctx)
	for _, iv := range report.Inventory {
		if iv.Kind != "workers" {
			continue
		}
		for _, item := range iv.Items {
			switch item.Name {
			case "edge-worker":
				if item.Checked != "passed" || item.Identity == nil || len(item.Bound) != 1 || item.Bound[0] != "edge" {
					t.Fatalf("identified worker: %+v", item)
				}
			default:
				if item.Checked != "none" || len(item.Bound) != 0 {
					t.Fatalf("ordinary worker promoted: %+v", item)
				}
			}
		}
	}
	// A changed account makes the identity evidence stale, not current.
	if err := h.d.Settings.Set(ctx, settingCFAccount, fakeCFOther); err != nil {
		t.Fatal(err)
	}
	if o, _ := h.observe.Get(ctx, kindCFEdge, "edge-worker"); o.Status != "stale" {
		t.Fatalf("identity after account change: %s", o.Status)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
