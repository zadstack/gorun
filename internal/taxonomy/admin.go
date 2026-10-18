package taxonomy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

type adminHandlers struct {
	d   *kernel.Deps
	svc *service
	tpl *httpx.Templates
}

// registerAdmin mounts the taxonomy screens and the content-editor picker
// fragments (/admin/x/taxonomy/...).
func registerAdmin(d *kernel.Deps, svc *service) {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		d.Log.Warn("taxonomy: admin templates unavailable; admin screens not mounted")
		return
	}
	h := &adminHandlers{d: d, svc: svc, tpl: tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "taxonomy", LabelKey: "taxonomy.nav", Icon: "tags", Href: "/admin/taxonomies",
		Permission: "taxonomy.manage", Group: "structure", Position: 20})
	r := d.Router.Admin
	manage, view := auth.RequirePerm("taxonomy.manage"), auth.RequirePerm("content.view")
	r.With(manage).Get("/taxonomies", h.list)
	r.With(manage).Post("/taxonomies", h.saveTaxonomy)
	r.With(manage).Get("/taxonomies/{tax}", h.terms)
	r.With(manage).Post("/taxonomies/{tax}/delete", h.deleteTaxonomy)
	r.With(manage).Post("/taxonomies/{tax}/terms", h.saveTerm)
	r.With(manage).Post("/taxonomies/{tax}/terms/{id}/delete", h.deleteTerm)
	r.With(view).Get("/x/taxonomy/{tax}/terms", h.suggest)
	r.With(view).Get("/x/taxonomy/{tax}/picker", h.picker)
}

func (h *adminHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		h.tpl.RenderError(w, r, http.StatusNotFound)
	default:
		h.d.Log.Error("taxonomy admin", "err", err, "path", r.URL.Path)
		h.tpl.RenderError(w, r, http.StatusInternalServerError)
	}
}

func localized(m map[string]string, locale, def string) string {
	for _, k := range []string{locale, "en", "ar"} {
		if v := m[k]; v != "" {
			return v
		}
	}
	return def
}

type taxRow struct {
	Slug, Name                   string
	Hierarchical, System, Public bool
	Types                        []string
	Terms                        int
}

type choice struct {
	Value, Label string
	Selected     bool
}

type taxForm struct {
	Taxonomy  Taxonomy
	IsNew     bool
	Types     []choice
	Errors    map[string]string
	FormError string
}

type listView struct {
	Rows []taxRow
	Form taxForm
}

func (h *adminHandlers) contentTypes(ctx context.Context, selected []string) []choice {
	cs := h.svc.content()
	if cs == nil {
		return nil
	}
	types, err := cs.Types(ctx)
	if err != nil {
		return nil
	}
	locale := i18n.Locale(ctx)
	out := make([]choice, 0, len(types))
	for _, t := range types {
		out = append(out, choice{Value: t.Slug, Label: localized(t.Name, locale, t.Slug), Selected: slices.Contains(selected, t.Slug)})
	}
	return out
}

func (h *adminHandlers) list(w http.ResponseWriter, r *http.Request) {
	h.renderList(w, r, http.StatusOK, taxForm{Taxonomy: Taxonomy{Public: true}, IsNew: true})
}

func (h *adminHandlers) renderList(w http.ResponseWriter, r *http.Request, status int, form taxForm) {
	ctx := r.Context()
	taxes, err := h.svc.Taxonomies(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	locale := i18n.Locale(ctx)
	v := listView{Form: form}
	for _, t := range taxes {
		_, n, _ := h.svc.Terms(ctx, t.Slug, TermQuery{PerPage: 1})
		v.Rows = append(v.Rows, taxRow{Slug: t.Slug, Name: localized(t.Name, locale, t.Slug), Hierarchical: t.Hierarchical,
			System: t.System, Public: t.Public, Types: t.Types, Terms: n})
	}
	if v.Form.Errors == nil {
		v.Form.Errors = map[string]string{}
	}
	v.Form.Types = h.contentTypes(ctx, form.Taxonomy.Types)
	h.tpl.RenderWith(w, r, status, "admin", "pages/taxonomy/list.html", v)
}

func taxonomyFromForm(form url.Values) Taxonomy {
	bilingual := func(key string) map[string]string {
		m := map[string]string{}
		for _, loc := range i18n.Supported {
			if v := strings.TrimSpace(form.Get(key + "_" + loc)); v != "" {
				m[loc] = v
			}
		}
		return m
	}
	on := func(k string) bool { v := form.Get(k); return v == "1" || v == "on" || v == "true" }
	types := []string{}
	for _, t := range form["types"] {
		if t = strings.TrimSpace(t); t != "" {
			types = append(types, t)
		}
	}
	return Taxonomy{Slug: strings.TrimSpace(form.Get("slug")), Name: bilingual("name"), Singular: bilingual("singular"),
		Hierarchical: on("hierarchical"), Public: on("public"), Types: types}
}

// saveTaxonomy creates a taxonomy (list page form) or updates one (terms
// page settings form, with "existing" set).
func (h *adminHandlers) saveTaxonomy(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	t := taxonomyFromForm(r.PostForm)
	existing := r.PostForm.Get("existing") == "1"
	if !existing {
		if _, err := h.svc.Get(r.Context(), t.Slug); err == nil {
			h.renderList(w, r, http.StatusUnprocessableEntity, taxForm{Taxonomy: t, IsNew: true,
				Errors: map[string]string{"slug": "taxonomy.validation.exists"}, FormError: "taxonomy.flash.invalid"})
			return
		}
	}
	err := h.svc.Save(r.Context(), t)
	var ve *content.ValidationError
	switch {
	case err == nil:
		httpx.SetFlash(w, "success", "taxonomy.flash.saved")
		http.Redirect(w, r, "/admin/taxonomies/"+url.PathEscape(t.Slug), http.StatusSeeOther)
	case errors.As(err, &ve) && !existing:
		h.renderList(w, r, http.StatusUnprocessableEntity, taxForm{Taxonomy: t, IsNew: true, Errors: ve.Fields, FormError: "taxonomy.flash.invalid"})
	case errors.As(err, &ve):
		httpx.SetFlash(w, "error", "taxonomy.flash.invalid")
		http.Redirect(w, r, "/admin/taxonomies/"+url.PathEscape(t.Slug), http.StatusSeeOther)
	default:
		h.fail(w, r, err)
	}
}

func (h *adminHandlers) deleteTaxonomy(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Delete(r.Context(), chi.URLParam(r, "tax"))
	switch {
	case err == nil:
		httpx.SetFlash(w, "success", "taxonomy.flash.deleted")
	case errors.Is(err, ErrSystem):
		httpx.SetFlash(w, "error", "taxonomy.error.system")
	default:
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/taxonomies", http.StatusSeeOther)
}

// --- terms ---

type termRow struct {
	Term
	Depth   int
	Indent  string
	EditURL string
}

type termsView struct {
	Taxonomy     *Taxonomy
	Name         string
	Rows         []termRow
	Edit         Term
	Parents      []choice
	Errors       map[string]string
	FormError    string
	Settings     taxForm
	Search       string
	TermsBaseURL string
}

var indentClass = []string{"", "ps-4", "ps-8", "ps-12"}

// tree orders terms depth-first (children under parents) with depths.
func tree(terms []Term) []termRow {
	children := map[string][]Term{}
	ids := map[string]bool{}
	for _, t := range terms {
		ids[t.ID] = true
	}
	for _, t := range terms {
		parent := t.ParentID
		if !ids[parent] {
			parent = ""
		}
		children[parent] = append(children[parent], t)
	}
	var out []termRow
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, t := range children[parent] {
			out = append(out, termRow{Term: t, Depth: depth, Indent: indentClass[min(depth, len(indentClass)-1)]})
			if depth < 50 {
				walk(t.ID, depth+1)
			}
		}
	}
	walk("", 0)
	return out
}

func (h *adminHandlers) terms(w http.ResponseWriter, r *http.Request) {
	tax, err := h.svc.Get(r.Context(), chi.URLParam(r, "tax"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	edit := Term{Taxonomy: tax.Slug}
	if id := r.URL.Query().Get("edit"); id != "" {
		t, err := h.svc.GetTerm(r.Context(), id)
		if err != nil || t.Taxonomy != tax.Slug {
			h.fail(w, r, ErrNotFound)
			return
		}
		edit = *t
	}
	h.renderTerms(w, r, http.StatusOK, tax, edit, nil, "")
}

func (h *adminHandlers) renderTerms(w http.ResponseWriter, r *http.Request, status int, tax *Taxonomy, edit Term, errs map[string]string, formErr string) {
	ctx := r.Context()
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	all, _, err := h.svc.Terms(ctx, tax.Slug, TermQuery{PerPage: maxPerPage, Search: search})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	base := "/admin/taxonomies/" + url.PathEscape(tax.Slug)
	v := termsView{Taxonomy: tax, Name: localized(tax.Name, i18n.Locale(ctx), tax.Slug), Edit: edit, Errors: errs, FormError: formErr,
		Search: search, TermsBaseURL: base}
	if v.Errors == nil {
		v.Errors = map[string]string{}
	}
	rows := tree(all)
	if search != "" { // a filtered list has broken parent chains: show it flat
		rows = rows[:0]
		for _, t := range all {
			rows = append(rows, termRow{Term: t})
		}
	}
	for i := range rows {
		rows[i].EditURL = base + "?edit=" + url.QueryEscape(rows[i].ID)
	}
	v.Rows = rows
	if tax.Hierarchical {
		full := all
		if search != "" {
			full, _, _ = h.svc.Terms(ctx, tax.Slug, TermQuery{PerPage: maxPerPage})
		}
		for _, t := range tree(full) {
			if t.ID == edit.ID {
				continue
			}
			v.Parents = append(v.Parents, choice{Value: t.ID, Label: strings.Repeat("— ", t.Depth) + t.Name, Selected: t.ID == edit.ParentID})
		}
	}
	v.Settings = taxForm{Taxonomy: *tax, Types: h.contentTypes(ctx, tax.Types), Errors: map[string]string{}}
	h.tpl.RenderWith(w, r, status, "admin", "pages/taxonomy/terms.html", v)
}

func (h *adminHandlers) saveTerm(w http.ResponseWriter, r *http.Request) {
	tax, err := h.svc.Get(r.Context(), chi.URLParam(r, "tax"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	f := r.PostForm
	t := Term{ID: strings.TrimSpace(f.Get("id")), Taxonomy: tax.Slug, Name: f.Get("name"), Slug: f.Get("slug"),
		Description: strings.TrimSpace(f.Get("description")), ParentID: f.Get("parent_id")}
	if t.ID != "" {
		cur, err := h.svc.GetTerm(r.Context(), t.ID)
		if err != nil || cur.Taxonomy != tax.Slug {
			h.fail(w, r, ErrNotFound)
			return
		}
		t.Meta = cur.Meta
	}
	_, err = h.svc.SaveTerm(r.Context(), t)
	var ve *content.ValidationError
	switch {
	case err == nil:
		httpx.SetFlash(w, "success", "taxonomy.flash.term_saved")
		http.Redirect(w, r, "/admin/taxonomies/"+url.PathEscape(tax.Slug), http.StatusSeeOther)
	case errors.As(err, &ve):
		h.renderTerms(w, r, http.StatusUnprocessableEntity, tax, t, ve.Fields, "taxonomy.flash.invalid")
	default:
		h.fail(w, r, err)
	}
}

func (h *adminHandlers) deleteTerm(w http.ResponseWriter, r *http.Request) {
	tax := chi.URLParam(r, "tax")
	t, err := h.svc.GetTerm(r.Context(), chi.URLParam(r, "id"))
	if err == nil && t.Taxonomy != tax {
		err = ErrNotFound
	}
	if err == nil {
		err = h.svc.DeleteTerm(r.Context(), t.ID)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "taxonomy.flash.term_deleted")
	http.Redirect(w, r, "/admin/taxonomies/"+url.PathEscape(tax), http.StatusSeeOther)
}

// --- editor fragments ---

type pickerView struct {
	Taxonomy *Taxonomy
	Name     string
	Rows     []termRow
	Selected []string
	Selector map[string]bool
}

// picker renders the editor's term picker: a checkbox tree for
// hierarchical taxonomies, an Alpine tag input with HTMX suggestions for
// flat ones. It always sends terms_present[tax] so the editor knows the
// picker loaded.
func (h *adminHandlers) picker(w http.ResponseWriter, r *http.Request) {
	tax, err := h.svc.Get(r.Context(), chi.URLParam(r, "tax"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var selected []string
	for _, s := range strings.Split(r.URL.Query().Get("selected"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			selected = append(selected, s)
		}
	}
	v := pickerView{Taxonomy: tax, Name: localized(tax.Name, i18n.Locale(r.Context()), tax.Slug), Selected: selected, Selector: map[string]bool{}}
	for _, s := range selected {
		v.Selector[s] = true
	}
	if tax.Hierarchical {
		all, _, err := h.svc.Terms(r.Context(), tax.Slug, TermQuery{PerPage: maxPerPage})
		if err != nil {
			h.fail(w, r, err)
			return
		}
		v.Rows = tree(all)
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "", "pages/taxonomy/picker.html", v)
}

// suggest renders tag suggestions for the flat-taxonomy tag input.
func (h *adminHandlers) suggest(w http.ResponseWriter, r *http.Request) {
	tax := chi.URLParam(r, "tax")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var terms []Term
	if q != "" {
		var err error
		if terms, _, err = h.svc.Terms(r.Context(), tax, TermQuery{Search: q, PerPage: 10}); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "", "pages/taxonomy/suggest.html", map[string]any{"Terms": terms, "Query": q})
}
