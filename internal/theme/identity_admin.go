package theme

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

// Appearance → Identity: the one place for logos, name and tagline, colours
// with contrast checks, Arabic/Latin fonts, shape and spacing, header links
// and custom CSS. Edits land in a per-theme draft that the preview iframe
// renders; Apply writes the live identity and records a revision; Revert
// restores an earlier revision. Content is never touched.

const (
	adminIdentityPath = "/appearance/identity"
	// PermIdentity matches the site-design workspace: settings-only users
	// may shape the identity without themes.manage.
	PermIdentity = "settings.manage"
)

type identityLogoSlot struct {
	Key, Name, ID, Value, Label, Help string
	CanPick, Multiple                 bool
	Error                             string
}

type identityContrast struct {
	Shown      bool
	Ratio      string
	Pass       bool
	Text, Back string
	OnAccent   string
	Accent     string
}

type identityRevisionView struct {
	IdentityRevision
	Accent, Title string
	Current       bool
}

type identityDesignView struct {
	Build, CanBuild                        bool
	Title, Theme, Accent, Background, Text string
}

type identityPage struct {
	Design       identityDesignView
	Theme        Info
	Supports     bool
	Title        string
	Tagline      string
	Logos        []identityLogoSlot
	Fields       []settingView // identity_* + custom_css
	CTA          []settingView // cta_label/cta_url when the theme declares them
	Contrast     identityContrast
	Draft        *IdentityDraft
	History      []identityRevisionView
	HistoryValid bool
	CanImport    bool
	Errors       map[string]string
	Error        string // page-level message (i18n'd)
	Notice       string // saved, draft, reverted, imported, discarded
	CanPickMedia bool
	PreviewURL   string
	Locale       string
	Rebased      string // site keys the draft took from the live site
}

// registerIdentityAdmin adds the identity page and its preview.
func registerIdentityAdmin(d *kernel.Deps, a *admin) {
	r := d.Router.Admin.With(auth.RequirePerm(PermIdentity))
	r.Get(adminIdentityPath, a.identityPage)
	r.Post(adminIdentityPath, a.identitySave)
	r.Get(adminIdentityPath+"/preview", a.identityPreview)
}

func canBuildSite(ctx context.Context) bool {
	for _, p := range []string{"settings.manage", "content.create", "content.edit", "content.publish", "menus.manage"} {
		if !auth.Can(ctx, p) {
			return false
		}
	}
	return true
}

// EffectiveIdentityColors returns accent, background and text after the
// precedence site identity › builder identity › theme declaration.
func (s *service) EffectiveIdentityColors(ctx context.Context) (accent, background, text string) {
	info := s.Active(ctx)
	rep := s.Appearance(ctx)
	pick := func(role string) string {
		if v := rep.Colors[role]; v.Available && v.Value != nil {
			return *v.Value
		}
		return ""
	}
	accent, background, text = pick("accent"), pick("background"), pick("text")
	var b brand.Identity
	if s.d.Settings != nil && s.d.Settings.Get(ctx, "builder.identity", &b) == nil && brand.Validate(b) == nil {
		if accent == "" || !supportsIdentity(info) {
			accent = b.Accent
		}
		if background == "" {
			background = b.Background
		}
		if text == "" {
			text = b.Text
		}
	}
	return accent, background, text
}

// ImportBuilderIdentity maps the builder's brand identity onto theme
// identity values, one way: colours carry over, the bundled Zad font maps
// to itself, other builder fonts and the page width stay with the builder
// layer (which still renders beneath the site identity).
func ImportBuilderIdentity(slug string, b brand.Identity) IdentitySnapshot {
	snap := IdentitySnapshot{Theme: slug, Values: map[string]string{}, Site: map[string]string{}}
	if brand.Validate(b) != nil {
		return snap
	}
	snap.Values[idAccent], snap.Values[idBackground], snap.Values[idText] = b.Accent, b.Background, b.Text
	if b.Font == "zad" {
		snap.Values[idBody] = "zad"
	}
	return snap
}

func (a *admin) identityDesign(ctx context.Context) identityDesignView {
	accent, background, text := a.s.EffectiveIdentityColors(ctx)
	return identityDesignView{CanBuild: canBuildSite(ctx), Title: a.s.d.Settings.GetString(ctx, SettingTitle, "GoRun"), Theme: a.s.Active(ctx).Slug,
		Accent: accent, Background: background, Text: text}
}

// identityContrastFor computes the text/background ratio of snap with the
// theme's declared colours as fallback, and the derived on-accent colour.
func (a *admin) identityContrastFor(ctx context.Context, snap IdentitySnapshot) identityContrast {
	rep := a.s.Appearance(ctx)
	declared := func(role string) string {
		if rep.Declared == nil {
			return ""
		}
		if v := rep.Declared.Colors[role]; v.Available && v.Value != nil {
			return *v.Value
		}
		return ""
	}
	c := identityContrast{Text: snap.Get(idText), Back: snap.Get(idBackground), Accent: snap.Get(idAccent)}
	if c.Text == "" {
		c.Text = declared("text")
	}
	if c.Back == "" {
		c.Back = declared("background")
	}
	if ratio := contrast(c.Text, c.Back); ratio > 0 {
		c.Shown, c.Ratio, c.Pass = true, strconv.FormatFloat(ratio, 'f', 1, 64), ratio >= minTextContrast
	}
	if hexColor.MatchString(c.Accent) {
		c.OnAccent = identityOnAccent(c.Accent)
	}
	return c
}

func (a *admin) identityLogoSlots(ctx context.Context, site map[string]string, errs map[string]string) []identityLogoSlot {
	locale := i18n.Locale(ctx)
	canPick := a.canPickMedia(ctx)
	t := func(k string) string { return a.s.d.I18n.T(locale, k) }
	var out []identityLogoSlot
	for _, l := range []struct{ key, name string }{{"site.logo", "primary"}, {"site.logo_light", "light"}, {"site.logo_dark", "dark"}, {"site.icon", "icon"}} {
		out = append(out, identityLogoSlot{Key: l.key, Name: l.key, ID: "identity-logo-" + l.name, Value: site[l.key],
			Label: t("theme.identity.logo_" + l.name), Help: t("theme.identity.logo_" + l.name + "_help"), CanPick: canPick, Error: errs[l.key]})
	}
	return out
}

// identityViews renders the identity schema fields with the form names the
// page posts (the bare key).
func (a *admin) identityViews(ctx context.Context, schema []content.Field, values map[string]string, errs map[string]string) []settingView {
	vals := map[string]any{}
	for k, v := range values {
		vals[k] = v
	}
	views := a.settingViews(ctx, schema, vals, errs)
	for i := range views {
		views[i].Name, views[i].ID, views[i].CanResetIdentity = views[i].Key, "identity-"+views[i].Key, false
	}
	return views
}

// identityState is the snapshot the page edits: the draft when one exists,
// else the live identity.
func (a *admin) identityState(ctx context.Context, slug string) (IdentitySnapshot, *IdentityDraft) {
	if d, ok := a.s.IdentityDraft(ctx, slug); ok {
		return d.Snapshot, &d
	}
	return a.s.LiveIdentity(ctx, slug), nil
}

func (a *admin) ctaSchema(info Info) []content.Field {
	var out []content.Field
	for _, f := range settingsSchema(info) {
		for _, k := range identityLinkKeys {
			if f.Key == k {
				out = append(out, f)
			}
		}
	}
	return out
}

func (a *admin) renderIdentity(w http.ResponseWriter, r *http.Request, status int, snap IdentitySnapshot, errs map[string]string, message string) {
	ctx := r.Context()
	info := a.s.Active(ctx)
	supports := supportsIdentity(info)
	live := a.s.LiveIdentity(ctx, info.Slug)
	_, draft := a.identityState(ctx, info.Slug)
	hist, valid := a.s.IdentityHistory(ctx, info.Slug)
	views := make([]identityRevisionView, 0, len(hist))
	for i, rev := range hist {
		views = append(views, identityRevisionView{IdentityRevision: rev, Accent: rev.Snapshot.Get(idAccent), Title: rev.Snapshot.Site["site.title"], Current: i == 0})
	}
	cta := map[string]string{}
	for _, k := range identityLinkKeys {
		cta[k] = a.s.d.Settings.GetString(ctx, SettingKey(info.Slug, k), "")
		if v, ok := snap.Links[k]; ok {
			cta[k] = v
		}
	}
	var b brand.Identity
	canImport := a.s.d.Settings.Get(ctx, "builder.identity", &b) == nil && brand.Validate(b) == nil
	page := identityPage{Design: a.identityDesign(ctx), Theme: info, Supports: supports, Title: snap.Site["site.title"], Tagline: snap.Site["site.tagline"],
		Logos: a.identityLogoSlots(ctx, snap.Site, errs), Fields: a.identityViews(ctx, identityFields(), snap.Values, errs),
		CTA: a.identityViews(ctx, a.ctaSchema(info), cta, errs), Contrast: a.identityContrastFor(ctx, snap), Draft: draft,
		History: views, HistoryValid: valid, CanImport: canImport, Errors: errs, Error: message, CanPickMedia: a.canPickMedia(ctx),
		PreviewURL: "/admin" + adminIdentityPath + "/preview", Locale: i18n.Locale(ctx)}
	if page.Title == "" {
		page.Title = live.Site["site.title"]
	}
	if draft != nil && len(draft.Rebased) > 0 {
		page.Rebased = strings.Join(draft.Rebased, ", ")
	}
	if r.Method == http.MethodGet {
		for _, n := range []string{"saved", "draft", "reverted", "imported", "discarded"} {
			if r.URL.Query().Get(n) == "1" {
				page.Notice = n
			}
		}
	}
	a.tpl.RenderWith(w, r, status, "admin", "pages/appearance/identity.html", page)
}

// identity is GET Appearance → Identity.
func (a *admin) identityPage(w http.ResponseWriter, r *http.Request) {
	snap, _ := a.identityState(r.Context(), a.s.Active(r.Context()).Slug)
	a.renderIdentity(w, r, http.StatusOK, snap, nil, "")
}

// identityFromForm reads the posted identity. Identity values are ignored
// for a theme that does not declare the capability, so nothing is stored
// that the theme would never render.
func (a *admin) identityFromForm(r *http.Request, info Info) IdentitySnapshot {
	snap := IdentitySnapshot{Theme: info.Slug, Values: map[string]string{}, Site: map[string]string{}}
	if supportsIdentity(info) {
		for _, k := range IdentityKeys {
			snap.Values[k] = r.PostForm.Get(k)
		}
	} else {
		// The theme renders none of these; keep whatever it stores.
		snap.Values = a.s.LiveIdentity(r.Context(), info.Slug).Values
	}
	if declared := a.ctaSchema(info); len(declared) > 0 {
		snap.Links = map[string]string{}
		for _, f := range declared {
			snap.Links[f.Key] = r.PostForm.Get(f.Key)
		}
	}
	for _, k := range SiteIdentityKeys {
		snap.Site[k] = r.PostForm.Get(k)
	}
	return snap
}

func (a *admin) identitySave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	info := a.s.Active(ctx)
	slug := info.Slug
	redirect := func(flag string) { http.Redirect(w, r, "/admin"+adminIdentityPath+"?"+flag+"=1", http.StatusSeeOther) }
	action := r.PostForm.Get("action")
	if posted := r.PostForm.Get("theme"); (action == "apply" || action == "draft" || action == "") && posted != slug {
		// The page was opened for another theme; never mix identities.
		snap, _ := a.identityState(ctx, slug)
		a.renderIdentity(w, r, http.StatusConflict, snap, nil, a.s.d.I18n.T(i18n.Locale(ctx), "theme.identity.error_theme_changed"))
		return
	}
	switch action {
	case "revert":
		if _, err := a.s.RevertIdentity(ctx, slug, r.PostForm.Get("revision")); err != nil {
			if errors.Is(err, ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			a.identityFailed(w, r, slug, err)
			return
		}
		redirect("reverted")
	case "discard":
		if err := a.s.DiscardIdentityDraft(ctx, slug); err != nil {
			a.identityFailed(w, r, slug, err)
			return
		}
		redirect("discarded")
	case "import":
		var b brand.Identity
		if err := a.s.d.Settings.Get(ctx, "builder.identity", &b); err != nil || brand.Validate(b) != nil {
			http.NotFound(w, r)
			return
		}
		snap, _ := a.identityState(ctx, slug)
		imported := ImportBuilderIdentity(slug, b)
		for k, v := range imported.Values {
			snap.Values[k] = v
		}
		if snap.Site["site.title"] == "" {
			snap.Site["site.title"] = a.s.d.Settings.GetString(ctx, SettingTitle, "GoRun")
		}
		if err := a.s.SaveIdentityDraft(ctx, snap); err != nil {
			a.identityFailed(w, r, slug, err)
			return
		}
		redirect("imported")
	case "apply":
		snap := a.identityFromForm(r, info)
		if _, err := a.s.ApplyIdentity(ctx, snap); err != nil {
			a.identityInvalid(w, r, snap, err)
			return
		}
		redirect("saved")
	default: // draft
		snap := a.identityFromForm(r, info)
		if err := a.s.SaveIdentityDraft(ctx, snap); err != nil {
			a.identityInvalid(w, r, snap, err)
			return
		}
		if r.Header.Get("X-Requested-With") == "fetch" {
			w.Header().Set("Cache-Control", "private, no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		redirect("draft")
	}
}

func (a *admin) identityInvalid(w http.ResponseWriter, r *http.Request, snap IdentitySnapshot, err error) {
	var ve *IdentityValidationError
	if errors.As(err, &ve) {
		a.renderIdentity(w, r, http.StatusUnprocessableEntity, snap, ve.Fields, a.s.d.I18n.T(i18n.Locale(r.Context()), "theme.identity.error_invalid"))
		return
	}
	a.identityFailed(w, r, snap.Theme, err)
}

func (a *admin) identityFailed(w http.ResponseWriter, r *http.Request, slug string, err error) {
	a.s.log.Error("theme: identity save failed", "err", err, "theme", slug)
	snap, _ := a.identityState(r.Context(), slug)
	a.renderIdentity(w, r, http.StatusInternalServerError, snap, nil, a.s.d.I18n.T(i18n.Locale(r.Context()), "theme.identity.error_failed"))
}

// identityPreview renders the site with the draft (or live) identity.
func (a *admin) identityPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := a.s.Active(ctx).Slug
	snap, _ := a.identityState(ctx, slug)
	locale := r.URL.Query().Get("locale")
	if locale == "" {
		locale = a.s.d.Settings.GetString(ctx, SettingLocale, defaultSiteLocale)
	}
	view := "home"
	if r.URL.Query().Get("view") == "single" {
		view = "single"
	}
	if err := a.s.RenderIdentityPreview(ctx, w, r, IdentityPreviewOptions{View: view, Locale: locale, Snapshot: snap}); err != nil {
		a.s.log.Warn("theme: identity preview failed", "err", fmt.Sprint(err))
	}
}
