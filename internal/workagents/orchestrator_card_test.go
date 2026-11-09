package workagents

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// seedCardData gives the viewing admin one pending approval, one run finished
// today and the seeded example orchestrator profile.
func seedCardData(t *testing.T, s *service, ctx context.Context) {
	t.Helper()
	worker, err := s.saveAgent(ctx, Agent{Name: "Card worker", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedExampleOrchestratorAgents(ctx, s.d, func() string { return db.FormatTime(time.Now()) }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.d.DB.Exec(`INSERT INTO workagents_approvals(id,owner_id,name,status,historical,created_at)VALUES('card-approval',?,'Card approval','pending',0,?)`, owner(ctx), db.FormatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := s.d.DB.Exec(`INSERT INTO workagents_runs(id,owner_id,agent_id,status,finished_at,created_at)VALUES('card-run',?,?,'done',?,?)`, owner(ctx), worker.ID, db.FormatTime(time.Now()), db.FormatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
}

func TestOrchestratorCardEndpointCountsAndCaching(t *testing.T) {
	s, ctx, app := bootWorkspace(t)
	seedCardData(t, s, ctx)
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/workagents/orchestrator-card", nil).WithContext(ctx))
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatal("orchestrator card not rendered", rec.Code, body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("orchestrator card must not be cached", rec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(body, "المنسّق") || !strings.Contains(body, `href="/admin/workagents"`) {
		t.Fatal("card name or workspace link missing", body)
	}
	if !strings.Contains(body, "1 بانتظار موافقتك") || !strings.Contains(body, "<b>1</b>") {
		t.Fatal("pending approval count missing", body)
	}
	if !strings.Contains(body, "1 مكتملة اليوم") {
		t.Fatal("runs completed today count missing", body)
	}
	if !strings.Contains(body, "ساكن") {
		t.Fatal("idle tag missing", body)
	}
}

func TestOrchestratorCardFallsBackWhenOrchestratorMissing(t *testing.T) {
	_, ctx, app := bootWorkspace(t)
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/workagents/orchestrator-card", nil).WithContext(ctx))
	if rec.Code != 200 {
		t.Fatal("card without seed failed", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "فعّل المنسّق") {
		t.Fatal("enable hint missing", rec.Body.String())
	}
}

func TestOrchestratorCardRequiresReadPermission(t *testing.T) {
	_, adminCtx, app := bootWorkspace(t)
	roles, _ := kernel.Get[auth.RoleService](app.Deps, kernel.ServiceRoles)
	permissions := []string{"content.view"}
	if _, err := roles.Update(adminCtx, auth.RoleCustomer, auth.RolePatch{Permissions: &permissions}); err != nil {
		t.Fatal(err)
	}
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	user, err := users.Create(context.Background(), "no-card@example.com", "No card", "correct horse battery", auth.RoleCustomer)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/workagents/orchestrator-card", nil).WithContext(auth.WithUser(context.Background(), user)))
	if rec.Code != 403 {
		t.Fatal("user without workagents.read reached the card", rec.Code)
	}
}

func TestSidebarEmitsOrchestratorContainerWithPermission(t *testing.T) {
	_, adminCtx, app := bootWorkspace(t)
	tpl, ok := kernel.Get[*httpx.Templates](app.Deps, kernel.ServiceAdminTemplates)
	if !ok {
		t.Fatal("admin templates service missing")
	}
	rec := httptest.NewRecorder()
	tpl.RenderPartial(rec, httptest.NewRequest("GET", "/admin", nil).WithContext(adminCtx), "partials/sidebar", nil)
	body := rec.Body.String()
	if !strings.Contains(body, `data-orchestrator-card hx-get="/admin/workagents/orchestrator-card" hx-trigger="load" hx-swap="innerHTML"`) {
		t.Fatal("orchestrator card container missing", body)
	}
	if !strings.Contains(body, `href="/static/admin/orchestrator-card.css`) {
		t.Fatal("card stylesheet link missing", body)
	}
	// Without workagents.read the htmx container must not be emitted at all.
	roles, _ := kernel.Get[auth.RoleService](app.Deps, kernel.ServiceRoles)
	permissions := []string{"content.view"}
	if _, err := roles.Update(adminCtx, auth.RoleCustomer, auth.RolePatch{Permissions: &permissions}); err != nil {
		t.Fatal(err)
	}
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	user, err := users.Create(context.Background(), "no-sidebar-card@example.com", "No sidebar card", "correct horse battery", auth.RoleCustomer)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	tpl.RenderPartial(rec, httptest.NewRequest("GET", "/admin", nil).WithContext(auth.WithUser(context.Background(), user)), "partials/sidebar", nil)
	if strings.Contains(rec.Body.String(), "data-orchestrator-card") {
		t.Fatal("container leaked to a user without workagents.read", rec.Body.String())
	}
}
