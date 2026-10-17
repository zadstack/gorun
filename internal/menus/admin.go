package menus

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/taxonomy"
)

const searchLimit = 10

type adminHandlers struct {
	svc       *service
	tpl       *httpx.Templates
	log       *slog.Logger
	locations func(ctx context.Context) []string
}

type menuForm struct {
	Name     string `form:"name"`
	Slug     string `form:"slug"`
	Location string `form:"location"`
	Locale   string `form:"locale"`
}

type menuRow struct {
	Menu
	Count int
}

type listData struct {
	Menus     []menuRow
	Locations []string
	Form      menuForm
	Fields    map[string]string
}

func countItems(items []Item) int {
	n := len(items)
	for _, it := range items {
		n += countItems(it.Children)
	}
	return n
}

func (h *adminHandlers) renderList(w http.ResponseWriter, r *http.Request, status int, f menuForm, fields map[string]string) {
	menus, err := h.svc.List(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := listData{Locations: h.locations(r.Context()), Form: f, Fields: fields}
	for _, m := range menus {
		d.Menus = append(d.Menus, menuRow{Menu: m, Count: countItems(m.Items)})
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/menus/list.html", d)
}

func (h *adminHandlers) list(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, http.StatusOK, menuForm{}, nil)
}

func (h *adminHandlers) create(w http.ResponseWriter, r *http.Request) {
	var f menuForm
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	m, err := h.svc.Save(r.Context(), Menu{Name: f.Name, Slug: f.Slug, Location: f.Location, Locale: f.Locale})
	var ve *ValidationError
	if errors.As(err, &ve) {
		h.renderList(w, r, http.StatusUnprocessableEntity, f, ve.Fields)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "menus.flash.created")
	http.Redirect(w, r, "/admin/menus/"+m.ID, http.StatusSeeOther)
}

// editorItem is one row of the Alpine editor (a flattened tree with depth).
type editorItem struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	RefID     string `json:"ref_id"`
	URL       string `json:"url"`
	CSSClass  string `json:"css_class"`
	OpenInNew bool   `json:"open_in_new"`
	Depth     int    `json:"depth"`
	Hint      string `json:"hint"`
}

type editData struct {
	EditToken string
	Menu      *Menu
	Form      menuForm
	Locations []string
	Locales   []string // public-site locales a menu can be limited to
	Items     []editorItem
	Fields    map[string]string
	ItemError bool
	Unsaved   bool
}

// editorItems flattens the tree, adding a hint (resolved title and URL) for
// content, term and archive items.
func (h *adminHandlers) editorItems(ctx context.Context, items []Item, depth int) []editorItem {
	var out []editorItem
	for _, it := range items {
		e := editorItem{ID: it.ID, Label: it.Label, Kind: it.Kind, RefID: it.RefID, URL: it.URL, CSSClass: it.CSSClass,
			OpenInNew: it.OpenInNew, Depth: depth}
		if it.Kind != KindCustom {
			cp := it
			cp.Children = nil
			if h.svc.resolveOne(ctx, &cp) {
				e.Hint = strings.TrimSpace(cp.Label + " · " + cp.URL)
			}
		}
		out = append(out, e)
		out = append(out, h.editorItems(ctx, it.Children, depth+1)...)
	}
	return out
}

func (h *adminHandlers) renderEdit(w http.ResponseWriter, r *http.Request, status int, m *Menu, f menuForm, items []Item, fields map[string]string) {
	d := editData{Menu: m, Form: f, Locations: h.locations(r.Context()), Locales: i18n.Supported, Items: h.editorItems(r.Context(), items, 0), Fields: fields}
	d.EditToken = menuEditToken(m)
	if token := submittedMenuToken(r); token != nil {
		d.EditToken = *token
	}
	if d.Items == nil {
		d.Items = []editorItem{}
	}
	for k := range fields {
		if strings.HasPrefix(k, "items") {
			d.ItemError = true
		}
	}
	d.Unsaved = d.ItemError || status == http.StatusConflict
	h.tpl.RenderWith(w, r, status, "admin", "pages/menus/edit.html", d)
}

func (h *adminHandlers) edit(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.getRaw(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.notFoundOr(w, r, err)
		return
	}
	h.renderEdit(w, r, http.StatusOK, m, menuForm{Name: m.Name, Slug: m.Slug, Location: m.Location, Locale: m.Locale}, m.Items, nil)
}

func (h *adminHandlers) update(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.getRaw(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.notFoundOr(w, r, err)
		return
	}
	var f menuForm
	if err := httpx.Bind(r, &f); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	_, err = h.svc.saveGuarded(r.Context(), Menu{ID: m.ID, Name: f.Name, Slug: f.Slug, Location: f.Location, Locale: f.Locale}, submittedMenuToken(r))
	if errors.Is(err, errEditConflict) {
		h.renderEdit(w, r, http.StatusConflict, m, f, m.Items, map[string]string{"conflict": "menus.error.conflict"})
		return
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		h.renderEdit(w, r, http.StatusUnprocessableEntity, m, f, m.Items, ve.Fields)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "menus.flash.saved")
	http.Redirect(w, r, "/admin/menus/"+m.ID, http.StatusSeeOther)
}

func (h *adminHandlers) saveItems(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.getRaw(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.notFoundOr(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	var items []Item
	if err := json.Unmarshal([]byte(r.PostForm.Get("items")), &items); err != nil {
		h.renderEdit(w, r, http.StatusUnprocessableEntity, m, menuForm{Name: m.Name, Slug: m.Slug, Location: m.Location, Locale: m.Locale},
			m.Items, map[string]string{"items": "menus.error.items_invalid"})
		return
	}
	if items == nil {
		items = []Item{}
	}
	err = h.svc.saveItemsGuarded(r.Context(), m.ID, items, submittedMenuToken(r))
	if errors.Is(err, errEditConflict) {
		h.renderEdit(w, r, http.StatusConflict, m, menuForm{Name: m.Name, Slug: m.Slug, Location: m.Location, Locale: m.Locale}, items, map[string]string{"conflict": "menus.error.conflict"})
		return
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		h.renderEdit(w, r, http.StatusUnprocessableEntity, m, menuForm{Name: m.Name, Slug: m.Slug, Location: m.Location, Locale: m.Locale}, items, ve.Fields)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "menus.flash.items_saved")
	http.Redirect(w, r, "/admin/menus/"+m.ID, http.StatusSeeOther)
}

func (h *adminHandlers) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.notFoundOr(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "menus.flash.deleted")
	http.Redirect(w, r, "/admin/menus", http.StatusSeeOther)
}

// searchResult is one addable item in the editor's search panel.
type searchResult struct {
	Kind, RefID, Label, Hint string
}

type searchData struct {
	Kind        string
	Q           string
	Results     []searchResult
	Unavailable bool
}

// search lists content (published), terms or archives matching q.
func (h *adminHandlers) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	d := searchData{Kind: r.URL.Query().Get("kind"), Q: q}
	switch d.Kind {
	case KindTerm:
		d.Results, d.Unavailable = h.searchTerms(ctx, q)
	case KindArchive:
		d.Results, d.Unavailable = h.searchArchives(ctx, q)
	default:
		d.Kind = KindContent
		d.Results, d.Unavailable = h.searchContent(ctx, q)
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "", "pages/menus/search.html", d)
}

func (h *adminHandlers) searchContent(ctx context.Context, q string) ([]searchResult, bool) {
	svc, ok := h.svc.content()
	if !ok {
		return nil, true
	}
	page, err := svc.Query(ctx, content.Query{Search: q, Status: "published", PerPage: searchLimit,
		Order: []content.Order{{By: "updated_at", Dir: "desc"}}})
	if err != nil {
		h.log.Error("menus: search content", "err", err)
		return nil, false
	}
	var out []searchResult
	for _, it := range page.Items {
		out = append(out, searchResult{Kind: KindContent, RefID: it.ID, Label: it.Title, Hint: it.Type + " · " + ContentPath(&it)})
	}
	return out, false
}

func (h *adminHandlers) searchTerms(ctx context.Context, q string) ([]searchResult, bool) {
	svc, ok := h.svc.taxonomy()
	if !ok {
		return nil, true
	}
	taxes, err := svc.Taxonomies(ctx)
	if err != nil {
		h.log.Error("menus: search terms", "err", err)
		return nil, false
	}
	var out []searchResult
	for _, tx := range taxes {
		terms, _, err := svc.Terms(ctx, tx.Slug, taxonomy.TermQuery{Search: q, PerPage: searchLimit})
		if err != nil {
			h.log.Error("menus: search terms", "taxonomy", tx.Slug, "err", err)
			continue
		}
		for _, t := range terms {
			out = append(out, searchResult{Kind: KindTerm, RefID: t.ID, Label: t.Name, Hint: localized(tx.Singular, i18n.Locale(ctx)) + " · " + TermPath(&t)})
		}
	}
	return out, false
}

func (h *adminHandlers) searchArchives(ctx context.Context, q string) ([]searchResult, bool) {
	svc, ok := h.svc.content()
	if !ok {
		return nil, true
	}
	types, err := svc.Types(ctx)
	if err != nil {
		h.log.Error("menus: search archives", "err", err)
		return nil, false
	}
	var out []searchResult
	for _, t := range types {
		label := localized(t.Plural, i18n.Locale(ctx))
		if !t.HasArchive || !t.Public || (q != "" && !strings.Contains(strings.ToLower(label+" "+t.Slug), strings.ToLower(q))) {
			continue
		}
		out = append(out, searchResult{Kind: KindArchive, RefID: t.Slug, Label: label, Hint: ArchivePath(&t)})
	}
	return out, false
}

func (h *adminHandlers) notFoundOr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	h.fail(w, r, err)
}

func (h *adminHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("menus: admin", "err", err, "path", httpx.RedactPath(r.URL.Path))
	h.tpl.RenderError(w, r, http.StatusInternalServerError)
}
