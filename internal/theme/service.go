package theme

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/zadstack/gorun/internal/kernel"
)

// Setting keys read by the theme engine.
const (
	SettingTheme   = "site.theme"
	SettingTitle   = "site.title"
	SettingTagline = "site.tagline"
	SettingLocale  = "site.locale"
	SettingTZ      = "site.timezone"
	SettingFront   = "site.front_page"

	defaultSiteLocale = "ar"
	defaultTheme      = "default"
)

var _ Service = (*service)(nil)

// service implements Service. Parsed template sets are cached per theme in
// prod and rebuilt per request in dev; preview sets (with drafts) are never
// cached.
type service struct {
	d     *kernel.Deps
	root  string
	extra []string // brand theme directories searched after root
	dev   bool
	log   *slog.Logger
	links *Linker

	cloneMu sync.Mutex // html/template Clone of shared masters
	mu      sync.Mutex
	cache   map[string]*compiled
}

func newService(d *kernel.Deps, root string, log *slog.Logger) *service {
	if log == nil {
		log = d.Log
	}
	if log == nil {
		log = slog.Default()
	}
	return &service{d: d, root: root, dev: d.Config.Env == "dev", log: log, links: NewLinker(d), cache: map[string]*compiled{}}
}

func (s *service) activeSlug(ctx context.Context) string {
	def := s.d.Config.Theme
	if def == "" {
		def = defaultTheme
	}
	slug := def
	if s.d.Settings != nil {
		slug = s.d.Settings.GetString(ctx, SettingTheme, def)
	}
	if info, err := findInfo(s.roots(), slug); err == nil {
		return info.Slug
	}
	return slug
}

// Active returns the active theme's info (only Slug when it cannot be loaded).
func (s *service) Active(ctx context.Context) Info {
	slug := s.activeSlug(ctx)
	info, err := findInfo(s.roots(), slug)
	if err != nil {
		s.log.Error("theme: load active theme", "theme", slug, "err", err)
		return Info{Slug: slug}
	}
	return info
}

// InfoLookup is implemented by the theme service: Lookup reads one
// installed theme's theme.json (ErrNotFound for unknown or invalid slugs).
type InfoLookup interface {
	Lookup(slug string) (Info, error)
}

var _ InfoLookup = (*service)(nil)

// Lookup implements InfoLookup.
func (s *service) Lookup(slug string) (Info, error) { return findInfo(s.roots(), slug) }

// List returns the installed themes.
func (s *service) List(context.Context) ([]Info, error) { return listThemes(s.roots()) }

// Activate validates the theme (theme.json, parent chain, index.html
// present) and stores it as setting site.theme.
func (s *service) Activate(ctx context.Context, slug string) error {
	chain, err := loadChain(s.roots(), slug)
	if err != nil {
		return err
	}
	slug = chain[0].Slug
	if _, ok := collect(themeLayers(chain))[indexTemplate]; !ok {
		return fmt.Errorf("%w: %q has no templates/%s", ErrInvalidTheme, slug, indexTemplate)
	}
	if err := s.d.Settings.Set(ctx, SettingTheme, slug); err != nil {
		return err
	}
	s.invalidate()
	s.d.Audit.Record(ctx, "theme.activate", "theme", slug, nil)
	return nil
}

// Resolve returns the template the active theme uses for req.
func (s *service) Resolve(req kernel.KindRequest) (string, error) {
	set, err := s.set(context.Background(), s.activeSlug(context.Background()), false)
	if err != nil {
		return "", err
	}
	return resolve(req, s.d.Theme, set.exists)
}

// themeLayers are the theme chain directories (no overrides).
func themeLayers(chain []Info) []layer {
	ls := make([]layer, 0, len(chain))
	for i, info := range chain {
		origin := "parent"
		if i == 0 {
			origin = "theme"
		}
		ls = append(ls, layer{dir: info.Dir, origin: origin})
	}
	return ls
}

// layers lists the directories searched for slug's templates, highest
// priority first: drafts (preview only), published overrides, the theme,
// its parents.
// slug has already passed loadChain (slugRe), so overridesDir cannot fail.
func (s *service) layers(slug string, chain []Info, preview bool) []layer {
	od, err := overridesDir(s.d.Config, slug)
	if err != nil {
		return themeLayers(chain)
	}
	var ls []layer
	if preview {
		ls = append(ls, layer{dir: filepath.Join(od, draftsSubdir), origin: "draft"})
	}
	ls = append(ls, layer{dir: od, origin: "override"})
	return append(ls, themeLayers(chain)...)
}

// Templates lists the active theme's effective templates with their source.
func (s *service) Templates(ctx context.Context) ([]TemplateFile, error) {
	slug := s.activeSlug(ctx)
	chain, err := loadChain(s.roots(), slug)
	if err != nil {
		return nil, err
	}
	files := collect(s.layers(slug, chain, false))
	out := make([]TemplateFile, 0, len(files))
	for _, f := range files {
		out = append(out, TemplateFile{Name: f.name, Path: s.displayPath(f.path), Source: f.origin})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// roots are the theme directories in lookup order: core first, then extras.
func (s *service) roots() []string { return append([]string{s.root}, s.extra...) }

// displayPath shortens an absolute path for the admin and API.
func (s *service) displayPath(p string) string {
	for _, base := range append([]string{filepath.Join(s.d.Config.DataDir)}, s.roots()...) {
		if abs, err := filepath.Abs(base); err == nil {
			if rel, err := filepath.Rel(abs, p); err == nil && !filepath.IsAbs(rel) && rel != "" && rel[0] != '.' {
				return filepath.ToSlash(rel)
			}
		}
		if rel, err := filepath.Rel(base, p); err == nil && rel != "" && rel[0] != '.' {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(p)
}

// ReadTemplate returns the published source of name (override or theme).
func (s *service) ReadTemplate(ctx context.Context, name string) (string, error) {
	if _, err := relPath(name); err != nil {
		return "", err
	}
	slug := s.activeSlug(ctx)
	chain, err := loadChain(s.roots(), slug)
	if err != nil {
		return "", err
	}
	f, ok := collect(s.layers(slug, chain, false))[name]
	if !ok {
		return "", fmt.Errorf("%w: template %q", ErrNotFound, name)
	}
	b, err := os.ReadFile(f.path)
	if err != nil {
		return "", fmt.Errorf("theme: read %s: %w", name, err)
	}
	return string(b), nil
}

func (s *service) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.cache)
}

// set returns the parsed template set for slug: cached in prod, rebuilt in
// dev and for previews.
func (s *service) set(ctx context.Context, slug string, preview bool) (*compiled, error) {
	if s.dev || preview {
		return s.compile(ctx, slug, preview)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cache[slug]; ok {
		return c, nil
	}
	c, err := s.compile(ctx, slug, false)
	if err != nil {
		return nil, err
	}
	s.cache[slug] = c
	return c, nil
}
