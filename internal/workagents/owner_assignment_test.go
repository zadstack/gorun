package workagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// This verifies ownership through real booted API middleware, not direct SQL reassignment.
func TestAdminCreatesExplicitOwnedProfileAndCustomerRuns(t *testing.T) {
	s, adminCtx, app := bootWorkspace(t)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	create := func(email, name string) *auth.User {
		u, e := users.Create(context.Background(), email, name, "correct horse battery", auth.RoleCustomer)
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	customer := create("assigned-customer@example.test", "Assigned Customer")
	other := create("cross-owner@example.test", "Other Customer")
	disabled := create("disabled-owner@example.test", "Disabled Customer")
	if _, e := app.Deps.DB.Exec(`UPDATE users SET status='disabled' WHERE id=?`, disabled.ID); e != nil {
		t.Fatal(e)
	}
	key := func(u *auth.User, scopes []string) string {
		token, _, e := keys.Create(adminCtx, "Owner assignment test", u.ID, scopes, 60, "")
		if e != nil {
			t.Fatal(e)
		}
		return token
	}
	adminKey := key(auth.UserFromContext(adminCtx), []string{"workagents.manage", "workagents.read"})
	customerKey := key(customer, []string{"workagents.run", "workagents.read"})
	otherKey := key(other, []string{"workagents.run", "workagents.read"})
	call := func(method, path string, payload any, key string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(payload)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		return w
	}
	response := call("POST", "/api/v1/workagents/agents", Agent{OwnerID: customer.ID, Name: "Assigned customer agent", Enabled: true}, adminKey)
	if response.Code != 200 {
		t.Fatalf("admin create %d %s", response.Code, response.Body.String())
	}
	var result struct{ Data Agent }
	if e := json.Unmarshal(response.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.Data.OwnerID != customer.ID || result.Data.ID == "" {
		t.Fatalf("ownership ignored: %+v", result.Data)
	}
	response = call("POST", "/api/v1/workagents/agents/"+result.Data.ID+"/run", map[string]string{"input": "Read-only customer acceptance", "request_key": "owner-assignment-run"}, customerKey)
	if response.Code != 202 {
		t.Fatalf("owned run %d %s", response.Code, response.Body.String())
	}
	response = call("POST", "/api/v1/workagents/agents/"+result.Data.ID+"/run", map[string]string{"input": "Cross owner"}, otherKey)
	if response.Code != 404 {
		t.Fatalf("cross owner %d", response.Code)
	}
	response = call("POST", "/api/v1/workagents/agents", Agent{OwnerID: customer.ID, Name: "Customer escalation", Enabled: true}, customerKey)
	if response.Code != 403 {
		t.Fatalf("customer create %d", response.Code)
	}
	for _, id := range []string{disabled.ID, "nonexistent-owner"} {
		response = call("POST", "/api/v1/workagents/agents", Agent{OwnerID: id, Name: "Invalid owner"}, adminKey)
		if response.Code != 422 {
			t.Fatalf("invalid owner %s %d %s", id, response.Code, response.Body.String())
		}
	}
	changed := result.Data
	changed.OwnerID = other.ID
	response = call("POST", "/api/v1/workagents/agents", changed, adminKey)
	if response.Code != 422 {
		t.Fatalf("immutable owner %d", response.Code)
	}
	after, e := s.agent(adminCtx, result.Data.ID)
	if e != nil || after.OwnerID != customer.ID {
		t.Fatal(after, e)
	}
	defaultAgent, e := s.saveAgent(adminCtx, Agent{Name: "Admin default"})
	if e != nil || defaultAgent.OwnerID != auth.UserFromContext(adminCtx).ID {
		t.Fatal(defaultAgent, e)
	}
	after.SourceID = "immutable-source"
	if _, e = app.Deps.DB.Exec(`UPDATE workagents_agents SET source_id=? WHERE id=?`, after.SourceID, after.ID); e != nil {
		t.Fatal(e)
	}
	after.OwnerID = ""
	after.Name = "Renamed owned profile"
	saved, e := s.saveAgent(adminCtx, after)
	if e != nil || saved.OwnerID != customer.ID || saved.SourceID != "immutable-source" {
		t.Fatal(saved, e)
	}
	after.OwnerID = other.ID
	if _, e = s.saveAgent(adminCtx, after); !errors.Is(e, errInvalid) {
		t.Fatal(e)
	}
}

func TestPerOwnerPendingCapacityConcurrentReplayAndResume(t *testing.T) {
	s, ctx := setup(t, func(context.Context, *kernel.Deps, string, string, string, string) (string, error) {
		return "completed result", nil
	})
	a, e := s.saveAgent(ctx, Agent{Name: "Bounded queue", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.d.DB.Exec(`INSERT INTO workagents_runs(id,owner_id,agent_id,status,historical,created_at)VALUES('old-queue',?,?,'queued',1,'')`, a.OwnerID, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	successful := map[string]string{}
	busy := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("bounded-request-%d", i)
			id, e := s.run(ctx, a.ID, "same input", key)
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				successful[key] = id
			} else if errors.Is(e, errBusy) {
				busy++
			} else {
				t.Errorf("unexpected queue failure %v", e)
			}
		}(i)
	}
	wg.Wait()
	if len(successful) != 3 || busy != 9 {
		t.Fatalf("cap accepted=%d busy=%d", len(successful), busy)
	}
	var chosenKey, chosenID string
	for k, id := range successful {
		chosenKey, chosenID = k, id
		break
	}
	again, e := s.run(ctx, a.ID, "same input", chosenKey)
	if e != nil || again != chosenID {
		t.Fatalf("replay at capacity: %s %v", again, e)
	}
	if _, e = s.run(ctx, a.ID, "changed input", chosenKey); !errors.Is(e, errConflict) {
		t.Fatal(e)
	}
	if _, e = s.d.DB.Exec(`UPDATE workagents_runs SET status='running' WHERE id=?`, chosenID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.run(ctx, a.ID, "new", "full-while-running"); !errors.Is(e, errBusy) {
		t.Fatal(e)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": chosenID})
	if e = s.execute(context.Background(), payload); e != nil {
		t.Fatal(e)
	}
	if _, e = s.run(ctx, a.ID, "resumed", "capacity-resumed-request"); e != nil {
		t.Fatal(e)
	}
	var n int
	s.d.DB.QueryRow(`SELECT count(*) FROM workagents_runs WHERE historical=0 AND status IN('queued','running')`).Scan(&n)
	if n != 3 {
		t.Fatal(n)
	}
	response := httptest.NewRecorder()
	(&handlers{s: s}).failure(response, errBusy)
	if response.Code != 429 || !strings.Contains(response.Body.String(), "queue is full") {
		t.Fatal(response.Code, response.Body.String())
	}
}
func TestSchedulerDefersFullOwnerAndConfiguredCapacityClamps(t *testing.T) {
	s, ctx := setup(t, nil)
	a, e := s.saveAgent(ctx, Agent{Name: "Scheduled queue", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Migrate(ctx, s.d.DB, "settings", settingsmod.New().Migrations()); e != nil {
		t.Fatal(e)
	}
	s.d.Settings = settings.New(s.d.DB)
	for value, want := range map[int]int{-2: 1, 2: 2, 25: 20} {
		if e = s.d.Settings.Set(ctx, "workagents.max_pending_per_owner", value); e != nil {
			t.Fatal(e)
		}
		if got := s.pendingLimit(ctx); got != want {
			t.Fatalf("limit %d: %d", value, got)
		}
	}
	if e = s.d.Settings.Set(ctx, "workagents.max_pending_per_owner", 1); e != nil {
		t.Fatal(e)
	}
	if _, e = s.run(ctx, a.ID, "queued", "one-pending-request"); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	wf, e := s.saveWorkflow(ctx, Workflow{AgentID: a.ID, Name: "Deferred workflow", Input: "scheduled input", Enabled: true, IntervalSeconds: 60})
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(61 * time.Second)
	if e = s.schedule(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	s.d.DB.QueryRow(`SELECT count(*) FROM workagents_runs WHERE workflow_id=?`, wf.ID).Scan(&n)
	if n != 0 {
		t.Fatal("full owner scheduled", n)
	}
	s.d.DB.Exec(`UPDATE workagents_runs SET status='done' WHERE workflow_id='' AND historical=0`)
	if e = s.schedule(ctx); e != nil {
		t.Fatal(e)
	}
	s.d.DB.QueryRow(`SELECT count(*) FROM workagents_runs WHERE workflow_id=?`, wf.ID).Scan(&n)
	if n != 1 {
		t.Fatal("schedule did not resume", n)
	}
}
func TestWorkerPlainContextRetainsOwnerPermissionsForDraft(t *testing.T) {
	s, adminCtx, app := bootWorkspace(t)
	a, e := s.saveAgent(adminCtx, Agent{Name: "Draft worker", ExecutionKind: "content_draft", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	_, e = app.Deps.DB.Exec(`INSERT INTO workagents_runs(id,owner_id,agent_id,input,created_at)VALUES('worker-permissions',?,?,?,'')`, a.OwnerID, a.ID, `{"type":"post","title":"Worker draft","body":"Private draft"}`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.execute(context.Background(), json.RawMessage(`{"run_id":"worker-permissions"}`)); e != nil {
		t.Fatal(e)
	}
	var status string
	app.Deps.DB.QueryRow(`SELECT status FROM workagents_runs WHERE id='worker-permissions'`).Scan(&status)
	if status != "done" {
		t.Fatal(status)
	}
	native, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	draft, e := native.GetBySlug(adminCtx, "post", "agent-draft-worker-permissions", "ar")
	if e != nil || draft.Status != content.Status("draft") {
		t.Fatal(draft, e)
	}
}
