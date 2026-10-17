package content

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

type typeRow struct {
	Slug, Name, Icon string
	Fields           int
	Public, System   bool
}

func (h *adminHandlers) typesList(w http.ResponseWriter, r *http.Request) {
	types, err := h.svc.Types(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	locale := i18n.Locale(r.Context())
	rows := make([]typeRow, 0, len(types))
	for _, t := range types {
		rows = append(rows, typeRow{Slug: t.Slug, Name: localized(t.Name, locale, t.Slug), Icon: iconOr(t.Icon, "file-text"),
			Fields: len(t.Fields), Public: t.Public, System: t.System})
	}
	h.tpl.Render(w, r, "pages/content/types_list.html", map[string]any{"Types": rows})
}

type typeForm struct {
	Type       Type
	IsNew      bool
	Action     string
	FieldsJSON string
	Supports   []choice
	FieldTypes []FieldType
	TypeSlugs  []string
	Taxonomies string
	Templates  string
	Errors     FieldErrors
	ErrorsJSON string // translated errors for the Alpine builder
	NameKeys   []nameInput
	FormError  string
}

type nameInput struct{ ID, Label, Value, Dir string }

func (h *adminHandlers) typeNew(w http.ResponseWriter, r *http.Request) {
	t := Type{Public: true, Supports: []string{"title", "body", "excerpt", "featured_image", "author", "revisions", "seo"}}
	h.renderTypeForm(w, r, http.StatusOK, t, true, nil, "")
}

func (h *adminHandlers) typeEdit(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.GetType(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderTypeForm(w, r, http.StatusOK, *t, false, nil, "")
}

func (h *adminHandlers) renderTypeForm(w http.ResponseWriter, r *http.Request, status int, t Type, isNew bool, errs FieldErrors, formErr string) {
	normalizeType(&t)
	fieldsJSON, _ := json.Marshal(t.Fields)
	v := typeForm{Type: t, IsNew: isNew, Action: "/admin/types", FieldsJSON: string(fieldsJSON), FieldTypes: FieldTypes,
		Taxonomies: strings.Join(t.Taxonomies, ", "), Templates: strings.Join(t.Templates, ", "), Errors: errs, FormError: formErr}
	if v.Errors == nil {
		v.Errors = FieldErrors{}
	}
	translated := map[string]string{}
	for k, msg := range v.Errors {
		translated[k] = h.tr(r.Context(), msg)
	}
	ej, _ := json.Marshal(translated)
	v.ErrorsJSON = string(ej)
	for _, part := range []struct {
		key string
		m   map[string]string
	}{{"name", t.Name}, {"singular", t.Singular}, {"plural", t.Plural}} {
		for _, loc := range i18n.Supported {
			dir := "ltr"
			if loc == "ar" {
				dir = "rtl"
			}
			v.NameKeys = append(v.NameKeys, nameInput{ID: part.key + "_" + loc, Label: "content.types." + part.key + "_" + loc, Value: part.m[loc], Dir: dir})
		}
	}
	if !isNew {
		v.Action = "/admin/types/" + url.PathEscape(t.Slug)
	}
	for _, s := range Supports {
		sel := false
		for _, x := range t.Supports {
			sel = sel || x == s
		}
		v.Supports = append(v.Supports, choice{Value: s, Selected: sel})
	}
	if types, err := h.svc.Types(r.Context()); err == nil {
		for _, ct := range types {
			v.TypeSlugs = append(v.TypeSlugs, ct.Slug)
		}
	}
	h.tpl.RenderWith(w, r, status, "admin", "pages/content/type_edit.html", v)
}

// typeFromForm builds a Type from the builder form; fields arrive as JSON
// (fields_json, produced by the Alpine fields repeater) and are validated
// server-side by SaveType.
func typeFromForm(form url.Values) (Type, FieldErrors) {
	errs := FieldErrors{}
	bilingual := func(key string) map[string]string {
		m := map[string]string{}
		for _, loc := range i18n.Supported {
			if v := strings.TrimSpace(form.Get(key + "_" + loc)); v != "" {
				m[loc] = v
			}
		}
		return m
	}
	list := func(key string) []string {
		out := []string{}
		for _, p := range strings.Split(form.Get(key), ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	on := func(key string) bool { v := form.Get(key); return v == "on" || v == "1" || v == "true" }
	t := Type{Slug: strings.TrimSpace(form.Get("slug")), Name: bilingual("name"), Singular: bilingual("singular"), Plural: bilingual("plural"),
		Icon: strings.TrimSpace(form.Get("icon")), Public: on("public"), HasArchive: on("has_archive"), Hierarchical: on("hierarchical"),
		ArchiveSlug: strings.TrimSpace(form.Get("archive_slug")), Supports: form["supports"], Taxonomies: list("taxonomies"),
		Templates: list("templates")}
	t.MenuPosition, _ = strconv.Atoi(strings.TrimSpace(form.Get("menu_position")))
	if raw := strings.TrimSpace(form.Get("fields_json")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &t.Fields); err != nil {
			errs["fields"] = "content.validation.fields_json"
		}
	}
	return t, errs
}

func (h *adminHandlers) typeCreate(w http.ResponseWriter, r *http.Request) { h.typeSave(w, r, true) }

func (h *adminHandlers) typeUpdate(w http.ResponseWriter, r *http.Request) { h.typeSave(w, r, false) }

func (h *adminHandlers) typeSave(w http.ResponseWriter, r *http.Request, isNew bool) {
	if err := r.ParseForm(); err != nil {
		h.tpl.RenderError(w, r, http.StatusBadRequest)
		return
	}
	t, errs := typeFromForm(r.PostForm)
	if !isNew {
		cur, err := h.svc.GetType(r.Context(), chi.URLParam(r, "slug"))
		if err != nil {
			h.fail(w, r, err)
			return
		}
		t.Slug = cur.Slug
	} else if _, err := h.svc.GetType(r.Context(), t.Slug); err == nil {
		errs["slug"] = "content.validation.type_exists"
	}
	if len(errs) == 0 {
		err := h.svc.SaveType(r.Context(), t)
		var ve *ValidationError
		switch {
		case err == nil:
			httpx.SetFlash(w, "success", "content.flash.type_saved")
			http.Redirect(w, r, "/admin/types/"+url.PathEscape(t.Slug), http.StatusSeeOther)
			return
		case errors.As(err, &ve):
			errs = ve.Fields
		default:
			h.fail(w, r, err)
			return
		}
	}
	h.renderTypeForm(w, r, http.StatusUnprocessableEntity, t, isNew, errs, "content.flash.invalid")
}

func (h *adminHandlers) typeDelete(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	err := h.svc.DeleteType(r.Context(), slug)
	switch {
	case err == nil:
		httpx.SetFlash(w, "success", "content.flash.type_deleted")
	case errors.Is(err, ErrSystemType):
		httpx.SetFlash(w, "error", "content.error.system_type")
	case errors.Is(err, ErrTypeInUse):
		httpx.SetFlash(w, "error", "content.error.type_in_use")
	default:
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/types", http.StatusSeeOther)
}
