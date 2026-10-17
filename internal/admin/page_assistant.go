package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func (h *handlers) pageAssistantRoutes() {
	h.d.Router.Admin.Get("/x/page-assistant/{content}", h.pageAssistantGet)
	h.d.Router.Admin.Post("/x/page-assistant/{content}/apply", h.pageAssistantApply)
}

func (h *handlers) pageAssistantError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Cache-Control", "private, no-store")
	h.aiJSON(w, status, map[string]string{"error": code})
}

func (h *handlers) pageAssistantItem(w http.ResponseWriter, r *http.Request) (content.Service, *content.Item) {
	id := chi.URLParam(r, "content")
	if h.studioAccess(r.Context(), id) != nil {
		h.pageAssistantError(w, 403, "forbidden")
		return nil, nil
	}
	svc, ok := kernel.Get[content.Service](h.d, kernel.ServiceContent)
	if !ok {
		h.pageAssistantError(w, 503, "unavailable")
		return nil, nil
	}
	it, err := svc.Get(r.Context(), id)
	if err != nil {
		h.pageAssistantError(w, 404, "not_found")
		return nil, nil
	}
	if len(it.Body) > 100000 {
		h.pageAssistantError(w, 422, "oversize")
		return nil, nil
	}
	return svc, it
}

func assistantCanApply(r *http.Request, it *content.Item) bool {
	return auth.Can(r.Context(), "content.edit") && ((it.Status != "published" && it.Status != "scheduled") || auth.Can(r.Context(), "content.publish"))
}

func (h *handlers) pageAssistantMetadata(w http.ResponseWriter, r *http.Request, it *content.Item, saved bool) {
	fields := []string{"title", "excerpt"}
	if it.Template != "builder" {
		fields = append(fields, "body")
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.aiJSON(w, 200, map[string]any{"id": it.ID, "type": it.Type, "title": it.Title, "excerpt": it.Excerpt, "body": it.Body,
		"version": content.EditToken(it), "fields": fields, "can_apply": assistantCanApply(r, it), "published": it.Status == "published",
		"editor_url": "/admin/content/" + it.Type + "/" + it.ID, "saved": saved})
}

func (h *handlers) pageAssistantGet(w http.ResponseWriter, r *http.Request) {
	_, it := h.pageAssistantItem(w, r)
	if it != nil {
		h.pageAssistantMetadata(w, r, it, false)
	}
}

func (h *handlers) pageAssistantApply(w http.ResponseWriter, r *http.Request) {
	svc, it := h.pageAssistantItem(w, r)
	if it == nil {
		return
	}
	if !assistantCanApply(r, it) {
		h.pageAssistantError(w, 403, "forbidden")
		return
	}
	var in struct {
		Version     string `json:"version"`
		Field       string `json:"field"`
		Previous    string `json:"previous"`
		Replacement string `json:"replacement"`
		Reviewed    bool   `json:"reviewed"`
	}
	if studioRead(w, r, &in) != nil {
		h.pageAssistantError(w, 400, "invalid")
		return
	}
	if !in.Reviewed || len(in.Previous) > 100000 || len(in.Replacement) > 100000 {
		h.pageAssistantError(w, 422, "invalid")
		return
	}
	if in.Version == "" || in.Version != content.EditToken(it) {
		h.pageAssistantError(w, 409, "conflict")
		return
	}
	change := content.InputFromItem(it)
	switch in.Field {
	case "title":
		if in.Previous != it.Title || strings.TrimSpace(in.Replacement) == "" || len(in.Replacement) > 2000 {
			h.pageAssistantError(w, 422, "invalid")
			return
		}
		change.Title = plainText.Sanitize(in.Replacement)
	case "excerpt":
		if in.Previous != it.Excerpt || len(in.Replacement) > 10000 {
			h.pageAssistantError(w, 422, "invalid")
			return
		}
		change.Excerpt = plainText.Sanitize(in.Replacement)
	case "body":
		if it.Template == "builder" {
			h.pageAssistantError(w, 422, "builder")
			return
		}
		if in.Previous == "" || strings.Count(it.Body, in.Previous) != 1 {
			h.pageAssistantError(w, 422, "selection")
			return
		}
		change.Body = strings.Replace(it.Body, in.Previous, in.Replacement, 1)
		if len(change.Body) > 100000 {
			h.pageAssistantError(w, 422, "oversize")
			return
		}
	default:
		h.pageAssistantError(w, 422, "invalid")
		return
	}
	updater, ok := svc.(content.AtomicUpdater)
	if !ok {
		h.pageAssistantError(w, 503, "unavailable")
		return
	}
	saved, err := updater.UpdateAtomic(r.Context(), it.ID, change, in.Version, nil)
	if errors.Is(err, content.ErrConflict) {
		h.pageAssistantError(w, 409, "conflict")
		return
	}
	if errors.Is(err, content.ErrForbidden) {
		h.pageAssistantError(w, 403, "forbidden")
		return
	}
	if err != nil {
		var validation *content.ValidationError
		if errors.As(err, &validation) {
			h.pageAssistantError(w, 422, "invalid")
		} else {
			h.d.Log.Error("admin: page assistant save failed", "err", err)
			h.pageAssistantError(w, 500, "unavailable")
		}
		return
	}
	h.d.Audit.Record(r.Context(), "page_assistant.apply", "content", it.ID, map[string]string{"field": in.Field})
	h.pageAssistantMetadata(w, r, saved, true)
}
