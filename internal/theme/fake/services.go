package fake

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// ErrNotFound is returned by the fakes that have no package sentinel.
var ErrNotFound = errors.New("fake: not found")

var (
	_ taxonomy.Service  = (*Taxonomy)(nil)
	_ media.Service     = (*Media)(nil)
	_ menus.Service     = (*Menus)(nil)
	_ redirects.Service = (*Redirects)(nil)
	_ seo.Service       = (*SEO)(nil)
)

// Taxonomy is an in-memory taxonomy.Service seeded with category and tag.
type Taxonomy struct {
	mu    sync.RWMutex
	taxes []taxonomy.Taxonomy
	terms []taxonomy.Term
}

// NewTaxonomy returns the seeded store.
func NewTaxonomy() *Taxonomy {
	return &Taxonomy{taxes: []taxonomy.Taxonomy{
		{Slug: "category", Public: true, Hierarchical: true, Types: []string{"post"}, Name: map[string]string{"en": "Categories"}},
		{Slug: "tag", Public: true, Types: []string{"post"}, Name: map[string]string{"en": "Tags"}},
	}}
}

// AddTerm stores t (ID defaults to taxonomy:slug).
func (s *Taxonomy) AddTerm(t taxonomy.Term) taxonomy.Term {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		t.ID = t.Taxonomy + ":" + t.Slug
	}
	s.terms = append(s.terms, t)
	return t
}

func (s *Taxonomy) Taxonomies(context.Context) ([]taxonomy.Taxonomy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.taxes), nil
}

func (s *Taxonomy) Get(_ context.Context, slug string) (*taxonomy.Taxonomy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.taxes {
		if t.Slug == slug {
			t := t
			return &t, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Taxonomy) Save(_ context.Context, t taxonomy.Taxonomy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taxes = append(slices.DeleteFunc(s.taxes, func(x taxonomy.Taxonomy) bool { return x.Slug == t.Slug }), t)
	return nil
}

func (s *Taxonomy) Delete(_ context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taxes = slices.DeleteFunc(s.taxes, func(x taxonomy.Taxonomy) bool { return x.Slug == slug })
	return nil
}

func (s *Taxonomy) Terms(_ context.Context, tax string, _ taxonomy.TermQuery) ([]taxonomy.Term, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []taxonomy.Term
	for _, t := range s.terms {
		if t.Taxonomy == tax {
			out = append(out, t)
		}
	}
	return out, len(out), nil
}

func (s *Taxonomy) GetTerm(_ context.Context, id string) (*taxonomy.Term, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.terms {
		if t.ID == id {
			t := t
			return &t, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Taxonomy) GetTermBySlug(_ context.Context, tax, slug string) (*taxonomy.Term, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.terms {
		if t.Taxonomy == tax && t.Slug == slug {
			t := t
			return &t, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Taxonomy) SaveTerm(_ context.Context, t taxonomy.Term) (*taxonomy.Term, error) {
	out := s.AddTerm(t)
	return &out, nil
}

func (s *Taxonomy) DeleteTerm(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terms = slices.DeleteFunc(s.terms, func(t taxonomy.Term) bool { return t.ID == id })
	return nil
}

func (s *Taxonomy) SetContentTerms(context.Context, string, map[string][]string) error { return nil }

func (s *Taxonomy) ContentTerms(context.Context, string) (map[string][]string, error) {
	return nil, nil
}

func (s *Taxonomy) ContentIDs(context.Context, string, []string) ([]string, error) { return nil, nil }

// Media is an in-memory media.Service keyed by ID.
type Media struct {
	mu    sync.RWMutex
	items map[string]media.Media
}

// NewMedia returns an empty library.
func NewMedia() *Media { return &Media{items: map[string]media.Media{}} }

// Add stores m.
func (s *Media) Add(m media.Media) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[m.ID] = m
}

func (s *Media) Upload(context.Context, string, io.Reader, media.UploadMeta) (*media.Media, error) {
	return nil, errors.New("fake: upload unsupported")
}

func (s *Media) FromURL(context.Context, string, media.UploadMeta) (*media.Media, error) {
	return nil, errors.New("fake: from url unsupported")
}

func (s *Media) Get(_ context.Context, id string) (*media.Media, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &m, nil
}

func (s *Media) List(context.Context, media.ListQuery) ([]media.Media, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]media.Media, 0, len(s.items))
	for _, m := range s.items {
		out = append(out, m)
	}
	return out, len(out), nil
}

func (s *Media) Update(ctx context.Context, id string, _ media.Patch) (*media.Media, error) {
	return s.Get(ctx, id)
}

func (s *Media) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return nil
}

func (s *Media) URL(m *media.Media, variant string) string {
	if v, ok := m.Variants[variant]; ok {
		return v.URL
	}
	return m.URL
}

func (s *Media) FindBySourceURL(context.Context, string) (*media.Media, error) {
	return nil, ErrNotFound
}

// Menus is an in-memory menus.Service. It does not apply the
// "menus.resolve_url" filter; the theme fills empty URLs itself.
type Menus struct {
	mu    sync.RWMutex
	menus []menus.Menu
}

// NewMenus returns the store with the given menus.
func NewMenus(ms ...menus.Menu) *Menus { return &Menus{menus: ms} }

func (s *Menus) List(context.Context) ([]menus.Menu, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.menus), nil
}

func (s *Menus) find(f func(menus.Menu) bool) (*menus.Menu, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.menus {
		if f(m) {
			m := m
			m.Items = slices.Clone(m.Items)
			return &m, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Menus) Get(_ context.Context, slug string) (*menus.Menu, error) {
	return s.find(func(m menus.Menu) bool { return m.Slug == slug })
}

func (s *Menus) ByLocation(_ context.Context, loc string) (*menus.Menu, error) {
	return s.find(func(m menus.Menu) bool { return m.Location == loc })
}

// ByLocationLocale prefers the menu of locale, then the one for every locale.
func (s *Menus) ByLocationLocale(ctx context.Context, loc, locale string) (*menus.Menu, error) {
	if m, err := s.find(func(m menus.Menu) bool { return m.Location == loc && m.Locale == locale && locale != "" }); err == nil {
		return m, nil
	}
	return s.find(func(m menus.Menu) bool { return m.Location == loc && m.Locale == "" })
}

func (s *Menus) Save(_ context.Context, m menus.Menu) (*menus.Menu, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.menus = append(s.menus, m)
	return &m, nil
}

func (s *Menus) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.menus = slices.DeleteFunc(s.menus, func(m menus.Menu) bool { return m.ID == id })
	return nil
}

func (s *Menus) SaveItems(context.Context, string, []menus.Item) error { return nil }

// Redirects is an in-memory redirects.Service keyed by from path.
type Redirects struct {
	mu   sync.RWMutex
	list []redirects.Redirect
}

// NewRedirects returns the store with the given redirects.
func NewRedirects(rs ...redirects.Redirect) *Redirects { return &Redirects{list: rs} }

func (s *Redirects) Lookup(_ context.Context, path string) (*redirects.Redirect, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.list {
		if r.FromPath == path && r.Enabled {
			r := r
			return &r, true
		}
	}
	return nil, false
}

func (s *Redirects) List(context.Context, redirects.ListQuery) ([]redirects.Redirect, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.list), len(s.list), nil
}

func (s *Redirects) Save(_ context.Context, r redirects.Redirect) (*redirects.Redirect, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, r)
	return &r, nil
}

func (s *Redirects) Delete(context.Context, string) error { return nil }

func (s *Redirects) Hit(context.Context, string) {}

// SEO renders a <title> and description meta; Sitemap and Robots return
// fixed bodies tests can assert on.
type SEO struct{}

func (SEO) Render(_ context.Context, m seo.Meta, fallback seo.Meta) template.HTML {
	title, desc := m.Title, m.Description
	if title == "" {
		title = fallback.Title
	}
	if desc == "" {
		desc = fallback.Description
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<title>%s</title>", template.HTMLEscapeString(title))
	if desc != "" {
		fmt.Fprintf(&b, `<meta name="description" content="%s">`, template.HTMLEscapeString(desc))
	}
	if r := m.Robots + fallback.Robots; r != "" {
		if m.Robots != "" {
			r = m.Robots
		}
		fmt.Fprintf(&b, `<meta name="robots" content="%s">`, template.HTMLEscapeString(r))
	}
	if c := m.Canonical; c != "" || fallback.Canonical != "" {
		if c == "" {
			c = fallback.Canonical
		}
		fmt.Fprintf(&b, `<link rel="canonical" href="%s">`, template.HTMLEscapeString(c))
	}
	return template.HTML(b.String())
}

func (SEO) Sitemap(context.Context) ([]byte, error) {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"></urlset>`), nil
}

func (SEO) Entries(context.Context) ([]seo.SitemapEntry, error) { return nil, nil }

func (SEO) Robots(context.Context) string { return "User-agent: *\nAllow: /\n" }

// Module registers the non-nil fakes under the kernel.Service* names. Put it
// before theme in the module list; its name is outside kernel.BootOrder.
type Module struct {
	Content   *Content
	Taxonomy  *Taxonomy
	Media     *Media
	Menus     *Menus
	Redirects *Redirects
	SEO       seo.Service
}

// NewModule returns a module with every fake set (fresh, empty stores).
func NewModule() *Module {
	return &Module{Content: NewContent(), Taxonomy: NewTaxonomy(), Media: NewMedia(), Menus: NewMenus(),
		Redirects: NewRedirects(), SEO: SEO{}}
}

// Name implements kernel.Module.
func (*Module) Name() string { return "fakeservices" }

// Migrations implements kernel.Module.
func (*Module) Migrations() fs.FS { return nil }

// Register implements kernel.Module.
func (m *Module) Register(d *kernel.Deps) error {
	if m.Content != nil {
		d.Services.Set(kernel.ServiceContent, content.Service(m.Content))
	}
	if m.Taxonomy != nil {
		d.Services.Set(kernel.ServiceTaxonomy, taxonomy.Service(m.Taxonomy))
	}
	if m.Media != nil {
		d.Services.Set(kernel.ServiceMedia, media.Service(m.Media))
	}
	if m.Menus != nil {
		d.Services.Set(kernel.ServiceMenus, menus.Service(m.Menus))
	}
	if m.Redirects != nil {
		d.Services.Set(kernel.ServiceRedirects, redirects.Service(m.Redirects))
	}
	if m.SEO != nil {
		d.Services.Set(kernel.ServiceSEO, m.SEO)
	}
	return nil
}
