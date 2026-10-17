package workagents

import (
	"bytes"
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func formHandlers(t *testing.T, s *service) *handlers {
	t.Helper()
	tpl, err := template.ParseFS(files, "templates/page.html")
	if err != nil {
		t.Fatal(err)
	}
	return &handlers{s: s, tpl: tpl}
}

func postForm(ctx context.Context, fn http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/agents/x", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

func TestRunFormRequiresAccountValidatesAndQueues(t *testing.T) {
	s, ctx := setup(t, nil)
	h := formHandlers(t, s)
	a, err := s.saveAgent(ctx, Agent{Name: "Form runner", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if rec := postForm(context.Background(), h.runForm, "agent_id="+a.ID+"&input=hi"); rec.Code != 401 {
		t.Fatal("anonymous run accepted", rec.Code)
	}
	if rec := postForm(ctx, h.runForm, "agent_id=%zz"); rec.Code != 422 {
		t.Fatal("malformed form not 422", rec.Code)
	}
	if rec := postForm(ctx, h.runForm, "agent_id=missing&input=hi"); rec.Code != 404 {
		t.Fatal("missing agent not 404", rec.Code)
	}
	rec := postForm(ctx, h.runForm, "agent_id="+a.ID+"&input=Queued+from+form&request_key=form-1")
	if rec.Code != 303 || rec.Header().Get("Location") != "/agents#runs" {
		t.Fatal("run form did not redirect", rec.Code, rec.Header())
	}
	runs, err := s.runs(ctx)
	if err != nil || len(runs) != 1 || runs[0].Input != "Queued from form" || runs[0].Status != "queued" {
		t.Fatal("run form did not queue", runs, err)
	}
}

func TestProfileFormCreatesUpdatesAndPreservesImportedTools(t *testing.T) {
	s, ctx := setup(t, nil)
	h := formHandlers(t, s)
	if rec := postForm(ctx, h.profileForm, "name=%zz"); rec.Code != 422 {
		t.Fatal("malformed profile form not 422", rec.Code)
	}
	if rec := postForm(ctx, h.profileForm, "name="); rec.Code != 422 {
		t.Fatal("empty profile name not 422", rec.Code)
	}
	if rec := postForm(ctx, h.profileForm, "id=missing&name=Ghost"); rec.Code != 404 {
		t.Fatal("unknown profile id not 404", rec.Code)
	}
	rec := postForm(ctx, h.profileForm, "name=Form+profile&enabled=on&execution_kind=text")
	if rec.Code != 303 || rec.Header().Get("Location") != "/agents" {
		t.Fatal("profile create failed", rec.Code, rec.Body.String())
	}
	list, err := s.agents(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "Form profile" || !list[0].Enabled {
		t.Fatal("profile not stored", list, err)
	}
	imported, err := s.saveAgent(ctx, Agent{Name: "Imported", UnsupportedTools: []string{"legacy_tool"}})
	if err != nil {
		t.Fatal(err)
	}
	if rec = postForm(ctx, h.profileForm, "id="+imported.ID+"&name=Renamed+import"); rec.Code != 303 {
		t.Fatal("profile update failed", rec.Code, rec.Body.String())
	}
	got, err := s.agent(ctx, imported.ID)
	if err != nil || got.Name != "Renamed import" || len(got.UnsupportedTools) != 1 || got.UnsupportedTools[0] != "legacy_tool" {
		t.Fatal("form edit dropped imported tool requirements", got, err)
	}
	// Anonymous profile form posts are rejected without storing anything.
	if rec = postForm(context.Background(), h.profileForm, "name=Anonymous"); rec.Code < 400 {
		t.Fatal("anonymous profile accepted", rec.Code)
	}
	var n int
	if err = s.d.DB.QueryRow(`SELECT count(*) FROM workagents_agents WHERE name='Anonymous'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("anonymous profile stored", n, err)
	}
}

func TestScheduleFormValidatesIntervalAndStoresWorkflow(t *testing.T) {
	s, ctx := setup(t, nil)
	h := formHandlers(t, s)
	a, err := s.saveAgent(ctx, Agent{Name: "Scheduled", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"interval_seconds=%zz", "interval_seconds=soon&agent_id=" + a.ID, "interval_seconds=10&agent_id=" + a.ID + "&name=Too+fast&input=x"} {
		if rec := postForm(ctx, h.scheduleForm, body); rec.Code != 422 {
			t.Fatalf("invalid schedule %q not 422: %d", body, rec.Code)
		}
	}
	rec := postForm(ctx, h.scheduleForm, "agent_id="+a.ID+"&name=Hourly&input=Report&interval_seconds=3600&enabled=on")
	if rec.Code != 303 || rec.Header().Get("Location") != "/agents#workflows" {
		t.Fatal("schedule form failed", rec.Code, rec.Body.String())
	}
	list, err := s.workflows(ctx)
	if err != nil || len(list) != 1 || list[0].IntervalSeconds != 3600 || !list[0].Enabled {
		t.Fatal("schedule not stored", list, err)
	}
	if rec = postForm(context.Background(), h.scheduleForm, "agent_id="+a.ID+"&name=Anon&input=x&interval_seconds=60"); rec.Code < 400 {
		t.Fatal("anonymous schedule accepted", rec.Code)
	}
}

func TestWorkspacePageListsOwnersForManagersAndHidesDatabaseErrors(t *testing.T) {
	s, ctx := setup(t, nil)
	h := formHandlers(t, s)
	users, _ := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	if _, err := users.Create(ctx, "assignable@example.com", "Assignable Owner", "correct horse battery", auth.RoleCustomer); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.page(rec, httptest.NewRequest("GET", "/agents", nil).WithContext(ctx))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Assignable Owner") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("manager owner list missing", rec.Code)
	}

	for _, breakSQL := range []string{
		`UPDATE workagents_agents SET unsupported_tools='{}'`,
		`DROP TABLE workagents_runs`,
		`DROP TABLE workagents_workflows`,
		`DROP TABLE workagents_artifacts`,
		`DROP TABLE workagents_approvals`,
		`ALTER TABLE users RENAME TO users_moved`,
	} {
		t.Run(breakSQL, func(t *testing.T) {
			s, ctx := setup(t, nil)
			h := formHandlers(t, s)
			if strings.HasPrefix(breakSQL, "UPDATE") {
				if _, err := s.saveAgent(ctx, Agent{Name: "Corrupt"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.d.DB.Exec(breakSQL); err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			h.page(rec, httptest.NewRequest("GET", "/agents", nil).WithContext(ctx))
			if rec.Code < 400 || strings.Contains(rec.Body.String(), "no such table") || strings.Contains(rec.Body.String(), "<html") {
				t.Fatal("broken workspace rendered or leaked", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestWorkspacePageLogsTemplateFailures(t *testing.T) {
	s, ctx := setup(t, nil)
	var logs bytes.Buffer
	s.d.Log = slog.New(slog.NewTextHandler(&logs, nil))
	h := &handlers{s: s, tpl: template.Must(template.New("broken").Parse(`{{.NoSuchField}}`))}
	rec := httptest.NewRecorder()
	h.page(rec, httptest.NewRequest("GET", "/agents", nil).WithContext(ctx))
	if !strings.Contains(logs.String(), "workagents page render failed") {
		t.Fatal("template failure not logged", logs.String())
	}
}

func TestStaticStylesheetRouteAndRegisterFailsWithoutRoles(t *testing.T) {
	_, _, app := bootWorkspace(t)
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/static/workagents.css", nil))
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") || rec.Body.Len() == 0 {
		t.Fatal("stylesheet route broken", rec.Code, rec.Header())
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	if err = New(nil).Register(&kernel.Deps{DB: database}); err == nil {
		t.Fatal("register succeeded without role permissions")
	}
}
