package workagents

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

type designReviewPreviewInput struct {
	PageID  string `json:"page_id"`
	AgentID string `json:"agent_id"`
}
type designReviewPageData struct {
	CanRun      bool
	CanAudit    bool
	PageID      string
	Agents      []Agent
	Reviews     []DesignReview
	Preview     *DesignReviewPreview
	ContextText string
	RequestKey  string
}

func (h *handlers) designReviewPage(w http.ResponseWriter, r *http.Request) {
	h.renderDesignReviews(w, r, nil)
}
func (h *handlers) renderDesignReviews(w http.ResponseWriter, r *http.Request, preview *DesignReviewPreview) {
	agents, err := h.s.agents(r.Context())
	if err != nil {
		h.failure(w, err)
		return
	}
	reviews, err := h.s.designReviews(r.Context())
	if err != nil {
		h.failure(w, err)
		return
	}
	data := designReviewPageData{CanRun: auth.Can(r.Context(), "workagents.run") && auth.Can(r.Context(), "content.view"), PageID: r.URL.Query().Get("page_id"), Agents: agents, Reviews: reviews, Preview: preview, RequestKey: db.NewID()}
	data.CanAudit = auth.Can(r.Context(), "system.health")
	if preview != nil {
		data.ContextText = preview.Context
		data.PageID = preview.PageID
	}
	w.Header().Set("Cache-Control", "no-store")
	h.adminTpl.Render(w, r, "pages/workagents/design_reviews.html", data)
}
func (h *handlers) previewDesignReviewForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.failure(w, errInvalid)
		return
	}
	preview, err := h.s.previewDesignReview(r.Context(), r.FormValue("page_id"), r.FormValue("agent_id"))
	if err != nil {
		h.failure(w, err)
		return
	}
	h.renderDesignReviews(w, r, &preview)
}
func (h *handlers) enqueueDesignReviewForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.failure(w, errInvalid)
		return
	}
	_, err := h.s.enqueueDesignReview(r.Context(), DesignReviewEnqueue{PreviewID: r.FormValue("preview_id"), RequestKey: r.FormValue("request_key"), Approved: r.FormValue("approved") == "yes"})
	if err != nil {
		h.failure(w, err)
		return
	}
	http.Redirect(w, r, "/admin/workagents/design-reviews", http.StatusSeeOther)
}
func (h *handlers) designReviewActionForm(w http.ResponseWriter, r *http.Request) {
	var err error
	if chi.URLParam(r, "action") == "cancel" {
		_, err = h.s.cancelDesignReview(r.Context(), chi.URLParam(r, "id"))
	} else if chi.URLParam(r, "action") == "retry" {
		_, err = h.s.retryDesignReview(r.Context(), chi.URLParam(r, "id"))
	} else {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.failure(w, err)
		return
	}
	http.Redirect(w, r, "/admin/workagents/design-reviews", http.StatusSeeOther)
}
func (h *handlers) registerDesignReviews(d *kernel.Deps) {
	d.Admin.AddNav(kernel.NavItem{ID: "workagents.design-reviews", LabelKey: "workagents.review.title", Icon: "bot", Href: "/admin/workagents/design-reviews", Permission: "workagents.read", Group: "system", Position: 29})
	d.Jobs.Register(designReviewJobKind, h.s.executeDesignReview, 1)
	d.Jobs.Cron("workagents.design_reviews.recovery", 30*time.Second, h.s.recoverDesignReviews)
	d.Router.Admin.With(auth.RequirePerm("workagents.read")).Get("/workagents/design-reviews", h.designReviewPage)
	d.Router.Admin.With(auth.RequirePerm("workagents.run")).Post("/workagents/design-reviews/preview", h.previewDesignReviewForm)
	d.Router.Admin.With(auth.RequirePerm("workagents.run")).Post("/workagents/design-reviews/enqueue", h.enqueueDesignReviewForm)
	d.Router.Admin.With(auth.RequirePerm("workagents.run")).Post("/workagents/design-reviews/{id}/{action}", h.designReviewActionForm)
	h.designReviewAPI(d)
}

func (h *handlers) designReviewAPI(d *kernel.Deps) {
	add := func(method, path, summary, permission string, request, response any, status int, fn http.HandlerFunc) {
		d.API.Add(kernel.APIOperation{Method: method, Path: "/v1/workagents/design-reviews" + path, Summary: summary, Tags: []string{"workagents"}, Scopes: []string{permission, "content.view"}, Request: request, Response: response, Contract: &kernel.APIContract{SuccessStatuses: []int{status}, MaxBodyBytes: 64 << 10, Errors: []kernel.APIErrorResponse{{Status: 401, Code: "unauthorized", Description: "Active account required"}, {Status: 403, Code: "forbidden", Description: "Permission required"}, {Status: 404, Code: "workagents_failed", Description: "Page, agent, preview or task unavailable in this scope"}, {Status: 409, Code: "workagents_failed", Description: "Provider unavailable, stale approval or invalid task state"}, {Status: 422, Code: "invalid", Description: "Invalid JSON"}, {Status: 422, Code: "workagents_failed", Description: "Invalid page or review request"}, {Status: 429, Code: "workagents_failed", Description: "Concurrency limit reached"}}}, Handler: func(w http.ResponseWriter, r *http.Request) {
			if owner(r.Context()) == "" {
				httpx.Error(w, 401, "unauthorized", "active account required", nil)
				return
			}
			fn(w, r)
		}})
	}
	add("GET", "", "List scoped read-only design reviews", "workagents.read", nil, []DesignReview{}, 200, func(w http.ResponseWriter, r *http.Request) {
		v, e := h.s.designReviews(r.Context())
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})
	add("POST", "/preview", "Preview exact limited page context before approval", "workagents.run", designReviewPreviewInput{}, DesignReviewPreview{}, 200, func(w http.ResponseWriter, r *http.Request) {
		var in designReviewPreviewInput
		if !decode(w, r, &in) {
			return
		}
		v, e := h.s.previewDesignReview(r.Context(), in.PageID, in.AgentID)
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})
	add("POST", "", "Approve bound context and enqueue idempotent design review", "workagents.run", DesignReviewEnqueue{}, DesignReview{}, 202, func(w http.ResponseWriter, r *http.Request) {
		var in DesignReviewEnqueue
		if !decode(w, r, &in) {
			return
		}
		v, e := h.s.enqueueDesignReview(r.Context(), in)
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 202, v, nil)
	})
	add("GET", "/{id}", "Read scoped design review status and untrusted findings", "workagents.read", nil, DesignReview{}, 200, func(w http.ResponseWriter, r *http.Request) {
		v, e := h.s.designReview(r.Context(), chi.URLParam(r, "id"))
		if e != nil {
			h.failure(w, e)
			return
		}
		httpx.JSON(w, 200, v, nil)
	})
	for _, action := range []string{"cancel", "retry"} {
		action := action
		add("POST", "/{id}/"+action, "Request design review "+action, "workagents.run", nil, DesignReview{}, 200, func(w http.ResponseWriter, r *http.Request) {
			var v DesignReview
			var e error
			if action == "cancel" {
				v, e = h.s.cancelDesignReview(r.Context(), chi.URLParam(r, "id"))
			} else {
				v, e = h.s.retryDesignReview(r.Context(), chi.URLParam(r, "id"))
			}
			if e != nil {
				h.failure(w, e)
				return
			}
			httpx.JSON(w, 200, v, nil)
		})
	}
}
