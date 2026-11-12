package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

// Author and term choices for the bulk bar and the editor. The list page
// renders none of them: the bulk bar fetches them when the author or term
// action is chosen (GET /admin/x/content/{type}/choices, searchable), and a
// bulk form posted without a value (no JavaScript, or the fetch failed) gets
// a server-rendered step that asks for it. Authors are filtered in SQL
// (auth.ListQuery.Permission), so customers never crowd out staff.

const (
	authorChoicesMax = 100 // auth.Service.List returns at most 100 per page
	termChoicesMax   = 200
)

// choiceGroup is a labelled group of options (an <optgroup>).
type choiceGroup struct {
	Label   string
	Options []choice
}

// choicesView feeds the content/choice_options partial and the bulk step.
type choicesView struct {
	Authors []choice
	Terms   []choiceGroup
	More    bool // the list stops at its cap: searching narrows it
	Blank   bool // lead with an empty "choose" option (the editor)
}

// canAssign reports whether the request's user may give items another
// author: authors and contributors (own items only) keep their byline.
func canAssign(ctx context.Context) bool {
	return auth.Can(ctx, "content.edit") && !ownOnly(auth.UserFromContext(ctx))
}

func userLabel(u *auth.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

// authorChoices lists active staff whose role may create content, by name,
// matching search, at most authorChoicesMax. current, when it names a staff
// user outside that list (disabled, a role that may no longer write, or past
// the cap), is kept first and selected, so saving the editor does not
// change the byline. more reports a capped list.
func (h *adminHandlers) authorChoices(ctx context.Context, search, current string) (out []choice, more bool) {
	us, ok := kernel.Get[auth.Service](h.d, kernel.ServiceAuth)
	if !ok {
		return nil, false
	}
	users, total, err := us.List(ctx, auth.ListQuery{Permission: "content.create", Search: search, PerPage: authorChoicesMax, Sort: "name", Order: "asc"})
	if err != nil {
		h.d.Log.Warn("content: author choices", "err", err)
		return nil, false
	}
	found := false
	for i := range users {
		u := &users[i]
		found = found || u.ID == current
		out = append(out, choice{Value: u.ID, Label: userLabel(u), Selected: u.ID == current})
	}
	if current != "" && !found {
		if u, err := us.Get(ctx, current); err == nil && u.Role != auth.RoleCustomer {
			out = append([]choice{{Value: u.ID, Label: userLabel(u), Selected: true}}, out...)
		}
	}
	return out, total > len(users)
}

// taxonomyArgs returns "?,?" placeholders and arguments for t's taxonomies.
func taxonomyArgs(t *Type) (string, []any) {
	args := make([]any, len(t.Taxonomies))
	for i, tax := range t.Taxonomies {
		args[i] = tax
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(t.Taxonomies)), ","), args
}

// hasTerms reports whether any of t's taxonomies has a term, which is when
// the bulk bar offers the term actions. Without the taxonomy module (no
// terms table) it is false.
func (h *adminHandlers) hasTerms(ctx context.Context, t *Type) bool {
	if len(t.Taxonomies) == 0 {
		return false
	}
	marks, args := taxonomyArgs(t)
	var one int
	return h.d.DB.QueryRowContext(ctx, `SELECT 1 FROM terms WHERE taxonomy IN (`+marks+`) LIMIT 1`, args...).Scan(&one) == nil
}

// termChoices lists the terms of t's taxonomies matching search (name or
// slug), grouped by taxonomy in t's order, as "taxonomy/slug" values, at
// most termChoicesMax.
func (h *adminHandlers) termChoices(ctx context.Context, t *Type, search string) (out []choiceGroup, more bool) {
	if len(t.Taxonomies) == 0 {
		return nil, false
	}
	locale := i18n.Locale(ctx)
	marks, args := taxonomyArgs(t)
	names := map[string]string{}
	if rows, err := h.d.DB.QueryContext(ctx, `SELECT slug, name_json FROM taxonomies WHERE slug IN (`+marks+`)`, args...); err == nil {
		for rows.Next() {
			var slug, raw string
			var m map[string]string
			if rows.Scan(&slug, &raw) == nil && json.Unmarshal([]byte(raw), &m) == nil {
				names[slug] = localized(m, locale, slug)
			}
		}
		rows.Close()
	}
	where := `taxonomy IN (` + marks + `)`
	if search != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search) + "%"
		where += ` AND (name LIKE ? ESCAPE '\' OR slug LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	rows, err := h.d.DB.QueryContext(ctx, `SELECT taxonomy, slug, name FROM terms WHERE `+where+` ORDER BY taxonomy, name LIMIT ?`, append(args, termChoicesMax+1)...)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	groups := map[string]*choiceGroup{}
	n := 0
	for rows.Next() {
		var tax, slug string
		var name sql.NullString
		if rows.Scan(&tax, &slug, &name) != nil {
			continue
		}
		if n++; n > termChoicesMax {
			more = true
			break
		}
		g := groups[tax]
		if g == nil {
			g = &choiceGroup{Label: names[tax]}
			if g.Label == "" {
				g.Label = tax
			}
			groups[tax] = g
		}
		label := name.String
		if label == "" {
			label = slug
		}
		g.Options = append(g.Options, choice{Value: tax + "/" + slug, Label: label})
	}
	for _, tax := range t.Taxonomies {
		if g := groups[tax]; g != nil {
			out = append(out, *g)
		}
	}
	return out, more
}

// loadChoices fills v for kind ("author" or "term"), or refuses: ErrNotFound
// for an unknown kind, ErrForbidden when the user may not run that action.
func (h *adminHandlers) loadChoices(ctx context.Context, t *Type, kind, search, current string) (choicesView, error) {
	var v choicesView
	switch kind {
	case "author":
		if !canAssign(ctx) {
			return v, ErrForbidden
		}
		v.Authors, v.More = h.authorChoices(ctx, search, current)
	case "term":
		if !auth.Can(ctx, "content.edit") {
			return v, ErrForbidden
		}
		v.Terms, v.More = h.termChoices(ctx, t, search)
	default:
		return v, ErrNotFound
	}
	return v, nil
}

// choices answers GET /admin/x/content/{type}/choices?kind=author|term&q=
// with <option> elements for the bulk bar and the editor (selected= keeps
// the editor's current author; blank=1 leads with an empty option).
func (h *adminHandlers) choices(w http.ResponseWriter, r *http.Request) {
	t, ok := h.typeFromURL(w, r)
	if !ok {
		return
	}
	qs := r.URL.Query()
	v, err := h.loadChoices(r.Context(), t, qs.Get("kind"), strings.TrimSpace(qs.Get("q")), qs.Get("selected"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v.Blank = qs.Get("blank") == "1"
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.RenderPartial(w, r, "content/choice_options", v)
}

// bulkChooseView is the step that asks for the author or term of a bulk
// action posted without one.
type bulkChooseView struct {
	Choices                    choicesView
	TypeName, BaseURL, BackURL string
	Action, Kind               string
	IDs                        []string
	Return, Query              string
}

// bulkNeedsChoice reports whether a bulk author or term request lacks its
// value (or asks to search the choices) and the user may run the action.
func bulkNeedsChoice(ctx context.Context, action string, form url.Values) (kind string, ok bool) {
	search := form.Get("step") == "search"
	switch action {
	case "author":
		return "author", canAssign(ctx) && (search || strings.TrimSpace(form.Get("author_id")) == "")
	case "term_add", "term_remove":
		return "term", auth.Can(ctx, "content.edit") && (search || form.Get("term") == "")
	}
	return "", false
}

func (h *adminHandlers) bulkChoose(w http.ResponseWriter, r *http.Request, t *Type, kind string, ids []string) {
	ctx := r.Context()
	q := strings.TrimSpace(r.PostForm.Get("q"))
	cv, err := h.loadChoices(ctx, t, kind, q, "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	locale := i18n.Locale(ctx)
	h.tpl.Render(w, r, "pages/content/bulk_choose.html", bulkChooseView{Choices: cv,
		TypeName: localized(t.Plural, locale, localized(t.Name, locale, t.Slug)), BaseURL: "/admin/content/" + t.Slug,
		BackURL: bulkReturn(t, r.PostForm.Get("return")), Action: r.PostForm.Get("action"), Kind: kind, IDs: ids,
		Return: r.PostForm.Get("return"), Query: q})
}
