package pattern

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

const adminPatternsPath = "/appearance/patterns"

type admin struct {
	s   *service
	tpl *httpx.Templates
}

func registerAdmin(d *kernel.Deps, s *service) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return errors.New("pattern: admin templates missing (auth module must boot first)")
	}
	a := &admin{s: s, tpl: tpl}
	// Same permission as themes: patterns are Appearance layout, not HTTP extensions.
	d.Admin.AddNav(kernel.NavItem{
		ID: "appearance.patterns", LabelKey: "pattern.nav", Icon: "layout",
		Href: "/admin" + adminPatternsPath, Permission: theme.PermManage,
		Group: "appearance", Position: 402,
	})
	r := d.Router.Admin.With(auth.RequirePerm(theme.PermManage))
	r.Get(adminPatternsPath, a.list)
	r.Post(adminPatternsPath+"/{slug}/activate", a.activate)
	r.Post(adminPatternsPath+"/clear", a.clear)
	r.Get(adminPatternsPath+"/{slug}/screenshot.png", a.screenshot)
	return nil
}

type patternCard struct {
	Info
	Name, Description string
	Active            bool
	HasScreenshot     bool
}

func (a *admin) list(w http.ResponseWriter, r *http.Request) {
	list, err := a.s.List(r.Context())
	if err != nil {
		a.s.log.Error("pattern: list", "err", err)
	}
	active, _ := a.s.ActiveInfo(r.Context())
	locale := i18n.Locale(r.Context())
	cards := make([]patternCard, 0, len(list))
	for _, info := range list {
		_, statErr := os.Stat(filepath.Join(info.Dir, "screenshot.png"))
		cards = append(cards, patternCard{
			Info: info, Name: info.DisplayName(locale), Description: info.DisplayDescription(locale),
			Active: info.Slug == active.Slug, HasScreenshot: statErr == nil,
		})
	}
	a.tpl.Render(w, r, "pages/appearance/patterns.html", map[string]any{
		"Patterns": cards, "Active": active.Slug, "HasActive": active.Slug != "",
	})
}

func (a *admin) activate(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if err := a.s.Activate(r.Context(), slug); err != nil {
		a.s.log.Warn("pattern: activate", "slug", slug, "err", err)
		httpx.SetFlash(w, "error", "pattern.invalid")
	} else {
		httpx.SetFlash(w, "success", "pattern.activated")
	}
	http.Redirect(w, r, "/admin"+adminPatternsPath, http.StatusSeeOther)
}

func (a *admin) clear(w http.ResponseWriter, r *http.Request) {
	if err := a.s.Clear(r.Context()); err != nil {
		a.s.log.Warn("pattern: clear", "err", err)
		httpx.SetFlash(w, "error", "pattern.clear_failed")
	} else {
		httpx.SetFlash(w, "success", "pattern.cleared")
	}
	http.Redirect(w, r, "/admin"+adminPatternsPath, http.StatusSeeOther)
}

func (a *admin) screenshot(w http.ResponseWriter, r *http.Request) {
	info, err := a.s.Lookup(chi.URLParam(r, "slug"))
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
