package workagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestLinearFlowPausesPersistsAndResumesExactApprovedOutput(t *testing.T) {
	calls := 0
	s, ctx := setup(t, func(_ context.Context, _ *kernel.Deps, _, _, _, input string) (string, error) {
		calls++
		return input + " reviewed", nil
	})
	a, err := s.saveAgent(ctx, Agent{Name: "Writer", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p := LinearPlan{Name: "Reviewed text", Input: "Verified source facts", RequestKey: "acceptance", Steps: []LinearStep{{Kind: "agent", AgentID: a.ID}, {Kind: "approval"}, {Kind: "agent", AgentID: a.ID}, {Kind: "approval"}}}
	f, err := s.startLinearFlow(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.startLinearFlow(ctx, p)
	if err != nil || replay.ID != f.ID {
		t.Fatal("replay duplicated", replay, err)
	}
	changed := p
	changed.Input = "Changed"
	if _, err = s.startLinearFlow(ctx, changed); !errors.Is(err, errConflict) {
		t.Fatal("request conflict ignored", err)
	}
	tick := func() {
		t.Helper()
		if e := s.tickLinearFlows(ctx); e != nil {
			t.Fatal(e)
		}
	}
	finish := func() {
		t.Helper()
		runs, e := s.runs(ctx)
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range runs {
			if r.Status == "queued" {
				b, _ := json.Marshal(map[string]string{"run_id": r.ID})
				if e = s.execute(ctx, b); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	tick()
	tick()
	finish()
	tick()
	tick()
	f, err = s.linearFlow(ctx, f.ID)
	if err != nil || f.Status != "awaiting_approval" || f.Step != 1 || calls != 1 {
		t.Fatalf("not paused: %#v calls%d %v", f, calls, err)
	}
	for i := 0; i < 3; i++ {
		tick()
		finish()
	}
	if calls != 1 {
		t.Fatal("provider ran across unapproved gate")
	}
	// Wrong output hash and wrong stage must not approve current result.
	if _, err = s.decideLinearFlow(ctx, f.ID, LinearDecision{Approve: true, Step: 1, ResultHash: "stale"}); !errors.Is(err, errConflict) {
		t.Fatal("stale hash accepted", err)
	}
	if _, err = s.decideLinearFlow(ctx, f.ID, LinearDecision{Approve: true, Step: 3, ResultHash: f.ResultHash}); !errors.Is(err, errConflict) {
		t.Fatal("future gate accepted", err)
	}
	users, _ := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	other, e := users.Create(ctx, "other@example.com", "Other", "correct horse battery", auth.RoleAdmin)
	if e != nil {
		t.Fatal(e)
	}
	if _, err = s.decideLinearFlow(auth.WithUser(ctx, other), f.ID, LinearDecision{Approve: true, Step: 1, ResultHash: f.ResultHash}); !errors.Is(err, errUnavailable) {
		t.Fatal("cross owner approved", err)
	}
	if _, err = s.decideLinearFlow(ctx, f.ID, LinearDecision{Approve: true, Step: 1, ResultHash: f.ResultHash}); err != nil {
		t.Fatal(err)
	}
	// Duplicate decision cannot apply approval to the next stage.
	if _, err = s.decideLinearFlow(ctx, f.ID, LinearDecision{Approve: true, Step: 1, ResultHash: f.ResultHash}); !errors.Is(err, errConflict) {
		t.Fatal("replayed approval accepted", err)
	}
	tick()
	finish()
	tick()
	tick()
	f, err = s.linearFlow(ctx, f.ID)
	if err != nil || f.Status != "awaiting_approval" || f.Step != 3 || calls != 2 {
		t.Fatal(f, calls, err)
	}
	if _, err = s.decideLinearFlow(ctx, f.ID, LinearDecision{Approve: false, Step: 3, ResultHash: f.ResultHash}); err != nil {
		t.Fatal(err)
	}
	tick()
	f, _ = s.linearFlow(ctx, f.ID)
	if f.Status != "rejected" || calls != 2 {
		t.Fatal("rejection ignored", f, calls)
	}
}
func TestLinearFlowRejectsDraftWithoutFreshApproval(t *testing.T) {
	s, ctx := setup(t, nil)
	a, err := s.saveAgent(ctx, Agent{Name: "Draft writer", Enabled: true, ExecutionKind: "content_draft"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.startLinearFlow(ctx, LinearPlan{Name: "Unsafe", Input: "test", RequestKey: "unsafe", DraftTitle: "Title", DraftLocale: "en", Steps: []LinearStep{{Kind: "draft", AgentID: a.ID}, {Kind: "approval"}}})
	if !errors.Is(err, errInvalid) {
		t.Fatal("unsafe plan accepted", err)
	}
}

func TestLinearFlowCreatesOnlyOneSanitizedDraftAfterApproval(t *testing.T) {
	s, ctx, app := bootWorkspace(t)
	s.complete = func(context.Context, *kernel.Deps, string, string, string, string) (string, error) {
		return `<p>Verified draft content</p><script>evil()</script>`, nil
	}
	writer, err := s.saveAgent(ctx, Agent{Name: "Text", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.saveAgent(ctx, Agent{Name: "Save draft", Enabled: true, ExecutionKind: "content_draft"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.startLinearFlow(ctx, LinearPlan{Name: "Draft delivery", Input: "Facts", RequestKey: "draft-acceptance", DraftTitle: "Acceptance draft", DraftLocale: "en", Steps: []LinearStep{{Kind: "agent", AgentID: writer.ID}, {Kind: "approval"}, {Kind: "draft", AgentID: draft.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	advance := func() {
		t.Helper()
		if e := s.tickLinearFlows(ctx); e != nil {
			t.Fatal(e)
		}
	}
	finish := func() {
		t.Helper()
		runs, e := s.runs(ctx)
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range runs {
			if r.Status == "queued" {
				b, _ := json.Marshal(map[string]string{"run_id": r.ID})
				if e = s.execute(ctx, b); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	advance()
	finish()
	advance()
	advance()
	var n int
	if err = app.Deps.DB.QueryRow(`SELECT count(*) FROM content WHERE title='Acceptance draft'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("draft before approval", n, err)
	}
	f, _ = s.linearFlow(ctx, f.ID)
	if _, err = s.decideLinearFlow(ctx, f.ID, LinearDecision{Approve: true, Step: f.Step, ResultHash: f.ResultHash}); err != nil {
		t.Fatal(err)
	}
	advance()
	advance()
	finish()
	advance()
	advance()
	finish()
	f, _ = s.linearFlow(ctx, f.ID)
	if f.Status != "done" {
		t.Fatal("flow not finished", f)
	}
	var status, body string
	if err = app.Deps.DB.QueryRow(`SELECT count(*),status,body FROM content WHERE title='Acceptance draft'`).Scan(&n, &status, &body); err != nil {
		t.Fatal(err)
	}
	if n != 1 || status != "draft" || strings.Contains(body, "script") || !strings.Contains(body, "Verified draft content") {
		t.Fatal("wrong draft", n, status, body)
	}
}

func TestLinearFlowAPIRejectsUnmappedConditionsAndMissingPermissions(t *testing.T) {
	_, ctx, app := bootWorkspace(t)
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	token, _, err := keys.Create(ctx, "Linear admin", auth.UserFromContext(ctx).ID, []string{"workagents.manage"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	call := func(body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/v1/workagents/linear-flows", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		r := httptest.NewRecorder()
		app.Handler.ServeHTTP(r, req)
		return r
	}
	response := call(`{"name":"Imported","input":"Facts","request_key":"x","steps":[{"kind":"agent","agent_id":"x","condition":"facts.valid"}]}`, token)
	if response.Code != 400 && response.Code != 422 {
		t.Fatalf("unknown condition accepted: %d %s", response.Code, response.Body.String())
	}
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, err := users.Create(ctx, "viewer-linear@example.com", "Reader", "correct horse battery", auth.RoleCustomer)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err = keys.Create(ctx, "Linear customer", u.ID, []string{"workagents.manage"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	response = call(`{}`, token)
	if response.Code != 403 {
		t.Fatalf("customer gained manage: %d %s", response.Code, response.Body.String())
	}
}

func TestLinearFlowTickInactiveOwnersCannotStarveLaterFlow(t *testing.T) {
	s, ctx := setup(t, nil)
	users, _ := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	inactive, err := users.Create(ctx, "inactive-batch@example.com", "Inactive", "correct horse battery", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.d.DB.Exec(`UPDATE users SET status='disabled' WHERE id=?`, inactive.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err = s.d.DB.Exec(`INSERT INTO workagents_linear_flows(id,owner_id,request_key,plan,created_at,updated_at) VALUES(?,?,?,'{"steps":[]}',?,?)`, fmt.Sprintf("old-%03d", i), inactive.ID, fmt.Sprintf("old-%03d", i), "2020-01-01T00:00:00Z", "2020-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.d.DB.Exec(`INSERT INTO workagents_linear_flows(id,owner_id,request_key,plan,created_at,updated_at) VALUES('later',?,'later','{"steps":[]}','2021-01-01T00:00:00Z','2021-01-01T00:00:00Z')`, owner(ctx)); err != nil {
		t.Fatal(err)
	}
	if err = s.tickLinearFlows(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.d.DB.QueryRow(`SELECT count(*) FROM workagents_linear_flows WHERE status='failed' AND error_code='owner_unavailable'`).Scan(&n); err != nil || n != 100 {
		t.Fatal("inactive owners left running", n, err)
	}
	if err = s.tickLinearFlows(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	s.d.DB.QueryRow(`SELECT status FROM workagents_linear_flows WHERE id='later'`).Scan(&status)
	if status != "done" {
		t.Fatal("later flow starved", status)
	}
}

func TestLinearFlowTickRotatesErrorsAndContinuesBatch(t *testing.T) {
	s, ctx := setup(t, nil)
	// JSON is valid but cannot decode into a plan. This per-record error must not
	// prevent a later valid record from advancing, or monopolize the next batch.
	for _, row := range []struct{ id, plan string }{{"a-error", `[]`}, {"b-good", `{"steps":[]}`}} {
		if _, err := s.d.DB.Exec(`INSERT INTO workagents_linear_flows(id,owner_id,request_key,plan,created_at,updated_at) VALUES(?,?,?,?,?,?)`, row.id, owner(ctx), row.id, row.plan, "2020-01-01T00:00:00Z", "2020-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.tickLinearFlows(ctx); err == nil {
		t.Fatal("record error not reported")
	}
	var status, updated string
	if err := s.d.DB.QueryRow(`SELECT status FROM workagents_linear_flows WHERE id='b-good'`).Scan(&status); err != nil || status != "done" {
		t.Fatal("batch aborted", status, err)
	}
	if err := s.d.DB.QueryRow(`SELECT updated_at FROM workagents_linear_flows WHERE id='a-error'`).Scan(&updated); err != nil || updated == "2020-01-01T00:00:00Z" {
		t.Fatal("erroring flow not rotated", updated, err)
	}
}
