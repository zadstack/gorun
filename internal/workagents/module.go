package workagents

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

//go:embed migrations/*.sql templates/* catalog/*.json
var files embed.FS

type Module struct{ complete Completion }

func New(complete Completion) *Module { return &Module{complete: complete} }
func (*Module) Name() string          { return "workagents" }
func (*Module) Migrations() fs.FS     { return files }
func (*Module) Catalogs() fs.FS       { return files }
func (m *Module) Register(d *kernel.Deps) error {
	// Auth registered before this module's permission migration was applied.
	if err := auth.ReloadRoles(context.Background(), d.DB); err != nil {
		return err
	}
	tpl, e := template.ParseFS(files, "templates/page.html")
	if e != nil {
		return e
	}
	s := &service{d: d, complete: m.complete, now: time.Now}
	d.Services.Set("workagents", s)
	d.Perms.Declare("workagents.manage", map[string]string{"en": "Manage agent workflows", "ar": "إدارة الإيجنتس والفلوهات"})
	d.Perms.Declare("workagents.read", map[string]string{"en": "Read owned agents", "ar": "قراءة الإيجنتس المملوكة"})
	d.Perms.Declare("workagents.run", map[string]string{"en": "Run owned agents", "ar": "تشغيل الإيجنتس المملوكة"})
	d.Admin.AddNav(kernel.NavItem{ID: "workagents.workspace", LabelKey: "workagents.title", Icon: "bot", Href: "/admin/workagents", Permission: "workagents.manage", Group: "system", Position: 28})
	d.Jobs.Register(jobKind, s.execute, 1)
	d.Jobs.Cron("workagents.schedules", 30*time.Second, s.schedule)
	d.Jobs.Cron("workagents.linear", 30*time.Second, s.tickLinearFlows)
	d.Hooks.Filter("system.health.checks", 50, s.healthChecks)
	adminTpl, _ := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	h := &handlers{s: s, tpl: tpl, adminTpl: adminTpl}
	d.Router.Admin.With(auth.RequirePerm("workagents.manage")).Get("/workagents", h.page)
	d.Router.Public.Group(func(r chi.Router) {
		r.Use(auth.CSRFMiddleware())
		r.Get("/agents", h.page)
		r.Get("/workspace", h.page)
		r.Post("/agents/run", h.runForm)
		r.Post("/agents/profile", h.profileForm)
		r.Post("/agents/schedule", h.scheduleForm)
	})
	d.Router.Public.Get("/static/workagents.css", func(w http.ResponseWriter, r *http.Request) {
		b, e := files.ReadFile("templates/workagents.css")
		if e != nil {
			http.Error(w, "unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write(b)
	})
	h.api(d)
	return nil
}

type handlers struct {
	s        *service
	tpl      *template.Template
	adminTpl *httpx.Templates
}
type archived struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Content    string `json:"content,omitempty"`
	Status     string `json:"status,omitempty"`
	Historical bool   `json:"historical"`
}

func (s *service) archive(r *http.Request, approvals bool) ([]archived, error) {
	q := `SELECT id,name,content,'',historical FROM workagents_artifacts WHERE owner_id=? OR ?=1 ORDER BY created_at DESC LIMIT 100`
	if approvals {
		q = `SELECT id,name,decision,status,historical FROM workagents_approvals WHERE owner_id=? OR ?=1 ORDER BY created_at DESC LIMIT 100`
	}
	rows, e := s.d.DB.QueryContext(r.Context(), q, owner(r.Context()), manager(r.Context()))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []archived{}
	for rows.Next() {
		var a archived
		if e = rows.Scan(&a.ID, &a.Name, &a.Content, &a.Status, &a.Historical); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (h *handlers) page(w http.ResponseWriter, r *http.Request) {
	if owner(r.Context()) == "" {
		http.Redirect(w, r, "/account?next=/agents", 303)
		return
	}
	if manager(r.Context()) && h.adminTpl != nil && r.URL.Path != "/admin/workagents" {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/admin/workagents", http.StatusSeeOther)
		return
	}
	a, e := h.s.agents(r.Context())
	if e != nil {
		h.failure(w, e)
		return
	}
	runs, e := h.s.runs(r.Context())
	if e != nil {
		h.failure(w, e)
		return
	}
	workflows, e := h.s.workflows(r.Context())
	if e != nil {
		h.failure(w, e)
		return
	}
	artifacts, e := h.s.archive(r, false)
	if e != nil {
		h.failure(w, e)
		return
	}
	approvals, e := h.s.archive(r, true)
	if e != nil {
		h.failure(w, e)
		return
	}
	owners := []profileOwner{}
	if manager(r.Context()) {
		rows, e := h.s.d.DB.QueryContext(r.Context(), `SELECT id,name FROM users WHERE status='active' ORDER BY name,id LIMIT 200`)
		if e != nil {
			h.failure(w, e)
			return
		}
		for rows.Next() {
			var u profileOwner
			if e = rows.Scan(&u.ID, &u.Name); e != nil {
				rows.Close()
				h.failure(w, e)
				return
			}
			owners = append(owners, u)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			h.failure(w, e)
			return
		}
	}
	data := struct {
		Agents               []Agent
		Runs                 []Run
		Workflows            []Workflow
		Artifacts, Approvals []archived
		CSRF                 string
		Manage               bool
		Owners               []profileOwner
	}{a, runs, workflows, artifacts, approvals, auth.CSRFToken(r.Context()), manager(r.Context()), owners}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if data.Manage && h.adminTpl != nil {
		h.adminTpl.Render(w, r, "pages/workagents/workspace.html", data)
		return
	}
	if e = h.tpl.Execute(w, data); e != nil {
		h.s.d.Log.Error("workagents page render failed", "err", e)
	}
}
func (h *handlers) failure(w http.ResponseWriter, e error) {
	code := 500
	message := "agent operation failed"
	switch {
	case errors.Is(e, errBusy):
		code = 429
		message = e.Error()
	case errors.Is(e, errInvalid):
		code = 422
		message = e.Error()
	case errors.Is(e, errNotFound):
		code = 404
		message = e.Error()
	case errors.Is(e, errWorkflowGraph):
		code = 409
		message = e.Error()
	case errors.Is(e, errUnavailable):
		code = 409
		message = e.Error()
	case errors.Is(e, errConflict):
		code = 409
		message = e.Error()
	}
	httpx.Error(w, code, "workagents_failed", message, nil)
}
func (h *handlers) runForm(w http.ResponseWriter, r *http.Request) {
	if owner(r.Context()) == "" {
		http.Error(w, "unauthorized", 401)
		return
	}
	if e := r.ParseForm(); e != nil {
		h.failure(w, errInvalid)
		return
	}
	_, e := h.s.run(r.Context(), r.FormValue("agent_id"), r.FormValue("input"), r.FormValue("request_key"))
	if e != nil {
		h.failure(w, e)
		return
	}
	http.Redirect(w, r, "/agents#runs", 303)
}
func (h *handlers) profileForm(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		h.failure(w, errInvalid)
		return
	}
	a := Agent{ID: r.FormValue("id"), OwnerID: r.FormValue("owner_id"), Name: r.FormValue("name"), Description: r.FormValue("description"), SystemPrompt: r.FormValue("system_prompt"), ConnectionID: r.FormValue("connection_id"), Model: r.FormValue("model"), Enabled: r.FormValue("enabled") == "on", ExecutionKind: r.FormValue("execution_kind")}
	if a.ID != "" {
		old, e := h.s.agent(r.Context(), a.ID)
		if e != nil {
			h.failure(w, e)
			return
		}
		a.UnsupportedTools = old.UnsupportedTools
	}
	if _, e := h.s.saveAgent(r.Context(), a); e != nil {
		h.failure(w, e)
		return
	}
	http.Redirect(w, r, "/agents", 303)
}
func (h *handlers) scheduleForm(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		h.failure(w, errInvalid)
		return
	}
	interval, e := strconv.Atoi(r.FormValue("interval_seconds"))
	if e != nil {
		h.failure(w, errInvalid)
		return
	}
	wf := Workflow{ID: r.FormValue("id"), AgentID: r.FormValue("agent_id"), Name: r.FormValue("name"), Input: r.FormValue("input"), Enabled: r.FormValue("enabled") == "on", IntervalSeconds: interval}
	if _, e = h.s.saveWorkflow(r.Context(), wf); e != nil {
		h.failure(w, e)
		return
	}
	http.Redirect(w, r, "/agents#workflows", 303)
}
func (h *handlers) api(d *kernel.Deps) {
	add := func(method, path, summary string, request, response any, fn http.HandlerFunc) {
		permission := "workagents.read"
		if method != "GET" {
			permission = "workagents.manage"
		}
		if strings.HasSuffix(path, "/run") {
			permission = "workagents.run"
		}
		d.API.Add(kernel.APIOperation{Method: method, Path: "/v1/workagents" + path, Summary: summary, Tags: []string{"workagents"}, Scopes: []string{permission}, Request: request, Response: response, Handler: func(w http.ResponseWriter, r *http.Request) {
			if owner(r.Context()) == "" {
				httpx.Error(w, 401, "unauthorized", "active account required", nil)
				return
			}
			fn(w, r)
		}})
	}
	add("GET", "/agents", "List owned agent profiles", nil, []Agent{}, func(w http.ResponseWriter, r *http.Request) {
		v, e := h.s.agents(r.Context())
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})
	add("POST", "/agents", "Save a native agent profile", Agent{}, Agent{}, func(w http.ResponseWriter, r *http.Request) {
		var in Agent
		if !decode(w, r, &in) {
			return
		}
		v, e := h.s.saveAgent(r.Context(), in)
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})
	type runInput struct {
		Input      string `json:"input"`
		RequestKey string `json:"request_key"`
	}
	add("POST", "/agents/{id}/run", "Queue a text agent run", runInput{}, map[string]string{"run_id": ""}, func(w http.ResponseWriter, r *http.Request) {
		var in runInput
		if !decode(w, r, &in) {
			return
		}
		id, e := h.s.run(r.Context(), chi.URLParam(r, "id"), in.Input, in.RequestKey)
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 202, map[string]string{"run_id": id}, nil)
	})
	add("GET", "/runs", "List run status and persisted results", nil, []Run{}, func(w http.ResponseWriter, r *http.Request) {
		v, e := h.s.runs(r.Context())
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})
	add("GET", "/workflows", "List owned schedules", nil, []Workflow{}, func(w http.ResponseWriter, r *http.Request) {
		v, e := h.s.workflows(r.Context())
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})
	add("POST", "/workflows", "Save a persistent agent schedule", Workflow{}, Workflow{}, func(w http.ResponseWriter, r *http.Request) {
		var in Workflow
		if !decode(w, r, &in) {
			return
		}
		v, e := h.s.saveWorkflow(r.Context(), in)
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})

	add("POST", "/linear-flows", "Start an explicit linear agent flow with human approval", LinearPlan{}, LinearFlow{}, func(w http.ResponseWriter, r *http.Request) {
		var in LinearPlan
		if !decode(w, r, &in) {
			return
		}
		v, e := h.s.startLinearFlow(r.Context(), in)
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 202, v, nil)
	})
	add("GET", "/linear-flows/{id}", "Read flow output and exact approval step", nil, LinearFlow{}, func(w http.ResponseWriter, r *http.Request) {
		v, e := h.s.linearFlow(r.Context(), chi.URLParam(r, "id"))
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})
	add("POST", "/linear-flows/{id}/decision", "Approve or reject the exact flow output", LinearDecision{}, LinearFlow{}, func(w http.ResponseWriter, r *http.Request) {
		var in LinearDecision
		if !decode(w, r, &in) {
			return
		}
		v, e := h.s.decideLinearFlow(r.Context(), chi.URLParam(r, "id"), in)
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})
	for _, kind := range []string{"artifacts", "approvals"} {
		kind := kind
		add("GET", "/"+kind, "List owned "+kind, nil, []archived{}, func(w http.ResponseWriter, r *http.Request) {
			v, e := h.s.archive(r, kind == "approvals")
			if e != nil {
				h.failure(w, e)
				return
			}
			httpx.JSON(w, 200, v, nil)
		})
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil || dec.Decode(&struct{}{}) != io.EOF {
		httpx.Error(w, 422, "invalid", "invalid or oversized JSON", nil)
		return false
	}
	return true
}

type profileOwner struct{ ID, Name string }
