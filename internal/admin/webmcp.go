package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
)

// WebMCP: the admin registers in-page tools with document.modelContext
// (web/admin/static/webmcp.js) so an agent in the user's browser can work
// with GoRun through the user's own session. These endpoints are the tools'
// server side. They act as the signed-in user, inside the existing
// permission checks, and never publish: a draft is the only write.

const (
	webmcpTitleMax = 300
	webmcpBodyMax  = 64 << 10
)

type webmcpPage struct {
	Label   string `json:"label"`
	Href    string `json:"href"`
	Section string `json:"section,omitempty"`
}

type webmcpType struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type webmcpManifest struct {
	Pages       []webmcpPage `json:"pages"`
	ContentType []webmcpType `json:"content_types"`
	CanDraft    bool         `json:"can_draft"`
}

func (h *handlers) webmcpRoutes() {
	r := h.d.Router.Admin
	r.Get("/x/webmcp/manifest", h.webmcpManifest)
	r.Get("/x/webmcp/search", h.webmcpSearch)
	r.Post("/x/webmcp/drafts", h.webmcpDraft)
}

// webmcpManifest lists the admin pages and content types the user may use.
func (h *handlers) webmcpManifest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := webmcpManifest{Pages: []webmcpPage{}, ContentType: []webmcpType{}}
	for _, n := range h.d.Admin.Nav(ctx) {
		if n.Href == "" {
			continue
		}
		out.Pages = append(out.Pages, webmcpPage{Label: h.tr(r, n.LabelKey), Href: n.Href, Section: n.Group})
	}
	if cs, ok := h.contentService(); ok && can(r, "content.create") {
		out.CanDraft = true
		types, err := cs.Types(ctx)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		locale := i18n.Locale(ctx)
		for _, t := range types {
			name := t.Singular[locale]
			if name == "" {
				name = t.Slug
			}
			out.ContentType = append(out.ContentType, webmcpType{Slug: t.Slug, Name: name})
		}
	}
	h.aiJSON(w, http.StatusOK, out)
}

// webmcpSearch is the global admin search as JSON.
func (h *handlers) webmcpSearch(w http.ResponseWriter, r *http.Request) {
	res, err := h.search(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	hits := func(in []searchHit) []map[string]string {
		out := make([]map[string]string, 0, len(in))
		for _, h := range in {
			out = append(out, map[string]string{"title": h.Title, "detail": h.Sub, "href": h.Href})
		}
		return out
	}
	h.aiJSON(w, http.StatusOK, map[string]any{"query": res.Query, "too_short": res.TooShort,
		"content": hits(res.Content), "users": hits(res.Users), "pages": hits(res.Pages)})
}

type webmcpDraftInput struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// webmcpDraft creates a draft as the user. Status is always draft; the
// content service applies sanitizing, ownership and type validation.
func (h *handlers) webmcpDraft(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, code, msg string) {
		h.aiJSON(w, status, map[string]any{"ok": false, "code": code, "error": msg})
	}
	cs, ok := h.contentService()
	if !ok || !can(r, "content.create") {
		fail(http.StatusForbidden, "forbidden", "content.create permission required")
		return
	}
	var in webmcpDraftInput
	dec := json.NewDecoder(io.LimitReader(r.Body, webmcpBodyMax+4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(http.StatusUnprocessableEntity, "validation_failed", "body must be JSON {type, title, body}")
		return
	}
	in.Type, in.Title = strings.TrimSpace(in.Type), strings.TrimSpace(in.Title)
	switch {
	case in.Type == "":
		fail(http.StatusUnprocessableEntity, "validation_failed", "type is required")
		return
	case in.Title == "" || utf8.RuneCountInString(in.Title) > webmcpTitleMax:
		fail(http.StatusUnprocessableEntity, "validation_failed", "title is required (at most 300 characters)")
		return
	case len(in.Body) > webmcpBodyMax:
		fail(http.StatusUnprocessableEntity, "validation_failed", "body is larger than 64 KB")
		return
	}
	item, err := cs.Create(r.Context(), content.Input{Type: in.Type, Title: in.Title, Body: in.Body, Status: content.Status("draft")})
	if err != nil {
		var ve *content.ValidationError
		if errors.As(err, &ve) || errors.Is(err, content.ErrNotFound) {
			fail(http.StatusUnprocessableEntity, "validation_failed", "the content type does not exist or the draft is invalid")
			return
		}
		if errors.Is(err, content.ErrForbidden) {
			fail(http.StatusForbidden, "forbidden", "you may not create this content type")
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.aiJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": item.ID, "status": item.Status,
		"href": "/admin/content/" + item.Type + "/" + item.ID})
}
