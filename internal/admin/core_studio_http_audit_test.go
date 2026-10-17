package admin_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

type coreStudioHTTP struct {
	s                           *site
	owner                       *auth.User
	item                        *content.Item
	path, candidate, connection string
	binding                     map[string]any
	accepts, requests           atomic.Int32
}

func coreStudioHTTPFixture(t *testing.T) *coreStudioHTTP {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.BootWithOptions(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), admin.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	s := &site{t: t, srv: srv, app: app, c: newClient()}
	f := &coreStudioHTTP{s: s}
	f.owner = s.login("studio-audit@example.invalid", "admin", "en")
	f.item = studioPost(t, s, f.owner)
	f.path = "/admin/x/studio/" + f.item.ID
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.requests.Add(1); http.Error(w, "fixture denied", 500) }))
	provider.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			f.accepts.Add(1)
		}
	}
	provider.StartTLS()
	t.Cleanup(provider.Close)
	f.connection = db.NewID()
	version := db.NewID()
	if err := app.Deps.Settings.Set(context.Background(), "connections.profile."+f.connection, map[string]any{"id": f.connection, "version": version, "name": "Private renderer fixture", "kind": "mcp", "endpoint": provider.URL}); err != nil {
		t.Fatal(err)
	}
	f.binding = map[string]any{"id": "fixture-render", "name": "Bound video renderer", "kind": "video", "connection_id": f.connection, "connection_version": version, "endpoint": provider.URL, "tool": "create", "arguments": map[string]any{"prompt": "{{prompt}}", "private_argument": "PRIVATE_BIND_SENTINEL"}, "url_path": "structuredContent.url", "job_path": "structuredContent.job", "status_tool": "check", "status_arguments": map[string]string{"id": "{{job_id}}"}, "status_path": "structuredContent.status", "ready_value": "ready", "failed_value": "failed"}
	if err := app.Deps.Settings.Set(context.Background(), "ai.studio", map[string]any{"bindings": []any{f.binding}}); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *coreStudioHTTP) seed(t *testing.T, status string) {
	t.Helper()
	f.candidate = db.NewID()
	now := time.Now().UTC().Format(time.RFC3339)
	doc := map[string]any{"user_id": f.owner.ID, "remote_job": "PRIVATE_JOB_SENTINEL", "candidate": map[string]any{"id": f.candidate, "content_id": f.item.ID, "status": status, "input": map[string]any{"provider": "fixture-render", "kind": "video", "prompt": "Reviewed fixture prompt", "text": "Private source facts", "title": "Private source title"}, "binding": f.binding, "text": "", "error": "", "created_at": now}}
	raw := mustJSON(t, doc)
	if _, err := f.s.app.Deps.DB.Exec(`INSERT INTO admin_studio_candidates(id,content_id,user_id,request_key,status,document,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, f.candidate, f.item.ID, f.owner.ID, db.NewID(), status, raw, now, now); err != nil {
		t.Fatal(err)
	}
}
func (f *coreStudioHTTP) action(t *testing.T, action string, want int) {
	t.Helper()
	code, out := studioJSON(t, f.s, f.path+"/"+f.candidate+"/action", map[string]string{"action": action}, true)
	if code != want {
		t.Fatalf("action%s: %d want%d %#v", action, code, want, out)
	}
}
func (f *coreStudioHTTP) status(t *testing.T) string {
	t.Helper()
	var status string
	if err := f.s.app.Deps.DB.QueryRow(`SELECT status FROM admin_studio_candidates WHERE id=?`, f.candidate).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}
func (f *coreStudioHTTP) unchangedContent(t *testing.T) {
	t.Helper()
	cs, ok := kernel.Get[content.Service](f.s.app.Deps, kernel.ServiceContent)
	if !ok {
		t.Fatal("content service missing")
	}
	item, err := cs.Get(context.Background(), f.item.ID)
	if err != nil || item.Body != "Original saved body" || item.Status != "draft" || item.PublishedAt != "" {
		t.Fatalf("studio altered/published original content: %+v %v", item, err)
	}
}
func (f *coreStudioHTTP) startWorker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.s.app.Deps.Jobs.Start(ctx, 1)
	t.Cleanup(func() { cancel(); f.s.app.Deps.Jobs.Wait() })
}
func (f *coreStudioHTTP) waitFailed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if f.status(t) == "failed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("candidate did not reach safe failed state")
}

func TestCoreStudioHTTPPollIsAtomicAndRevokedBindingFailsWithoutRetry(t *testing.T) {
	f := coreStudioHTTPFixture(t)
	f.seed(t, "waiting")
	action := f.path + "/" + f.candidate + "/action"
	if code, _ := studioJSON(t, f.s, action, map[string]string{"action": "check"}, false); code != 403 {
		t.Fatal("check bypassed CSRF", code)
	}
	if _, err := f.s.app.Deps.DB.Exec(`CREATE TRIGGER fixture_deny_studio_poll BEFORE INSERT ON jobs WHEN NEW.kind='studio.generate' BEGIN SELECT RAISE(ABORT,'fixture enqueue failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.action(t, "check", 500)
	var n int
	if err := f.s.app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='studio.generate'`).Scan(&n); err != nil || n != 0 || f.status(t) != "waiting" {
		t.Fatal("enqueue failure consumed waiting candidate", n, err)
	}
	if _, err := f.s.app.Deps.DB.Exec(`DROP TRIGGER fixture_deny_studio_poll`); err != nil {
		t.Fatal(err)
	}
	f.action(t, "check", 202)
	if f.status(t) != "queued" {
		t.Fatal("explicit poll not queued")
	}
	var raw string
	if err := f.s.app.Deps.DB.QueryRow(`SELECT payload_json FROM jobs WHERE kind='studio.generate'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload["id"] != f.candidate || payload["poll"] != true {
		t.Fatal("poll queued creation or wrong identity", payload, err)
	}
	f.action(t, "check", 409)
	f.action(t, "remove", 409)
	if err := f.s.app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='studio.generate'`).Scan(&n); err != nil || n != 1 {
		t.Fatal("duplicate poll enqueue", n, err)
	}
	if code, out := studioJSON(t, f.s, "/admin/settings/studio", map[string]any{"bindings": []any{}}, true); code != 200 {
		t.Fatal("binding revocation", code, out)
	}
	f.startWorker(t)
	f.waitFailed(t)
	f.action(t, "check", 409)
	response, body := f.s.do("GET", f.path, nil)
	if response.StatusCode != 200 || !strings.Contains(body, "Check the provider before explicitly generating again") || strings.Contains(body, "PRIVATE_JOB_SENTINEL") || strings.Contains(body, "PRIVATE_BIND_SENTINEL") {
		t.Fatal("failed projection leaks or loses operator evidence", response.StatusCode, body)
	}
	var jobStatus string
	if err := f.s.app.Deps.DB.QueryRow(`SELECT status FROM jobs WHERE kind='studio.generate'`).Scan(&jobStatus); err != nil || jobStatus != "done" {
		t.Fatal("uncertain failure scheduled automatic retry", jobStatus, err)
	}
	if f.accepts.Load() != 0 || f.requests.Load() != 0 {
		t.Fatal("revoked binding crossed provider boundary")
	}
	f.unchangedContent(t)
}

func TestCoreStudioHTTPProviderFailureIsSafeAndIdempotent(t *testing.T) {
	f := coreStudioHTTPFixture(t)
	fake := newFakeAI(t, 500, `{"error":{"message":"PRIVATE_PROVIDER_ECHO sk-test-SECRET-0123456789"}}`, nil)
	configureAI(t, f.s, url.Values{"provider": {"openai"}, "base_url": {fake.srv.URL}, "model": {"fixture-model"}})
	input := map[string]any{"provider": "native-text", "kind": "text", "prompt": "Failure boundary only", "text": "Unchanged source", "title": "Fixture", "reviewed": true}
	code, preview := studioJSON(t, f.s, f.path+"/preview", input, true)
	if code != 200 || fake.calls.Load() != 0 {
		t.Fatal("preview dispatched", code)
	}
	input["request_id"] = preview["request_id"]
	input["review_hash"] = preview["review_hash"]
	f.startWorker(t)
	code, out := studioJSON(t, f.s, f.path+"/generate", input, true)
	if code != 202 {
		t.Fatal(code, out)
	}
	f.candidate = out["id"].(string)
	f.waitFailed(t)
	if fake.calls.Load() != 1 {
		t.Fatal("provider failure retried", fake.calls.Load())
	}
	response, body := f.s.do("GET", f.path, nil)
	if response.StatusCode != 200 || !strings.Contains(body, "Generation or import failed") || strings.Contains(body, "PRIVATE_PROVIDER_ECHO") || strings.Contains(body, testAIKey) {
		t.Fatal("raw provider failure escaped", response.StatusCode, body)
	}
	f.action(t, "check", 409)
	f.action(t, "edit", 409)
	code, retry := studioJSON(t, f.s, f.path+"/generate", input, true)
	if code != 202 || retry["id"] != f.candidate || retry["status"] != "failed" || fake.calls.Load() != 1 {
		t.Fatal("same consent retried creation", code, retry, fake.calls.Load())
	}
	f.action(t, "unsupported", 400)
	f.unchangedContent(t)
}

func TestCoreStudioHTTPRemovalClearsPrivateStateAndOwnerIsolated(t *testing.T) {
	f := coreStudioHTTPFixture(t)
	f.seed(t, "failed")
	response, body := f.s.do("GET", f.path, nil)
	if response.StatusCode != 200 || !strings.Contains(body, "Interrupted generation") || strings.Contains(body, "PRIVATE_JOB_SENTINEL") || strings.Contains(body, "PRIVATE_BIND_SENTINEL") {
		t.Fatal("interrupted legacy state projection", response.StatusCode, body)
	}
	f.s.login("studio-other@example.invalid", "editor", "en")
	f.action(t, "remove", 404)
	response, body = f.s.do("GET", f.path, nil)
	if response.StatusCode != 200 || strings.Contains(body, f.candidate) {
		t.Fatal("another user's candidate leaked", response.StatusCode)
	}
	f.s.signIn(f.owner.Email)
	other := studioPost(t, f.s, f.owner)
	if code, _ := studioJSON(t, f.s, "/admin/x/studio/"+other.ID+"/"+f.candidate+"/action", map[string]string{"action": "remove"}, true); code != 404 {
		t.Fatal("candidate crossed content identity", code)
	}
	f.action(t, "remove", 200)
	if f.status(t) != "removed" {
		t.Fatal("candidate not removed")
	}
	var raw string
	if err := f.s.app.Deps.DB.QueryRow(`SELECT document FROM admin_studio_candidates WHERE id=?`, f.candidate).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		RemoteJob string `json:"remote_job"`
		Candidate struct {
			Text    string `json:"text"`
			Input   struct{ Text, Title, Prompt string }
			Binding any `json:"binding"`
			Native  any `json:"native"`
		} `json:"candidate"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc.RemoteJob != "" || doc.Candidate.Text != "" || doc.Candidate.Input.Text != "" || doc.Candidate.Input.Title != "" || doc.Candidate.Input.Prompt != "" || doc.Candidate.Binding != nil || doc.Candidate.Native != nil {
		t.Fatal("removed candidate retained private document", err)
	}
	response, body = f.s.do("GET", f.path, nil)
	if response.StatusCode != 200 || strings.Contains(body, f.candidate) {
		t.Fatal("removed candidate still listed")
	}
	if f.accepts.Load() != 0 || f.requests.Load() != 0 {
		t.Fatal("remove invoked provider")
	}
	f.unchangedContent(t)
}

func TestCoreStudioHTTPDiscoveryRejectsUnsafeSourcesWithoutAdvancingEvidence(t *testing.T) {
	f := coreStudioHTTPFixture(t)
	path := "/admin/settings/studio/discover"
	if code, _ := studioJSON(t, f.s, path, map[string]string{"id": f.connection}, false); code != 403 {
		t.Fatal("discovery CSRF", code)
	}
	for _, input := range []any{map[string]string{"id": "missing"}, map[string]any{"id": f.connection, "unexpected": true}} {
		if code, _ := studioJSON(t, f.s, path, input, true); code != 400 {
			t.Fatal("invalid discovery source", code)
		}
	}
	cached := map[string]any{"version": "old-version", "tools": []any{map[string]any{"name": "previous-tool", "inputSchema": map[string]string{"type": "object"}}}}
	key := "ai.studio.discovery." + f.connection
	if err := f.s.app.Deps.Settings.Set(context.Background(), key, cached); err != nil {
		t.Fatal(err)
	}
	if code, out := studioJSON(t, f.s, path, map[string]string{"id": f.connection}, true); code != 502 || strings.Contains(string(mustJSON(t, out)), "PRIVATE_") {
		t.Fatal("unsafe discovery", code, out)
	}
	var after map[string]any
	if err := f.s.app.Deps.Settings.Get(context.Background(), key, &after); err != nil || string(mustJSON(t, after)) != string(mustJSON(t, cached)) {
		t.Fatal("failed discovery replaced evidence", err)
	}
	if f.accepts.Load() != 0 || f.requests.Load() != 0 {
		t.Fatal("private discovery opened TCP/HTTP")
	}
	f.unchangedContent(t)
}
