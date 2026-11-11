package content

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/i18n"
)

// FieldDiff is one changed field between two versions of an item.
type FieldDiff struct {
	Key   string // e.g. title, seo.description, fields.price, terms.category
	Label string // translated label for built-in keys; the key otherwise
	Spans []DiffSpan
}

// CompareItems lists the fields that differ between old and new (title,
// slug, status, excerpt, template, parent, menu order, featured image, SEO,
// custom fields, terms) and the line diff of their bodies. Labels are
// i18n keys for built-in fields; custom keys are returned as they are.
func CompareItems(old, cur *Item) ([]FieldDiff, []DiffLine) {
	var out []FieldDiff
	add := func(key, label, a, b string) {
		if a != b {
			out = append(out, FieldDiff{Key: key, Label: label, Spans: DiffWords(a, b)})
		}
	}
	add("title", "content.editor.title", old.Title, cur.Title)
	add("slug", "content.editor.slug", old.Slug, cur.Slug)
	add("status", "content.list.status", string(old.Status), string(cur.Status))
	add("excerpt", "content.editor.excerpt", old.Excerpt, cur.Excerpt)
	add("template", "content.editor.template", old.Template, cur.Template)
	add("parent_id", "content.editor.parent", old.ParentID, cur.ParentID)
	add("menu_order", "content.editor.menu_order", strconv.Itoa(old.MenuOrder), strconv.Itoa(cur.MenuOrder))
	add("featured_media_id", "content.editor.featured_image", old.FeaturedMediaID, cur.FeaturedMediaID)
	for _, k := range unionKeys(old.SEO, cur.SEO) {
		add("seo."+k, "", diffValue(old.SEO[k]), diffValue(cur.SEO[k]))
	}
	for _, k := range unionKeys(old.Fields, cur.Fields) {
		add("fields."+k, "", diffValue(old.Fields[k]), diffValue(cur.Fields[k]))
	}
	for _, k := range unionKeys(old.Terms, cur.Terms) {
		add("terms."+k, "", strings.Join(old.Terms[k], ", "), strings.Join(cur.Terms[k], ", "))
	}
	return out, DiffLines(HTMLTextLines(old.Body), HTMLTextLines(cur.Body))
}

func unionKeys[V any](a, b map[string]V) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// diffValue renders a stored value as comparable text.
func diffValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

type compareView struct {
	Type                 *Type
	TypeName             string
	Item                 *Item
	EditURL              string
	Revision             Revision
	Against              string // "current" or "previous"
	OldLabel, NewLabel   string // i18n keys
	OldAt, NewAt         string
	CurrentURL, PrevURL  string
	HasPrevious          bool
	Fields               []FieldDiff
	Body                 []DiffLine
	BodyChanged          bool
	CanRestore           bool
	RestoreAction        string
	Added, Removed, Same int
}

// compareRevision renders GET /admin/content/{type}/{id}/revisions/{rev}/compare:
// the revision against the current item (?against=current, the default) or
// against the revision before it (?against=previous).
func (h *adminHandlers) compareRevision(w http.ResponseWriter, r *http.Request) {
	t, it, ok := h.itemFromURL(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	revs, err := h.svc.Revisions(ctx, it.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	idx := slices.IndexFunc(revs, func(rv Revision) bool { return rv.ID == chi.URLParam(r, "rev") })
	if idx < 0 {
		h.fail(w, r, ErrNotFound)
		return
	}
	base := "/admin/content/" + t.Slug + "/" + it.ID
	rev := revs[idx]
	v := compareView{Type: t, TypeName: localized(t.Singular, i18n.Locale(ctx), localized(t.Name, i18n.Locale(ctx), t.Slug)), Item: it,
		EditURL: base, Revision: rev, Against: "current", HasPrevious: idx+1 < len(revs),
		CurrentURL: base + "/revisions/" + rev.ID + "/compare", PrevURL: base + "/revisions/" + rev.ID + "/compare?against=previous",
		CanRestore: idx > 0 && auth.Can(ctx, "content.edit"), RestoreAction: base + "/revisions/" + rev.ID + "/restore"}
	oldItem, newItem := &rev.Snapshot, it
	v.OldLabel, v.NewLabel, v.OldAt, v.NewAt = "content.revisions.this_revision", "content.revisions.current_version", rev.CreatedAt, it.UpdatedAt
	if r.URL.Query().Get("against") == "previous" {
		if !v.HasPrevious {
			h.fail(w, r, ErrNotFound)
			return
		}
		prev := revs[idx+1]
		v.Against = "previous"
		oldItem, newItem = &prev.Snapshot, &rev.Snapshot
		v.OldLabel, v.NewLabel, v.OldAt, v.NewAt = "content.revisions.previous_revision", "content.revisions.this_revision", prev.CreatedAt, rev.CreatedAt
	}
	v.Fields, v.Body = CompareItems(oldItem, newItem)
	for i := range v.Fields {
		if v.Fields[i].Label == "" {
			v.Fields[i].Label = v.Fields[i].Key
		} else {
			v.Fields[i].Label = h.tr(ctx, v.Fields[i].Label)
		}
	}
	for _, l := range v.Body {
		switch l.Op {
		case DiffEqual:
			v.Same++
		case DiffInsert:
			v.Added++
		case DiffDelete:
			v.Removed++
		default:
			v.Added++
			v.Removed++
		}
	}
	v.BodyChanged = v.Added+v.Removed > 0
	h.tpl.Render(w, r, "pages/content/revision_compare.html", v)
}
