package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	approvalsPath  = "/admin/agents/approvals"
	pendingShown   = 100
	recentShown    = 20
	approvalsPage  = "pages/agents/approvals.html"
	approvalsLabel = "mcp.nav.approvals"
)

// approvalView is an approval prepared for the admin page.
type approvalView struct {
	Approval
	ArgsPretty   string
	ResultPretty string
}

type approvalsData struct {
	Pending []approvalView
	Recent  []approvalView
	Notice  string // i18n key of the last action's outcome
}

func pretty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

func views(list []Approval) []approvalView {
	out := make([]approvalView, len(list))
	for i, a := range list {
		out[i] = approvalView{Approval: a, ArgsPretty: pretty(a.Args), ResultPretty: pretty(a.Result)}
	}
	return out
}

func (s *server) registerAdmin(tpl *httpx.Templates) {
	s.d.Admin.AddNav(kernel.NavItem{ID: "agents.approvals", LabelKey: approvalsLabel, Icon: "shield-check", Href: approvalsPath,
		Permission: PermApprove, Group: "agents", Position: 10})
	r := s.d.Router.Admin.With(auth.RequirePerm(PermApprove))
	r.Get("/agents/approvals", func(w http.ResponseWriter, r *http.Request) { s.renderApprovals(w, r, tpl, "") })
	r.Post("/agents/approvals/{id}/approve", s.adminDecide(tpl, s.approvals.Approve, "mcp.approvals.approved"))
	r.Post("/agents/approvals/{id}/deny", s.adminDecide(tpl, s.approvals.Deny, "mcp.approvals.denied"))
}

func (s *server) renderApprovals(w http.ResponseWriter, r *http.Request, tpl *httpx.Templates, notice string) {
	pending, err := s.approvals.list(r.Context(), StatusPending, pendingShown, 0)
	if err != nil {
		s.log.Error("mcp: list approvals", "err", err)
		tpl.RenderError(w, r, http.StatusInternalServerError)
		return
	}
	all, err := s.approvals.list(r.Context(), "", pendingShown+recentShown, 0)
	if err != nil {
		s.log.Error("mcp: list approvals", "err", err)
		tpl.RenderError(w, r, http.StatusInternalServerError)
		return
	}
	var recent []Approval
	for _, a := range all {
		if a.Status != StatusPending && len(recent) < recentShown {
			recent = append(recent, a)
		}
	}
	tpl.Render(w, r, approvalsPage, approvalsData{Pending: views(pending), Recent: views(recent), Notice: notice})
}

type decideFunc func(ctx context.Context, id string) (*Approval, bool, error)

// adminDecide approves or denies; HTMX requests get the refreshed page
// content, plain form posts a redirect with a flash message.
func (s *server) adminDecide(tpl *httpx.Templates, decide decideFunc, notice string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, won, err := decide(r.Context(), chi.URLParam(r, "id"))
		switch {
		case errors.Is(err, ErrApprovalNotFound):
			tpl.RenderError(w, r, http.StatusNotFound)
			return
		case errors.Is(err, ErrDecisionForbidden):
			tpl.RenderError(w, r, http.StatusForbidden)
			return
		case err != nil:
			s.log.Error("mcp: decide approval", "err", err)
			tpl.RenderError(w, r, http.StatusInternalServerError)
			return
		}
		msg, kind := notice, "success"
		if !won {
			msg, kind = "mcp.approvals.already_decided", "info"
		}
		if r.Header.Get("HX-Request") == "true" {
			s.renderApprovals(w, r, tpl, msg)
			return
		}
		httpx.SetFlash(w, kind, msg)
		http.Redirect(w, r, approvalsPath, http.StatusSeeOther)
	}
}

func (s *server) registerAPI() {
	tags, scopes := []string{"mcp"}, []string{PermApprove}
	s.d.API.Add(api.Op("GET", "/mcp/approvals", "List MCP approvals (filter with ?status=pending|approved|denied|expired|failed)",
		tags, scopes, nil, []Approval{}, func(w http.ResponseWriter, r *http.Request) {
			status := r.URL.Query().Get("status")
			switch status {
			case "", StatusPending, StatusApproved, StatusDenied, StatusExpired, StatusFailed:
			default:
				api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "invalid status", map[string]string{"status": "invalid"})
				return
			}
			page, per := api.Pagination(r)
			list, err := s.approvals.list(r.Context(), status, per, (page-1)*per)
			if err != nil {
				api.Fail(w, r, err)
				return
			}
			total, err := s.approvals.count(r.Context(), status)
			if err != nil {
				api.Fail(w, r, err)
				return
			}
			api.WriteList(w, list, total, page, per)
		}))
	decide := func(fn decideFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			a, _, err := fn(r.Context(), chi.URLParam(r, "id"))
			if errors.Is(err, ErrApprovalNotFound) {
				api.NotFound(w)
				return
			}
			if errors.Is(err, ErrDecisionForbidden) {
				api.WriteError(w, http.StatusForbidden, "forbidden",
					"approvals are decided by a user session, or by an API key that lists mcp.approve explicitly and did not request the call", nil)
				return
			}
			if err != nil {
				api.Fail(w, r, err)
				return
			}
			api.WriteItem(w, a)
		}
	}
	s.d.API.Add(api.Op("POST", "/mcp/approvals/{id}/approve", "Approve a pending MCP call: it executes once and the result is stored (no-op when already decided)",
		tags, scopes, nil, Approval{}, decide(s.approvals.Approve)))
	s.d.API.Add(api.Op("POST", "/mcp/approvals/{id}/deny", "Deny a pending MCP call (no-op when already decided)",
		tags, scopes, nil, Approval{}, decide(s.approvals.Deny)))
}

// healthCheck adds the pending-approvals line to System Health.
func (s *server) healthCheck(ctx context.Context, v any) (any, error) {
	list, ok := v.(*[]admin.HealthCheck)
	if !ok {
		return v, nil
	}
	pending, stale, err := s.approvals.pendingCounts(ctx)
	check := admin.HealthCheck{Name: "mcp_approvals", Status: admin.CheckOK}
	tr := func(key string, args ...any) string {
		if s.d.I18n == nil {
			return fmt.Sprint(append([]any{key}, args...)...)
		}
		return s.d.I18n.T(i18n.Locale(ctx), key, args...)
	}
	switch {
	case err != nil:
		check.Status, check.Detail = admin.CheckFail, err.Error()
	case stale > 0:
		check.Status, check.Detail = admin.CheckWarn, tr("mcp.health.stale", pending, stale)
	default:
		check.Detail = tr("mcp.health.pending", pending)
	}
	*list = append(*list, check)
	return list, nil
}
