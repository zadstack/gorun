package theme

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	adminThemesPath    = "/appearance/themes"
	adminTemplatesPath = "/appearance/templates"
	adminEditPath      = "/appearance/templates/edit"
)

type admin struct {
	s   *service
	tpl *httpx.Templates
}

// registerAdmin adds Appearance → Themes / Theme settings / Template overrides (themes.manage).
func registerAdmin(d *kernel.Deps, s *service) {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return
	}
	a := &admin{s: s, tpl: tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "appearance.themes", LabelKey: "theme.nav.themes", Icon: "palette", Href: "/admin" + adminThemesPath,
		Permission: PermManage, Group: "appearance", Position: 400})
	d.Admin.AddNav(kernel.NavItem{ID: "appearance.theme_settings", LabelKey: "theme.nav.settings", Icon: "sliders-horizontal", Href: "/admin" + adminSettingsPath,
		Permission: PermManage, Group: "appearance", Position: 410})
	d.Admin.AddNav(kernel.NavItem{ID: "appearance.templates", LabelKey: "theme.nav.templates", Icon: "file-code", Href: "/admin" + adminTemplatesPath,
		Permission: PermManage, Group: "appearance", Position: 420})
	d.Admin.AddNav(kernel.NavItem{ID: "appearance.overview", LabelKey: "theme.nav.overview", Icon: "layout-grid", Href: "/admin" + adminOverviewPath,
		Permission: PermManage, Group: "appearance", Position: 405})
	r := d.Router.Admin.With(auth.RequirePerm(PermManage))
	r.Get(adminOverviewPath, a.overview)
	r.Get(adminThemesPath, a.themes)
	r.Post(adminThemesPath+"/{slug}/activate", a.activate)
	r.Get(adminThemesPath+"/{slug}/screenshot.png", a.screenshot)
	r.Get(adminSettingsPath, a.themeSettings)
	r.Post(adminSettingsPath, a.themeSettingsSave)
	r.Get(adminTemplatesPath, a.templates)
	r.Get(adminEditPath, a.edit)
	r.Post(adminEditPath, a.save)
}

type themeCard struct {
	Info
	Active        bool
	HasScreenshot bool
}

func (a *admin) themes(w http.ResponseWriter, r *http.Request) {
	list, err := a.s.List(r.Context())
	if err != nil {
		a.s.log.Error("theme: list", "err", err)
	}
	active := a.s.activeSlug(r.Context())
	cards := make([]themeCard, 0, len(list))
	for _, info := range list {
		_, statErr := os.Stat(filepath.Join(info.Dir, "screenshot.png"))
		cards = append(cards, themeCard{Info: info, Active: info.Slug == active, HasScreenshot: statErr == nil})
	}
	a.tpl.Render(w, r, "pages/appearance/themes.html", map[string]any{"Themes": cards, "Active": active})
}

func (a *admin) activate(w http.ResponseWriter, r *http.Request) {
	if err := a.s.Activate(r.Context(), chi.URLParam(r, "slug")); err != nil {
		a.s.log.Warn("theme: activate", "theme", chi.URLParam(r, "slug"), "err", err)
		httpx.SetFlash(w, "error", "theme.themes.invalid")
	} else {
		httpx.SetFlash(w, "success", "theme.themes.activated")
	}
	http.Redirect(w, r, "/admin"+adminThemesPath, http.StatusSeeOther)
}

func (a *admin) screenshot(w http.ResponseWriter, r *http.Request) {
	info, err := findInfo(a.s.roots(), chi.URLParam(r, "slug"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(info.Dir, "screenshot.png")
	if _, err := os.Stat(p); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, p)
}

// templateRow is one line of the template overrides list.
type templateRow struct {
	Name, Source, Path string
	HasDraft           bool
}

func (a *admin) templates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	files, err := a.s.Templates(ctx)
	if err != nil {
		a.s.log.Error("theme: templates", "err", err)
	}
	overrides, _ := a.s.Overrides(ctx)
	drafts := map[string]bool{}
	for _, o := range overrides {
		if o.Draft {
			drafts[o.Name] = true
		}
	}
	rows := make([]templateRow, 0, len(files))
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.Name] = true
		rows = append(rows, templateRow{Name: f.Name, Source: f.Source, Path: f.Path, HasDraft: drafts[f.Name]})
	}
	for name := range drafts {
		if !seen[name] {
			rows = append(rows, templateRow{Name: name, Source: "draft", HasDraft: true})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	a.tpl.Render(w, r, "pages/appearance/templates.html", map[string]any{"Rows": rows, "Theme": a.s.Active(ctx)})
}

// editorData feeds pages/appearance/template_edit.html.
// Error is an i18n key; Detail is the template parser's diagnostic (only
// for ErrInvalidTemplate — it describes the submitted source).
type editorData struct {
	Name, Body, Source, Error, Detail, PreviewURL string
	HasDraft, HasOverride, Exists                 bool
}

func (a *admin) editorData(r *http.Request, name string) editorData {
	ctx := r.Context()
	d := editorData{Name: name, PreviewURL: "/?" + PreviewParam + "=1"}
	if body, err := a.s.ReadTemplate(ctx, name); err == nil {
		d.Body, d.Exists = body, true
	}
	if files, err := a.s.Templates(ctx); err == nil {
		for _, f := range files {
			if f.Name == name {
				d.Source = f.Source
			}
		}
	}
	d.HasOverride = a.s.hasOverride(ctx, name)
	if draft, ok := a.s.readDraft(ctx, name); ok {
		d.Body, d.HasDraft = draft, true
	}
	return d
}

func (a *admin) edit(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if !ValidName(name) {
		a.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	a.tpl.Render(w, r, "pages/appearance/template_edit.html", a.editorData(r, name))
}

func (a *admin) save(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.URL.Query().Get("name")
	if !ValidName(name) {
		a.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTemplateBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return
	}
	// An absent source is a malformed submission, not an intentionally empty file.
	if r.PostFormValue("action") != "delete" && !r.PostForm.Has("body") {
		http.Error(w, "missing template body", http.StatusBadRequest)
		return
	}
	body := r.PostFormValue("body")
	var err error
	flash := ""
	switch r.PostFormValue("action") {
	case "draft":
		err, flash = a.s.SaveOverride(ctx, name, body, true), "theme.editor.saved_draft"
	case "publish":
		err, flash = a.s.SaveOverride(ctx, name, body, false), "theme.editor.published"
	case "delete":
		err, flash = a.s.DeleteOverride(ctx, name), "theme.editor.deleted"
	default:
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if err != nil {
		a.s.log.Warn("theme: save override", "template", name, "err", err)
		d := a.editorData(r, name)
		d.Body = body
		d.Error, d.Detail = editorError(err)
		a.tpl.RenderWith(w, r, http.StatusUnprocessableEntity, "admin", "pages/appearance/template_edit.html", d)
		return
	}
	httpx.SetFlash(w, "success", flash)
	http.Redirect(w, r, "/admin"+adminEditPath+"?name="+url.QueryEscape(name), http.StatusSeeOther)
}

// maxTemplateBytes caps a template body submitted from the admin or API.
const maxTemplateBytes = 512 << 10

// editorError maps a service error to an i18n key (and, for template
// errors, the parser's diagnostic without the internal error prefix).
func editorError(err error) (key, detail string) {
	switch {
	case errors.Is(err, ErrInvalidTemplate):
		var te *templateError
		if errors.As(err, &te) {
			detail = te.detail
		}
		return "theme.editor.invalid", detail
	case errors.Is(err, ErrNotFound):
		return "theme.editor.not_found", ""
	case errors.Is(err, ErrInvalidName):
		return "theme.editor.invalid_name", ""
	case errors.Is(err, ErrInvalidTheme):
		return "theme.themes.invalid", ""
	}
	return "core.error.internal", ""
}
