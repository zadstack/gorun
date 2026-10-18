package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

type adminHandlers struct {
	svc *service
	tpl *httpx.Templates
	d   *kernel.Deps
}
type editorState struct {
	Draft    *Draft             `json:"draft"`
	Defaults map[string]Section `json:"defaults"`
	Labels   map[string]string  `json:"labels"`
}

func registerAdmin(d *kernel.Deps, svc *service) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return fmt.Errorf("builder: admin templates unavailable")
	}
	h := &adminHandlers{svc: svc, tpl: tpl, d: d}
	r := d.Router.Admin.With(auth.RequirePerm("content.edit"))
	r.Get("/builder/{id}", h.editor)
	r.Post("/builder/{id}/save", h.save)
	r.Post("/builder/{id}/preview", h.preview)
	r.Post("/builder/{id}/reset", h.reset)
	r.Get("/builder/{id}/revisions/{rev}/preview", h.revisionPreview)
	restore := r.With(auth.RequireRecentAuth)
	restore.Get("/builder/{id}/revisions/{rev}/restore", h.restoreForm)
	restore.Post("/builder/{id}/revisions/{rev}/restore", h.restoreSubmit)
	r.With(auth.RequirePerm("content.publish")).Post("/builder/{id}/publish", h.publish)
	return nil
}
func (h *adminHandlers) editor(w http.ResponseWriter, r *http.Request) {
	draft, err := h.svc.Load(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	cs, err := h.svc.content()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	item, err := cs.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	locale := i18n.Locale(r.Context())
	labels := map[string]string{}
	for _, key := range editorKeys {
		labels[key] = h.d.I18n.T(locale, "builder."+key)
	}
	defaults := map[string]Section{}
	for _, spec := range Catalog(locale) {
		defaults[spec.Kind] = DefaultSection(spec.Kind, locale)
	}
	revisions, err := h.svc.ListRevisions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "admin", "pages/builder/editor.html", struct {
		ID              string
		State           editorState
		Catalog         []BlockSpec
		CanPublish      bool
		HasExistingBody bool
		Revisions       []LayoutRevision
	}{chi.URLParam(r, "id"), editorState{draft, defaults, labels}, Catalog(locale), auth.Can(r.Context(), "content.publish"), item.Body != "" && (!draft.HasPublished || draft.ContentChanged), revisions})
}

var editorKeys = []string{"title", "text", "image", "image_alt", "link", "link_label", "price", "variant", "hidden", "drag", "up", "down", "duplicate", "remove", "add_item", "remove_item", "saving", "saved", "error", "conflict", "published", "confirm_publish", "confirm_remove", "empty", "item", "section", "preview_error", "hero_scene", "scene_off", "scene_orbit", "scene_ribbons", "scene_blocks", "scene_help", "layout_default", "layout_split", "layout_centered", "unsaved", "confirm_reset", "discarded", "source", "manual", "published_posts", "feed_help", "import_error", "importing", "history", "history_empty", "history_preview", "history_restore", "history_kind_save", "history_kind_publish", "history_kind_restore", "history_kind_reset", "history_sections"}

type saveRequest struct {
	Document      Document `json:"document"`
	BaseUpdatedAt string   `json:"base_updated_at" format:"-"`
}

func decodeBuilder(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("extra request data")
	}
	return nil
}
func builderJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (h *adminHandlers) save(w http.ResponseWriter, r *http.Request) {
	var req saveRequest
	if decodeBuilder(w, r, &req) != nil {
		builderJSON(w, 400, map[string]string{"error": "builder.error"})
		return
	}
	draft, err := h.svc.Save(r.Context(), chi.URLParam(r, "id"), req.Document, req.BaseUpdatedAt)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	builderJSON(w, 200, draft)
}
func (h *adminHandlers) publish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UpdatedAt string `json:"updated_at"`
	}
	if decodeBuilder(w, r, &req) != nil {
		builderJSON(w, 400, map[string]string{"error": "builder.error"})
		return
	}
	if err := h.svc.Publish(r.Context(), chi.URLParam(r, "id"), req.UpdatedAt); err != nil {
		h.fail(w, r, err)
		return
	}
	draft, err := h.svc.Load(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	builderJSON(w, 200, draft)
}
func (h *adminHandlers) preview(w http.ResponseWriter, r *http.Request) {
	item, err := h.svc.editable(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var req struct {
		Document Document `json:"document"`
	}
	if decodeBuilder(w, r, &req) != nil {
		http.Error(w, "Invalid document", 400)
		return
	}
	html, err := Render(req.Document)
	if err != nil {
		http.Error(w, "Invalid document", 422)
		return
	}
	previewItem := *item
	previewItem.Body = html
	options, themed, err := previewOptions(h.d, r.Context(), item.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if themed {
		renderer, ok := kernel.Get[interface {
			RenderBuilderThemePreview(context.Context, *content.Item, theme.BuilderPreviewOptions) (string, error)
		}](h.d, kernel.ServiceTheme)
		if ok {
			html, err = renderer.RenderBuilderThemePreview(r.Context(), &previewItem, options)
			if err != nil {
				h.d.Log.Error("builder: themed preview", "err", err)
				http.Error(w, "Preview unavailable", 422)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' https: data:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; base-uri 'none'; sandbox")
			w.Header().Set("Cache-Control", "no-store")
			if _, err := w.Write([]byte(html)); err != nil {
				h.d.Log.Debug("builder: preview write", "err", err)
			}
			return
		}
	}
	if renderer, ok := kernel.Get[interface {
		RenderBuilderBody(context.Context, *content.Item) (string, error)
	}](h.d, kernel.ServiceTheme); ok {
		html, err = renderer.RenderBuilderBody(r.Context(), &previewItem)
		if err != nil {
			h.d.Log.Error("builder: dynamic preview", "err", err)
			http.Error(w, "Preview unavailable", 422)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' https: data:; style-src 'unsafe-inline'; form-action 'none'; sandbox")
	w.Header().Set("Cache-Control", "no-store")
	locale := item.Locale
	if locale == "" {
		locale = h.d.Settings.GetString(r.Context(), "site.locale", i18n.DefaultLocale)
	}
	if !slices.Contains(i18n.Supported, locale) {
		locale = i18n.DefaultLocale
	}
	dir := "ltr"
	if locale == "ar" {
		dir = "rtl"
	}
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="%s" dir="%s"><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src https: http: data:; style-src 'unsafe-inline'; form-action 'none'"><meta name="viewport" content="width=device-width,initial-scale=1"><style>%s</style><body>%s</body></html>`, locale, dir, builderCSS+previewBrand(h.d, r.Context(), chi.URLParam(r, "id")), html)
}
func (h *adminHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := 422
	if errors.Is(err, ErrConflict) {
		status = 409
	}
	if errors.Is(err, content.ErrNotFound) {
		status = 404
	}
	if errors.Is(err, content.ErrForbidden) {
		status = 403
	}
	h.d.Log.Warn("builder request rejected", "error", err)
	builderJSON(w, status, map[string]string{"error": "builder.error"})
}

func (h *adminHandlers) reset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UpdatedAt            string  `json:"updated_at"`
		ExpectedContentToken *string `json:"expected_content_token"`
	}
	if decodeBuilder(w, r, &req) != nil {
		builderJSON(w, 400, map[string]string{"error": "builder.error"})
		return
	}
	if err := h.svc.ResetGuarded(r.Context(), chi.URLParam(r, "id"), req.UpdatedAt, req.ExpectedContentToken); err != nil {
		h.fail(w, r, err)
		return
	}
	draft, err := h.svc.Load(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "builder.discarded")
	builderJSON(w, 200, draft)
}
