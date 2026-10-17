package workagents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// apiRouter mounts the module's raw operation handlers (without the api
// package's scope guard) so the module's own checks and error mapping are
// exercised directly.
func apiRouter(t *testing.T, s *service) http.Handler {
	t.Helper()
	s.d.API = kernel.NewAPIRegistry()
	h := &handlers{s: s}
	h.api(s.d)
	r := chi.NewRouter()
	for _, op := range s.d.API.Operations() {
		r.Method(op.Method, op.Path, op.Handler)
	}
	return r
}

func apiCall(router http.Handler, ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAPIHandlersRejectInactiveOrAnonymousAccounts(t *testing.T) {
	s, _ := setup(t, nil)
	router := apiRouter(t, s)
	inactive := auth.WithUser(context.Background(), &auth.User{ID: "disabled", Role: auth.RoleAdmin, Status: "disabled"})
	for _, ctx := range []context.Context{context.Background(), inactive} {
		rec := apiCall(router, ctx, "GET", "/v1/workagents/agents", "")
		if rec.Code != 401 || !strings.Contains(rec.Body.String(), "unauthorized") {
			t.Fatal("inactive/anonymous account reached handler", rec.Code, rec.Body.String())
		}
	}
}

func TestAPIHandlersRejectMalformedJSONOnEveryWrite(t *testing.T) {
	s, ctx := setup(t, nil)
	router := apiRouter(t, s)
	for _, path := range []string{"/v1/workagents/agents", "/v1/workagents/agents/x/run", "/v1/workagents/workflows", "/v1/workagents/linear-flows", "/v1/workagents/linear-flows/x/decision"} {
		for _, body := range []string{`{"unknown_field":1}`, `{} {}`, `not json`} {
			rec := apiCall(router, ctx, "POST", path, body)
			if rec.Code != 422 || !strings.Contains(rec.Body.String(), "invalid or oversized JSON") {
				t.Fatalf("%s accepted %q: %d %s", path, body, rec.Code, rec.Body.String())
			}
		}
	}
	var n int
	if err := s.d.DB.QueryRow(`SELECT (SELECT count(*) FROM workagents_agents)+(SELECT count(*) FROM workagents_workflows)+(SELECT count(*) FROM workagents_linear_flows)`).Scan(&n); err != nil || n != 0 {
		t.Fatal("malformed request stored state", n, err)
	}
}

func TestAPIHandlersMapServiceErrorsToStatusCodes(t *testing.T) {
	s, ctx := setup(t, nil)
	router := apiRouter(t, s)
	rec := apiCall(router, ctx, "POST", "/v1/workagents/agents", `{"name":""}`)
	if rec.Code != 422 {
		t.Fatal("invalid agent not 422", rec.Code, rec.Body.String())
	}
	disabled, err := s.saveAgent(ctx, Agent{Name: "Disabled", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	rec = apiCall(router, ctx, "POST", "/v1/workagents/agents/"+disabled.ID+"/run", `{"input":"x"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), errUnavailable.Error()) {
		t.Fatal("disabled agent run not 409", rec.Code, rec.Body.String())
	}
	enabled, err := s.saveAgent(ctx, Agent{Name: "Enabled", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if rec = apiCall(router, ctx, "POST", "/v1/workagents/agents/"+enabled.ID+"/run", `{"input":"first","request_key":"k"}`); rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = apiCall(router, ctx, "POST", "/v1/workagents/agents/"+enabled.ID+"/run", `{"input":"second","request_key":"k"}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), errConflict.Error()) {
		t.Fatal("reused key not 409", rec.Code, rec.Body.String())
	}
	rec = apiCall(router, ctx, "POST", "/v1/workagents/workflows", `{"agent_id":"`+enabled.ID+`","name":"Graph","input":"{\"organization\":{}}","interval_seconds":60,"enabled":true}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "workflow_graph_unsupported") {
		t.Fatal("graph workflow not 409", rec.Code, rec.Body.String())
	}
	if rec = apiCall(router, ctx, "GET", "/v1/workagents/linear-flows/missing", ""); rec.Code != 404 {
		t.Fatal("missing flow not 404", rec.Code)
	}
	if rec = apiCall(router, ctx, "POST", "/v1/workagents/linear-flows/missing/decision", `{"approve":true}`); rec.Code != 404 {
		t.Fatal("missing flow decision not 404", rec.Code)
	}
	if rec = apiCall(router, ctx, "POST", "/v1/workagents/linear-flows", `{"name":"x"}`); rec.Code != 422 {
		t.Fatal("invalid linear plan not 422", rec.Code)
	}
}

func TestAPIHandlersFailureFallbackAndBusy(t *testing.T) {
	h := &handlers{}
	for _, c := range []struct {
		err  error
		code int
		text string
	}{
		{errBusy, 429, errBusy.Error()},
		{errInvalid, 422, errInvalid.Error()},
		{errNotFound, 404, errNotFound.Error()},
		{errWorkflowGraph, 409, "workflow_graph_unsupported"},
		{errUnavailable, 409, errUnavailable.Error()},
		{errConflict, 409, errConflict.Error()},
		{errors.New("SELECT secret FROM table"), 500, "agent operation failed"},
	} {
		rec := httptest.NewRecorder()
		h.failure(rec, c.err)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.text) {
			t.Fatal("wrong failure mapping", c.err, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	h.failure(rec, errors.New("SELECT secret FROM table"))
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("internal error leaked", rec.Body.String())
	}
}

func TestAPIHandlersListAndLinearFlowLifecycle(t *testing.T) {
	s, ctx := setup(t, func(_ context.Context, _ *kernel.Deps, _, _, _, input string) (string, error) {
		return input + " done", nil
	})
	router := apiRouter(t, s)
	a, err := s.saveAgent(ctx, Agent{Name: "Writer", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	rec := apiCall(router, ctx, "POST", "/v1/workagents/workflows", `{"agent_id":"`+a.ID+`","name":"Daily","input":"Summarise","interval_seconds":120,"enabled":true}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"interval_seconds":120`) {
		t.Fatal("workflow save failed", rec.Code, rec.Body.String())
	}
	rec = apiCall(router, ctx, "GET", "/v1/workagents/workflows", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Daily") {
		t.Fatal("workflow list failed", rec.Code, rec.Body.String())
	}
	if rec = apiCall(router, ctx, "POST", "/v1/workagents/agents/"+a.ID+"/run", `{"input":"hello"}`); rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = apiCall(router, ctx, "GET", "/v1/workagents/runs", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"input":"hello"`) {
		t.Fatal("runs list failed", rec.Code, rec.Body.String())
	}
	if _, err = s.d.DB.Exec(`INSERT INTO workagents_approvals(id,owner_id,name,status,decision,created_at)VALUES('ap1',?,'Imported gate','pending','approve','2020-01-01T00:00:00Z')`, a.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.d.DB.Exec(`INSERT INTO workagents_artifacts(id,owner_id,name,content,historical,created_at)VALUES('ar1',?,'Old artifact','Archived text',1,'2020-01-01T00:00:00Z')`, a.OwnerID); err != nil {
		t.Fatal(err)
	}
	rec = apiCall(router, ctx, "GET", "/v1/workagents/approvals", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Imported gate") || !strings.Contains(rec.Body.String(), `"status":"pending"`) {
		t.Fatal("approvals list failed", rec.Code, rec.Body.String())
	}
	rec = apiCall(router, ctx, "GET", "/v1/workagents/artifacts", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Archived text") {
		t.Fatal("artifacts list failed", rec.Code, rec.Body.String())
	}

	plan := `{"name":"Flow","input":"Facts","request_key":"api-flow","steps":[{"kind":"agent","agent_id":"` + a.ID + `"},{"kind":"approval"}]}`
	rec = apiCall(router, ctx, "POST", "/v1/workagents/linear-flows", plan)
	if rec.Code != 202 {
		t.Fatal("linear flow start failed", rec.Code, rec.Body.String())
	}
	var started struct {
		Data LinearFlow `json:"data"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.Data.ID == "" || started.Data.Status != "running" {
		t.Fatal("linear flow response", rec.Body.String(), err)
	}
	id := started.Data.ID
	rec = apiCall(router, ctx, "GET", "/v1/workagents/linear-flows/"+id, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), id) {
		t.Fatal("linear flow read failed", rec.Code, rec.Body.String())
	}
	// Stale decision before the approval gate is reached is a conflict.
	rec = apiCall(router, ctx, "POST", "/v1/workagents/linear-flows/"+id+"/decision", `{"approve":true,"step":1,"result_hash":"x"}`)
	if rec.Code != 409 {
		t.Fatal("decision on running flow not 409", rec.Code, rec.Body.String())
	}
	if _, err = s.d.DB.Exec(`UPDATE workagents_linear_flows SET status='awaiting_approval',step=1,result='Facts done' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	rec = apiCall(router, ctx, "POST", "/v1/workagents/linear-flows/"+id+"/decision", `{"approve":false,"step":1,"result_hash":"`+flowHash("Facts done")+`"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"rejected"`) {
		t.Fatal("decision failed", rec.Code, rec.Body.String())
	}
}

func TestAPIHandlersHideDatabaseFailuresBehindGeneric500(t *testing.T) {
	cases := []struct {
		table, method, path, body string
	}{
		{"workagents_runs", "GET", "/v1/workagents/runs", ""},
		{"workagents_workflows", "GET", "/v1/workagents/workflows", ""},
		{"workagents_artifacts", "GET", "/v1/workagents/artifacts", ""},
		{"workagents_approvals", "GET", "/v1/workagents/approvals", ""},
		{"workagents_linear_flows", "GET", "/v1/workagents/linear-flows/x", ""},
	}
	for _, c := range cases {
		t.Run(c.table, func(t *testing.T) {
			s, ctx := setup(t, nil)
			router := apiRouter(t, s)
			if _, err := s.d.DB.Exec(`DROP TABLE ` + c.table); err != nil {
				t.Fatal(err)
			}
			rec := apiCall(router, ctx, c.method, c.path, c.body)
			if rec.Code != 500 || !strings.Contains(rec.Body.String(), "agent operation failed") || strings.Contains(rec.Body.String(), "no such table") {
				t.Fatal("database failure not hidden", rec.Code, rec.Body.String())
			}
		})
	}
	t.Run("agents", func(t *testing.T) {
		s, ctx := setup(t, nil)
		router := apiRouter(t, s)
		if _, err := s.saveAgent(ctx, Agent{Name: "Broken tools"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.d.DB.Exec(`UPDATE workagents_agents SET unsupported_tools='{}'`); err != nil {
			t.Fatal(err)
		}
		rec := apiCall(router, ctx, "GET", "/v1/workagents/agents", "")
		if rec.Code != 409 {
			t.Fatal("corrupt tool list not reported unavailable", rec.Code, rec.Body.String())
		}
	})
	t.Run("linear start", func(t *testing.T) {
		s, ctx := setup(t, nil)
		router := apiRouter(t, s)
		a, err := s.saveAgent(ctx, Agent{Name: "Writer", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.d.DB.Exec(`DROP TABLE workagents_linear_flows`); err != nil {
			t.Fatal(err)
		}
		rec := apiCall(router, ctx, "POST", "/v1/workagents/linear-flows", `{"name":"F","input":"I","request_key":"k","steps":[{"kind":"agent","agent_id":"`+a.ID+`"},{"kind":"approval"}]}`)
		if rec.Code != 500 {
			t.Fatal("insert failure not 500", rec.Code, rec.Body.String())
		}
	})
}
