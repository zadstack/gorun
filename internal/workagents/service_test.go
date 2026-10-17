package workagents

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func setup(t *testing.T, complete Completion) (*service, context.Context) {
	t.Helper()
	ctx := context.Background()
	database, e := db.Open(filepath.Join(t.TempDir(), "agents.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { database.Close() })
	for _, m := range []kernel.Module{auth.New(), New(complete)} {
		if e = db.Migrate(ctx, database, m.Name(), m.Migrations()); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.Migrate(ctx, database, "jobs", jobs.Migrations()); e != nil {
		t.Fatal(e)
	}
	if _, e = database.Exec(`INSERT OR IGNORE INTO role_permissions(role,permission)VALUES('admin','*')`); e != nil {
		t.Fatal(e)
	}
	if e = auth.ReloadRoles(ctx, database); e != nil {
		t.Fatal(e)
	}
	users := auth.NewService(database, nil)
	u, e := users.Create(ctx, "operator@example.com", "Operator", "correct horse battery", auth.RoleAdmin)
	if e != nil {
		t.Fatal(e)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := &kernel.Deps{DB: database, Log: log, Services: kernel.NewServiceRegistry()}
	d.Jobs = jobs.New(database, log)
	d.Services.Set(kernel.ServiceAuth, users)
	s := &service{d: d, complete: complete, now: time.Now}
	d.Jobs.Register(jobKind, s.execute, 1)
	return s, auth.WithUser(ctx, u)
}

func TestQueuedRunPersistsRealProviderResultAndDeduplicates(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		if json.NewDecoder(r.Body).Decode(&in) != nil || in["input"] != "Analyse test content" || in["system"] != "Test editor" {
			http.Error(w, "invalid provider input", 422)
			return
		}
		io.WriteString(w, `{"result":"Verified provider response"}`)
	}))
	defer provider.Close()
	complete := func(ctx context.Context, _ *kernel.Deps, _, _, system, input string) (string, error) {
		body, _ := json.Marshal(map[string]string{"system": system, "input": input})
		req, _ := http.NewRequestWithContext(ctx, "POST", provider.URL, strings.NewReader(string(body)))
		resp, e := provider.Client().Do(req)
		if e != nil {
			return "", e
		}
		defer resp.Body.Close()
		var out map[string]string
		e = json.NewDecoder(resp.Body).Decode(&out)
		return out["result"], e
	}
	s, ctx := setup(t, complete)
	a, e := s.saveAgent(ctx, Agent{Name: "Editor", SystemPrompt: "Test editor", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.run(ctx, a.ID, "Analyse test content", "test-idempotency")
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.run(ctx, a.ID, "Analyse test content", "test-idempotency")
	if e != nil || id != again {
		t.Fatal("repeat request duplicated", e)
	}
	if _, e = s.run(ctx, a.ID, "Different input", "test-idempotency"); !errors.Is(e, errConflict) {
		t.Fatal("changed request accepted", e)
	}
	worker, cancel := context.WithCancel(context.Background())
	s.d.Jobs.Start(worker, 1)
	defer func() { cancel(); s.d.Jobs.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs, e := s.runs(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if len(runs) == 1 && runs[0].Status == "done" {
			if runs[0].Result != "Verified provider response" || runs[0].FinishedAt == "" {
				t.Fatal("provider result not persisted", runs)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("queued provider job did not finish")
}

func TestUnsupportedToolsAndCrossOwnerIsolation(t *testing.T) {
	s, ctx := setup(t, nil)
	a, e := s.saveAgent(ctx, Agent{Name: "Unsupported", Enabled: true, UnsupportedTools: []string{"write_wordpress"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.run(ctx, a.ID, "Do not run", ""); !errors.Is(e, errUnavailable) {
		t.Fatal(e)
	}
	other := auth.WithUser(context.Background(), &auth.User{ID: "other", Role: auth.RoleCustomer, Status: auth.StatusActive})
	if _, e = s.agent(other, a.ID); !errors.Is(e, errNotFound) {
		t.Fatal("cross owner agent visible", e)
	}
	list, e := s.agents(other)
	if e != nil || len(list) != 0 {
		t.Fatal("cross owner list visible", list, e)
	}
	if _, e = s.run(other, a.ID, "Forbidden", ""); !errors.Is(e, errNotFound) {
		t.Fatal("cross owner run accepted", e)
	}
}

func TestUnconfiguredProviderNeverReportsSuccess(t *testing.T) {
	s, ctx := setup(t, nil)
	a, e := s.saveAgent(ctx, Agent{Name: "No provider", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.run(ctx, a.ID, "Test failure", "")
	if e != nil {
		t.Fatal(e)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": id})
	if e = s.execute(ctx, payload); e == nil {
		t.Fatal("unconfigured provider succeeded")
	}
	runs, e := s.runs(ctx)
	if e != nil || len(runs) != 1 || runs[0].Status != "failed" || runs[0].ErrorCode != "ai_unconfigured" || runs[0].Result != "" {
		t.Fatal(runs, e)
	}
}

func TestPersistentSchedulesDeduplicateTicksAndHistoricalRunsNeverReplay(t *testing.T) {
	s, ctx := setup(t, func(context.Context, *kernel.Deps, string, string, string, string) (string, error) {
		return "scheduled result", nil
	})
	a, e := s.saveAgent(ctx, Agent{Name: "Scheduled", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	w, e := s.saveWorkflow(ctx, Workflow{AgentID: a.ID, Name: "Periodic", Input: "Scheduled input", Enabled: true, IntervalSeconds: 60})
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(61 * time.Second)
	if e = s.schedule(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.schedule(ctx); e != nil {
		t.Fatal(e)
	}
	runs, e := s.runs(ctx)
	if e != nil || len(runs) != 1 || runs[0].WorkflowID != w.ID {
		t.Fatal("duplicate schedule tick", runs, e)
	}
	p, _ := json.Marshal(map[string]string{"run_id": runs[0].ID})
	if e = s.execute(ctx, p); e != nil {
		t.Fatal(e)
	}
	if _, e = s.d.DB.Exec(`INSERT INTO workagents_runs(id,owner_id,agent_id,status,historical,created_at)VALUES('history',?,?,'done',1,?)`, a.OwnerID, a.ID, db.FormatTime(now)); e != nil {
		t.Fatal(e)
	}
	if e = s.execute(ctx, json.RawMessage(`{"run_id":"history"}`)); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = s.d.DB.QueryRow(`SELECT count(*) FROM workagents_artifacts WHERE run_id='history'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("historical run replayed", count, e)
	}
	w.Enabled = false
	if _, e = s.saveWorkflow(ctx, w); e != nil {
		t.Fatal(e)
	}
	now = now.Add(2 * time.Minute)
	if e = s.schedule(ctx); e != nil {
		t.Fatal(e)
	}
	var total int
	s.d.DB.QueryRow(`SELECT count(*) FROM workagents_runs`).Scan(&total)
	if total != 2 {
		t.Fatal("disabled schedule ran", total)
	}
}

func TestWorkspacePageRequiresLoginAndEscapesArtifacts(t *testing.T) {
	s, ctx := setup(t, nil)
	a, e := s.saveAgent(ctx, Agent{Name: "Visible profile", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.d.DB.Exec(`INSERT INTO workagents_artifacts(id,owner_id,name,content,historical,created_at)VALUES('imported-artifact',?,'Historical artifact','<script>alert(1)</script>',1,?)`, a.OwnerID, db.Now()); e != nil {
		t.Fatal(e)
	}
	tpl, e := template.ParseFS(files, "templates/page.html")
	if e != nil {
		t.Fatal(e)
	}
	h := &handlers{s: s, tpl: tpl}
	anonymous := httptest.NewRecorder()
	h.page(anonymous, httptest.NewRequest("GET", "/agents", nil))
	if anonymous.Header().Get("Location") != "/account?next=/agents" {
		t.Fatal("agents guest must use native password-capable account login")
	}
	if anonymous.Code != 303 {
		t.Fatal("anonymous workspace exposed", anonymous.Code)
	}
	response := httptest.NewRecorder()
	h.page(response, httptest.NewRequest("GET", "/agents", nil).WithContext(ctx))
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, "Visible profile") || strings.Contains(body, "<script>alert(1)</script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("workspace rendering/escaping failed")
	}
	other := auth.WithUser(context.Background(), &auth.User{ID: "another-owner", Role: auth.RoleCustomer, Status: auth.StatusActive})
	response = httptest.NewRecorder()
	h.page(response, httptest.NewRequest("GET", "/agents", nil).WithContext(other))
	if strings.Contains(response.Body.String(), "Visible profile") || strings.Contains(response.Body.String(), "Historical artifact") {
		t.Fatal("workspace exposed another user's records")
	}
}

func bootWorkspace(t *testing.T) (*service, context.Context, *kernel.App) {
	t.Helper()
	app, e := kernel.BootWithOptions(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), BaseURL: "http://localhost", SecretKey: make([]byte, 32)}, []kernel.Module{settingsmod.New(), auth.New(), content.New(), api.New(), admin.New(), New(admin.CompleteWorkAgent)}, kernel.BootOptions{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(app.Close)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, e := users.Create(context.Background(), "workspace-admin@example.com", "Workspace admin", "correct horse battery", auth.RoleAdmin)
	if e != nil {
		t.Fatal(e)
	}
	s, ok := kernel.Get[*service](app.Deps, "workagents")
	if !ok {
		t.Fatal("workagents service not registered")
	}
	return s, auth.WithUser(context.Background(), u), app
}

func TestActualModuleBootCatalogAPIAndHealth(t *testing.T) {
	s, ctx, app := bootWorkspace(t)
	if app.Deps.I18n.T("ar", "workagents.title") != "الإيجنتس والفلوهات" {
		t.Fatal("catalog namespace not loaded")
	}
	a, e := s.saveAgent(ctx, Agent{Name: "Needs adapter", UnsupportedTools: []string{"unknown_tool"}})
	if e != nil {
		t.Fatal(e)
	}
	response := httptest.NewRecorder()
	app.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/agents", nil).WithContext(ctx))
	if response.Code != 200 || !strings.Contains(response.Body.String(), a.Name) {
		t.Fatal("booted owner workspace not rendered", response.Code)
	}
	response = httptest.NewRecorder()
	app.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/workagents/agents", nil).WithContext(ctx))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Needs adapter") {
		t.Fatal("booted API missing", response.Code, response.Body.String())
	}
	checks := []admin.HealthCheck{}
	if _, e = app.Deps.Hooks.Apply(ctx, admin.HealthCheckFilter, &checks); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, check := range checks {
		if check.Name == "أدوات الإيجنتس المنقولة" && check.Status == admin.CheckWarn {
			found = true
		}
	}
	if !found {
		t.Fatal("unsupported adapters not in health")
	}
}

func TestContentDeliveryCreatesSanitizedDraftAndRejectsCustomer(t *testing.T) {
	s, ctx, app := bootWorkspace(t)
	result, e := s.contentDraft(ctx, "bounded-run", `{"type":"post","title":"Agent draft","body":"<p>Safe content</p><script>bad()</script>"}`)
	if e != nil {
		t.Fatal(e)
	}
	var out map[string]string
	if json.Unmarshal([]byte(result), &out) != nil || out["status"] != "draft" {
		t.Fatal("content tool did not create draft", result)
	}
	svc, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	it, e := svc.Get(ctx, out["content_id"])
	if e != nil || it.Status != content.Status("draft") || strings.Contains(it.Body, "<script>") {
		t.Fatal("unsafe or published agent content", it, e)
	}
	again, e := s.contentDraft(ctx, "bounded-run", `{"type":"post","title":"Agent draft","body":"Same run"}`)
	if e != nil || again != result {
		t.Fatal("restart duplicated draft", again, e)
	}
	customer := auth.WithUser(context.Background(), &auth.User{ID: "customer", Role: auth.RoleCustomer, Status: auth.StatusActive})
	if _, e = s.contentDraft(customer, "forbidden-run", `{"type":"post","title":"Not allowed","body":"Forbidden"}`); e != toolError("content_permission_denied") {
		t.Fatal("customer created draft", e)
	}
	if _, e = s.contentDraft(ctx, "bad-run", `{"type":"post","title":"Publish request","status":"published"}`); e != toolError("invalid_tool_input") {
		t.Fatal("publish input accepted", e)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGitHubToolFixedReadOnlyEndpointAndBoundedInput(t *testing.T) {
	s, _ := setup(t, nil)
	calls := 0
	s.githubClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.String() != "https://api.github.com/repos/octocat/Hello-World" || r.Header.Get("Authorization") != "" {
			t.Fatal("tool sent an arbitrary request", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"full_name":"octocat/Hello-World","stargazers_count":7,"forks_count":1,"open_issues_count":2}`)), Header: make(http.Header)}, nil
	})}
	result, e := s.githubStars(context.Background(), `{"repository":"octocat/Hello-World"}`)
	if e != nil || !strings.Contains(result, `"stargazers_count":7`) {
		t.Fatal(result, e)
	}
	for _, input := range []string{`{"repository":"https://evil.invalid"}`, `{"repository":"octocat/../secret"}`, `{"repository":"octocat/Hello-World","url":"https://evil.invalid"}`, `{"repository":"octocat/Hello-World"} {}`} {
		if _, e = s.githubStars(context.Background(), input); e == nil {
			t.Fatal("arbitrary GitHub input accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid input reached network", calls)
	}
}

func TestBootedCustomerAPIUsesMigratedPermissionsAndOwnerIsolation(t *testing.T) {
	s, adminCtx, app := bootWorkspace(t)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	customer, e := users.Create(context.Background(), "owned-agent@example.com", "Customer", "correct horse battery", auth.RoleCustomer)
	if e != nil {
		t.Fatal(e)
	}
	other, e := users.Create(context.Background(), "other-agent@example.com", "Other", "correct horse battery", auth.RoleCustomer)
	if e != nil {
		t.Fatal(e)
	}
	mine, e := s.saveAgent(adminCtx, Agent{Name: "Customer agent", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := s.saveAgent(adminCtx, Agent{Name: "Other customer agent", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.d.DB.Exec(`UPDATE workagents_agents SET owner_id=? WHERE id=?`, customer.ID, mine.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.d.DB.Exec(`UPDATE workagents_agents SET owner_id=? WHERE id=?`, other.ID, foreign.ID); e != nil {
		t.Fatal(e)
	}
	token, _, e := keys.Create(adminCtx, "Customer test key", customer.ID, []string{"workagents.read", "workagents.run", "workagents.manage"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.Handler.ServeHTTP(rec, req)
		return rec
	}
	response := call("GET", "/api/v1/workagents/agents", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), mine.ID) || strings.Contains(response.Body.String(), foreign.ID) {
		t.Fatal("customer grant/isolation failed", response.Code, response.Body.String())
	}
	response = call("POST", "/api/v1/workagents/agents/"+mine.ID+"/run", `{"input":"Owned request","request_key":"owner-acceptance"}`)
	if response.Code != 202 {
		t.Fatal("customer cannot run owned profile", response.Code, response.Body.String())
	}
	response = call("POST", "/api/v1/workagents/agents/"+foreign.ID+"/run", `{"input":"Cross-owner request"}`)
	if response.Code != 404 {
		t.Fatal("cross-owner run did not return404", response.Code)
	}
	response = call("POST", "/api/v1/workagents/agents", `{"name":"Escalation attempt","enabled":true}`)
	if response.Code != 403 {
		t.Fatal("customer gained management", response.Code)
	}
	response = call("POST", "/api/v1/workagents/workflows", `{"agent_id":"`+mine.ID+`","name":"Forbidden schedule","input":"Request","interval_seconds":60,"enabled":true}`)
	if response.Code != 403 {
		t.Fatal("customer gained schedule management", response.Code)
	}
	empty, e := users.Create(context.Background(), "no-agents@example.com", "No agents", "correct horse battery", auth.RoleCustomer)
	if e != nil {
		t.Fatal(e)
	}
	token, _, e = keys.Create(adminCtx, "Empty customer key", empty.ID, []string{"workagents.read"}, 60, "")
	if e != nil {
		t.Fatal(e)
	}
	response = call("GET", "/api/v1/workagents/agents", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"data":[]`) {
		t.Fatal("empty customer list not200empty", response.Code, response.Body.String())
	}
}
