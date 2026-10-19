package content

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

const adminPerPage = 20

type listRow struct {
	ID, Title, Slug, Status, UpdatedAt, EditURL string
	Locale, AuthorName, ParentTitle             string
}

type listView struct {
	Type                 *Type
	TypeName, PluralName string
	BaseURL              string
	Rows                 []listRow
	Search, Status       string
	Locale, Author, View string
	Authors              []choice
	Statuses             []Status
	Links                pageLinks
	CanCreate, CanDelete bool
}

func (h *adminHandlers) list(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	qs := r.URL.Query()
	page, _ := strconv.Atoi(qs.Get("page"))
	q := Query{Type: t.Slug, Search: strings.TrimSpace(qs.Get("search")), Status: qs.Get("status"), Page: page, PerPage: adminPerPage,
		Order: []Order{{By: "updated_at", Dir: "desc"}}}
	if q.Status != "" && !slices.Contains(Statuses, Status(q.Status)) {
		q.Status = ""
	}
	view := qs.Get("view")
	if view != "mine" && view != "review" {
		view = ""
	}
	if lang := qs.Get("locale"); lang == "ar" || lang == "en" {
		q.Locale = lang
	}
	u := auth.UserFromContext(ctx)
	if ownOnly(u) || view == "mine" {
		q.AuthorID = u.ID
	} else {
		q.AuthorID = qs.Get("author")
	}
	if view == "mine" {
		q.Status = "draft"
	}
	if view == "review" {
		q.Status = "pending"
	}
	res, err := h.svc.Query(ctx, q)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	locale := i18n.Locale(ctx)
	base := "/admin/content/" + t.Slug
	v := listView{Type: t, TypeName: localized(t.Singular, locale, localized(t.Name, locale, t.Slug)), PluralName: localized(t.Plural, locale, localized(t.Name, locale, t.Slug)),
		BaseURL: base, Search: q.Search, Status: q.Status, Statuses: Statuses, Locale: q.Locale, Author: q.AuthorID, View: view,
		CanCreate: auth.Can(ctx, "content.create"), CanDelete: auth.Can(ctx, "content.delete")}
	if !ownOnly(u) {
		rows, err := h.d.DB.QueryContext(ctx, `SELECT DISTINCT u.id,u.name FROM users u JOIN content c ON c.author_id=u.id WHERE c.type=? ORDER BY u.name,u.id`, t.Slug)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id, name string
				if rows.Scan(&id, &name) == nil {
					if name == "" {
						name = "—"
					}
					v.Authors = append(v.Authors, choice{Value: id, Label: name, Selected: id == q.AuthorID})
				}
			}
		}
	}
	filters := url.Values{}
	for k, val := range map[string]string{"locale": q.Locale, "author": q.AuthorID, "view": view} {
		if val != "" {
			filters.Set(k, val)
		}
	}
	if q.Search != "" {
		filters.Set("search", q.Search)
	}
	if q.Status != "" {
		filters.Set("status", q.Status)
	}
	v.Links = newPageLinks(base, filters, res.Page, res.PerPage, res.Total)
	for _, it := range res.Items {
		authorName := ""
		_ = h.d.DB.QueryRowContext(ctx, `SELECT name FROM users WHERE id=?`, it.AuthorID).Scan(&authorName)
		parentTitle := ""
		if it.ParentID != "" {
			if parent, err := h.svc.Get(ctx, it.ParentID); err == nil {
				if !ownOnly(u) || parent.AuthorID == u.ID {
					parentTitle = parent.Title
				}
			}
		}
		v.Rows = append(v.Rows, listRow{Locale: it.Locale, AuthorName: authorName, ParentTitle: parentTitle, ID: it.ID, Title: it.Title, Slug: it.Slug, Status: string(it.Status), UpdatedAt: it.UpdatedAt,
			EditURL: base + "/" + it.ID})
	}
	h.tpl.Render(w, r, "pages/content/list.html", v)
}

func (h *adminHandlers) bulk(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	hard := r.PostForm.Get("action") == "delete"
	trashed, deleted := 0, 0
	for _, id := range r.PostForm["ids"] {
		it, err := h.svc.Get(r.Context(), id)
		if err != nil || it.Type != t.Slug {
			continue
		}
		purge := hard && it.Status == "trash"
		if err := h.svc.Delete(r.Context(), id, purge); err != nil {
			continue
		}
		if purge {
			deleted++
		} else {
			trashed++
		}
	}
	switch {
	case deleted > 0 && trashed == 0:
		httpx.SetFlash(w, "success", "content.flash.bulk_deleted")
	case trashed > 0 && deleted == 0:
		httpx.SetFlash(w, "success", "content.flash.bulk_trashed")
	case trashed > 0:
		httpx.SetFlash(w, "success", "content.flash.bulk_mixed")
	default:
		httpx.SetFlash(w, "info", "content.flash.bulk_none")
	}
	http.Redirect(w, r, "/admin/content/"+t.Slug, http.StatusSeeOther)
}

// --- editor ---

type pickerView struct{ Taxonomy, URL string }

type seoView struct{ Title, Description, Canonical, Robots, OGImage string }

// mediaView feeds media_picker_slot for the built-in media inputs
// (featured image, OG image). Multiple mirrors fieldView so the slot can
// read it on both.
type mediaView struct {
	Name, ID, Value, Label string
	Multiple, CanPick      bool
}

type editorView struct {
	Type                  *Type
	TypeName              string
	Item                  *Item
	IsNew                 bool
	Action, ListURL       string
	EditToken, TermsToken string
	Errors                FieldErrors
	Fields                []fieldView
	Statuses              []choice
	PublishAtLocal        string
	CanPublish            bool
	CanDelete             bool
	CanPickMedia          bool              // media module present and media.upload held
	Translations          []translationView // one per supported locale; empty for new or trashed items
	CanUnlink             bool              // the item is linked and content.edit is held
	Supports              map[string]bool
	Parent                *pickView
	Templates             []choice
	Authors               []choice
	Pickers               []pickerView
	Revisions             []Revision
	SEO                   seoView
	SEODrift              []string // stored SEO fields that no longer match the page
	RobotsOptions         []string
	Body                  fieldView
	OGImage               mediaView
	TermErrors            []string
	Featured              mediaView
	FormError             string
	BodyDir, Placeholder  string
}

func (h *adminHandlers) newItem(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	it := &Item{Type: t.Slug, Status: "draft", Fields: map[string]any{}, SEO: map[string]any{}, Terms: map[string][]string{}}
	if u := auth.UserFromContext(r.Context()); u != nil {
		it.AuthorID = u.ID
	}
	for _, f := range t.Fields {
		if f.Default != nil {
			it.Fields[f.Key] = f.Default
		}
	}
	h.renderEditor(w, r, http.StatusOK, t, it, true, nil, "")
}

func (h *adminHandlers) editItem(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	it, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err == nil && it.Type != t.Slug {
		err = ErrNotFound
	}
	if err == nil {
		err = checkOwner(r.Context(), it)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderEditor(w, r, http.StatusOK, t, it, false, nil, "")
}

func (h *adminHandlers) renderEditor(w http.ResponseWriter, r *http.Request, status int, t *Type, it *Item, isNew bool, errs FieldErrors, formErr string) {
	ctx := r.Context()
	locale := i18n.Locale(ctx)
	base := "/admin/content/" + t.Slug
	v := editorView{Type: t, TypeName: localized(t.Singular, locale, localized(t.Name, locale, t.Slug)), Item: it, IsNew: isNew, ListURL: base,
		Errors: errs, CanPublish: canPublish(ctx), CanDelete: auth.Can(ctx, "content.delete"), FormError: formErr,
		Supports: map[string]bool{}, PublishAtLocal: localDatetime(it.PublishAt), BodyDir: "auto"}
	if v.Errors == nil {
		v.Errors = FieldErrors{}
	}
	v.Action = base
	if !isNew {
		v.Action = base + "/" + it.ID
		v.EditToken, v.TermsToken = EditToken(it), termsEditToken(it.Terms)
		if status >= 400 && r.PostForm.Has("edit_token") {
			v.EditToken, v.TermsToken = r.PostForm.Get("edit_token"), r.PostForm.Get("terms_token")
		}
	}
	for _, s := range t.Supports {
		v.Supports[s] = true
	}
	for _, st := range Statuses {
		if st == "trash" && it.Status != "trash" {
			continue
		}
		if isLive(st) && !v.CanPublish && it.Status != st {
			continue
		}
		v.Statuses = append(v.Statuses, choice{Value: string(st), Selected: st == it.Status})
	}
	v.CanPickMedia = h.canPickMedia(ctx)
	fc := &fieldCtx{ctx: ctx, svc: h.svc, locale: locale, typeSlug: t.Slug, errs: v.Errors, itemID: it.ID, canPick: v.CanPickMedia}
	v.Fields = fc.views(t.Fields, it.Fields, "fields", "")
	str := func(k string) string { return displayString(it.SEO[k]) }
	v.SEO = seoView{Title: str("title"), Description: str("description"), Canonical: str("canonical"), Robots: str("robots"), OGImage: str("og_image")}
	if !v.IsNew {
		v.SEODrift = seoDrift(it)
	}
	v.Featured = mediaView{Name: "featured_media_id", ID: "featured-media", Value: it.FeaturedMediaID, Label: "content.editor.featured_image", CanPick: v.CanPickMedia}
	v.OGImage = mediaView{Name: "seo[og_image]", ID: "seo-og-image", Value: v.SEO.OGImage, Label: "content.seo.og_image", CanPick: v.CanPickMedia}
	v.RobotsOptions = []string{"index,follow", "noindex,follow", "index,nofollow", "noindex,nofollow"}
	v.Body = fieldView{Key: "body", Name: "body", ID: "body", Type: string(FieldRichtext), Label: h.tr(ctx, "content.editor.body"),
		Value: it.Body, Error: v.Errors["body"], StartHTML: unsupportedMarkup(it.Body)}
	for k, msg := range v.Errors {
		if strings.HasPrefix(k, "terms.") {
			v.TermErrors = append(v.TermErrors, msg)
		}
	}
	slices.Sort(v.TermErrors)
	for _, tpl := range t.Templates {
		v.Templates = append(v.Templates, choice{Value: tpl, Label: tpl, Selected: tpl == it.Template})
	}
	if t.Hierarchical {
		var sel []string
		if it.ParentID != "" {
			sel = []string{it.ParentID}
		}
		v.Parent = newPickView("parent_id", "parent_id", t.Slug, it.ID, false, h.svc.pickedChoices(ctx, sel))
	}
	if u := auth.UserFromContext(ctx); u != nil && !ownOnly(u) {
		v.Authors = h.authorChoices(ctx, it.AuthorID)
	}
	for _, tax := range t.Taxonomies {
		q := url.Values{"selected": {strings.Join(it.Terms[tax], ",")}}
		v.Pickers = append(v.Pickers, pickerView{Taxonomy: tax, URL: "/admin/x/taxonomy/" + url.PathEscape(tax) + "/picker?" + q.Encode()})
	}
	if !isNew && v.Supports["revisions"] {
		v.Revisions, _ = h.svc.Revisions(ctx, it.ID)
	}
	if !isNew && it.Status != "trash" {
		v.Translations, v.CanUnlink = h.translationViews(ctx, base, it)
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/content/edit.html", v)
}

func (h *adminHandlers) authorChoices(ctx context.Context, current string) []choice {
	us, ok := kernel.Get[auth.Service](h.d, kernel.ServiceAuth)
	if !ok {
		return nil
	}
	users, _, err := us.List(ctx, auth.ListQuery{PerPage: 200, Sort: "name", Order: "asc"})
	if err != nil {
		return nil
	}
	out := []choice{}
	for _, u := range users {
		if u.Role == "customer" {
			continue
		}
		label := u.Name
		if label == "" {
			label = u.Email
		}
		out = append(out, choice{Value: u.ID, Label: label, Selected: u.ID == current})
	}
	return out
}

func (h *adminHandlers) createItem(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	in := Input{Type: t.Slug, Fields: map[string]any{}, SEO: map[string]any{}, Terms: map[string][]string{}}
	applyForm(r.PostForm, t, &in)
	it, err := h.svc.Create(r.Context(), in)
	h.afterSave(w, r, t, nil, in, it, err)
}

func (h *adminHandlers) updateItem(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	cur, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err == nil && cur.Type != t.Slug {
		err = ErrNotFound
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	in := InputFromItem(cur)
	in.SEO, in.Terms = maps.Clone(cur.SEO), maps.Clone(cur.Terms)
	applyForm(r.PostForm, t, &in)
	if in.Status == "trash" && cur.Status != "trash" && !auth.Can(r.Context(), "content.delete") {
		h.fail(w, r, ErrForbidden)
		return
	}
	var it *Item
	if r.PostForm.Has("edit_token") {
		it, err = h.svc.updateEditorAtomic(r.Context(), cur.ID, in, r.PostForm.Get("edit_token"), r.PostForm.Get("terms_token"))
	} else {
		it, err = h.svc.Update(r.Context(), cur.ID, in)
	}
	h.afterSave(w, r, t, cur, in, it, err)
}

func (h *adminHandlers) afterSave(w http.ResponseWriter, r *http.Request, t *Type, cur *Item, in Input, it *Item, err error) {
	var ve *ValidationError
	switch {
	case err == nil:
		httpx.SetFlash(w, "success", "content.flash.saved")
		http.Redirect(w, r, "/admin/content/"+t.Slug+"/"+it.ID, http.StatusSeeOther)
	case errors.Is(err, ErrConflict):
		draft := itemFromInput(in, cur)
		h.renderEditor(w, r, http.StatusConflict, t, draft, cur == nil, nil, "content.flash.conflict")
	case errors.As(err, &ve):
		draft := itemFromInput(in, cur)
		h.renderEditor(w, r, http.StatusUnprocessableEntity, t, draft, cur == nil, ve.Fields, "content.flash.invalid")
	default:
		h.fail(w, r, err)
	}
}

// itemFromInput rebuilds the editor state from a rejected submission.
func itemFromInput(in Input, cur *Item) *Item {
	it := &Item{}
	if cur != nil {
		*it = *cur
	}
	it.Type, it.Slug, it.Title, it.Excerpt, it.Body, it.Fields = in.Type, in.Slug, in.Title, in.Excerpt, in.Body, in.Fields
	it.AuthorID, it.Status, it.ParentID, it.Template, it.FeaturedMediaID = in.AuthorID, in.Status, in.ParentID, in.Template, in.FeaturedMediaID
	it.SEO, it.Locale, it.PublishAt, it.Terms = in.SEO, in.Locale, in.PublishAt, in.Terms
	if in.MenuOrder != nil {
		it.MenuOrder = *in.MenuOrder
	}
	if it.Fields == nil {
		it.Fields = map[string]any{}
	}
	return it
}

// applyForm overlays the editor form onto in. Taxonomy terms change only
// for pickers that rendered (terms_present[tax]), so a picker that failed
// to load never clears an item's terms; the author changes only when the
// author select was shown.
func applyForm(form url.Values, t *Type, in *Input) {
	set := func(dst *string, key string) {
		if vals, ok := form[key]; ok && len(vals) > 0 {
			*dst = vals[0]
		}
	}
	set(&in.Title, "title")
	set(&in.Slug, "slug")
	set(&in.Excerpt, "excerpt")
	set(&in.Body, "body")
	if vals, ok := form["parent_id"]; ok {
		in.ParentID = lastNonEmpty(vals) // picker: a "" marker, then the chip
	}
	set(&in.Template, "template")
	set(&in.FeaturedMediaID, "featured_media_id")
	set(&in.PublishAt, "publish_at")
	set(&in.AuthorID, "author_id")
	st := string(in.Status)
	set(&st, "status")
	if form.Get("intent") == "publish" {
		st = "published"
	}
	in.Status = Status(st)
	if v, ok := form["menu_order"]; ok && len(v) > 0 {
		n, _ := strconv.Atoi(strings.TrimSpace(v[0]))
		in.MenuOrder = &n
	}
	if in.SEO == nil {
		in.SEO = map[string]any{}
	}
	for _, k := range []string{"title", "description", "canonical", "robots", "og_image"} {
		if vals, ok := form["seo["+k+"]"]; ok && len(vals) > 0 {
			if v := strings.TrimSpace(vals[0]); v != "" {
				in.SEO[k] = v
			} else {
				delete(in.SEO, k)
			}
		}
	}
	in.Fields = parseFieldForm(form, t.Fields, "fields")
	if in.Terms == nil {
		in.Terms = map[string][]string{}
	}
	for _, tax := range t.Taxonomies {
		if form.Get("terms_present["+tax+"]") == "" {
			continue
		}
		in.Terms[tax] = stringList(form["terms["+tax+"]"])
	}
}

func (h *adminHandlers) itemFromURL(w http.ResponseWriter, r *http.Request) (*Type, *Item, bool) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return nil, nil, false
	}
	it, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err == nil && it.Type != t.Slug {
		err = ErrNotFound
	}
	if err != nil {
		h.fail(w, r, err)
		return nil, nil, false
	}
	return t, it, true
}

func (h *adminHandlers) trashItem(w http.ResponseWriter, r *http.Request) {
	t, it, ok := h.itemFromURL(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	var expected *editorTokens
	if r.PostForm.Has("edit_token") {
		expected = &editorTokens{content: r.PostForm.Get("edit_token"), terms: r.PostForm.Get("terms_token")}
	}
	if err := h.svc.deleteWithTokens(r.Context(), it.ID, false, expected); err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "content.flash.trashed")
	http.Redirect(w, r, "/admin/content/"+t.Slug, http.StatusSeeOther)
}

func (h *adminHandlers) deleteItem(w http.ResponseWriter, r *http.Request) {
	t, it, ok := h.itemFromURL(w, r)
	if !ok {
		return
	}
	if it.Status != "trash" {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	if err := h.svc.Delete(r.Context(), it.ID, true); err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "content.flash.deleted")
	http.Redirect(w, r, "/admin/content/"+t.Slug+"?status=trash", http.StatusSeeOther)
}

func (h *adminHandlers) restoreItem(w http.ResponseWriter, r *http.Request) {
	t, it, ok := h.itemFromURL(w, r)
	if !ok {
		return
	}
	if _, err := h.svc.Restore(r.Context(), it.ID, chi.URLParam(r, "rev")); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			httpx.SetFlash(w, "error", "content.flash.restore_invalid")
			http.Redirect(w, r, "/admin/content/"+t.Slug+"/"+it.ID, http.StatusSeeOther)
			return
		}
		h.fail(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "content.flash.restored")
	http.Redirect(w, r, "/admin/content/"+t.Slug+"/"+it.ID, http.StatusSeeOther)
}

// pick renders search results for the HTMX item picker (relationship
// fields and parent): items of ?type matching ?q, excluding ?exclude.
func (h *adminHandlers) pick(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := Query{Type: qs.Get("type"), Search: strings.TrimSpace(qs.Get("q")), PerPage: 20, Order: []Order{{By: "title"}}}
	res, err := h.svc.Query(r.Context(), q)
	var ve *ValidationError
	if errors.As(err, &ve) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]listRow, 0, len(res.Items))
	for _, it := range res.Items {
		if it.ID != qs.Get("exclude") {
			items = append(items, listRow{ID: it.ID, Title: it.Title, Slug: it.Slug})
		}
	}
	h.tpl.RenderWith(w, r, http.StatusOK, "", "pages/content/pick.html", map[string]any{"Items": items, "Query": q.Search})
}

// repeaterRow renders one empty repeater row (HTMX "add row").
func (h *adminHandlers) repeaterRow(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	key := r.URL.Query().Get("field")
	i := slices.IndexFunc(t.Fields, func(f Field) bool { return f.Key == key && f.Type == FieldRepeater })
	if i < 0 {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	f := t.Fields[i]
	idx := newRowIndex()
	fc := &fieldCtx{ctx: r.Context(), svc: h.svc, locale: i18n.Locale(r.Context()), typeSlug: t.Slug, errs: FieldErrors{}, canPick: h.canPickMedia(r.Context())}
	name := "fields[" + f.Key + "]"
	h.tpl.RenderPartial(w, r, "fields/repeater_row", rowView{Index: idx, Fields: fc.views(f.SubFields, nil, name+"["+idx+"]", "")})
}

// --- dashboard widget ---

var countsTpl = template.Must(template.New("counts").Funcs(template.FuncMap{"t": func(string) string { return "" }}).Parse(`
<h2 class="text-base font-bold mb-3">{{t "content.widget.title"}}</h2>
{{if .}}<table id="content-counts" class="w-full text-sm">
<thead><tr class="text-muted font-mono text-[11px] uppercase"><th class="text-start py-1">{{t "content.widget.type"}}</th><th class="text-end py-1">{{t "content.status.published"}}</th><th class="text-end py-1">{{t "content.status.draft"}}</th><th class="text-end py-1">{{t "content.status.scheduled"}}</th></tr></thead>
<tbody>{{range .}}<tr class="border-t border-line"><td class="py-1.5"><a class="text-blue hover:text-blue-hover" href="/admin/content/{{.Slug}}">{{.Name}}</a></td><td class="text-end font-mono">{{.Published}}</td><td class="text-end font-mono">{{.Draft}}</td><td class="text-end font-mono">{{.Scheduled}}</td></tr>{{end}}</tbody>
</table>{{else}}<p class="text-muted">{{t "content.widget.empty"}}</p>{{end}}`))

type countRow struct {
	Slug, Name                  string
	Published, Draft, Scheduled int
}

// countsWidget renders item counts per public type by status.
func (h *adminHandlers) countsWidget(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !auth.Can(ctx, "content.view") {
		return "", nil
	}
	types, err := h.svc.Types(ctx)
	if err != nil {
		return "", err
	}
	rows, err := h.d.DB.QueryContext(ctx, `SELECT type, status, COUNT(*) FROM content GROUP BY type, status`)
	if err != nil {
		return "", fmt.Errorf("content: counts: %w", err)
	}
	defer rows.Close()
	counts := map[string]map[string]int{}
	for rows.Next() {
		var typ, st string
		var n int
		if err := rows.Scan(&typ, &st, &n); err != nil {
			return "", err
		}
		if counts[typ] == nil {
			counts[typ] = map[string]int{}
		}
		counts[typ][st] = n
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	locale := i18n.Locale(ctx)
	var data []countRow
	for _, t := range types {
		if !t.Public {
			continue
		}
		c := counts[t.Slug]
		data = append(data, countRow{Slug: t.Slug, Name: localized(t.Plural, locale, localized(t.Name, locale, t.Slug)),
			Published: c["published"], Draft: c["draft"] + c["pending"], Scheduled: c["scheduled"]})
	}
	tpl, err := countsTpl.Clone()
	if err != nil {
		return "", err
	}
	tpl.Funcs(template.FuncMap{"t": func(key string) string {
		if h.d.I18n == nil {
			return key
		}
		return h.d.I18n.T(locale, key)
	}})
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}
