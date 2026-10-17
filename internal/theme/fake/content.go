// Package fake holds in-memory implementations of the services the theme
// engine and the site front controller consume (content, taxonomy, media,
// menus, redirects, seo), for tests. They are deliberately simple: slices and
// maps guarded by a mutex, no validation beyond what tests need.
package fake

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/zadstack/gorun/internal/content"
)

var _ content.Service = (*Content)(nil)

// Content is an in-memory content.Service.
type Content struct {
	mu    sync.RWMutex
	types []content.Type
	items []content.Item
	seq   int
}

// NewContent returns a store seeded with the system types post and page.
func NewContent() *Content {
	return &Content{types: []content.Type{
		{Slug: "post", Public: true, HasArchive: true, ArchiveSlug: "blog", Name: map[string]string{"en": "Posts", "ar": "المقالات"}, Taxonomies: []string{"category", "tag"}, System: true},
		{Slug: "page", Public: true, Hierarchical: true, Name: map[string]string{"en": "Pages", "ar": "الصفحات"}, System: true},
	}}
}

// Add stores it (assigning an ID when empty) and returns the stored copy.
func (c *Content) Add(it content.Item) content.Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	if it.ID == "" {
		c.seq++
		it.ID = fmt.Sprintf("c%03d", c.seq)
	}
	if it.Status == "" {
		it.Status = "published"
	}
	c.items = append(c.items, it)
	return it
}

func (c *Content) Types(context.Context) ([]content.Type, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.types), nil
}

func (c *Content) GetType(_ context.Context, slug string) (*content.Type, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, t := range c.types {
		if t.Slug == slug {
			t := t
			return &t, nil
		}
	}
	return nil, content.ErrNotFound
}

func (c *Content) SaveType(_ context.Context, t content.Type) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.types {
		if c.types[i].Slug == t.Slug {
			c.types[i] = t
			return nil
		}
	}
	c.types = append(c.types, t)
	return nil
}

func (c *Content) DeleteType(_ context.Context, slug string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.types = slices.DeleteFunc(c.types, func(t content.Type) bool { return t.Slug == slug })
	return nil
}

// Query supports Type, Status, Search, ParentID, Locale, Taxonomy (any of
// the slugs), Fields (eq only), Order (published_at, title, menu_order) and
// paging (default 10 per page).
func (c *Content) Query(_ context.Context, q content.Query) (content.Page[content.Item], error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []content.Item
	for _, it := range c.items {
		if match(it, q) {
			out = append(out, it)
		}
	}
	sortItems(out, q.Order)
	perPage := q.PerPage
	if perPage <= 0 {
		perPage = 10
	}
	page := max(q.Page, 1)
	total := len(out)
	start := min((page-1)*perPage, total)
	end := min(start+perPage, total)
	return content.Page[content.Item]{Items: slices.Clone(out[start:end]), Total: total, Page: page, PerPage: perPage}, nil
}

func match(it content.Item, q content.Query) bool {
	switch {
	case q.Type != "" && it.Type != q.Type,
		q.Status != "" && string(it.Status) != q.Status,
		q.ParentID != "" && it.ParentID != q.ParentID,
		q.Locale != "" && it.Locale != q.Locale,
		q.PublicLocale != "" && it.Locale != q.PublicLocale && it.Locale != "",
		!q.IncludeTrash && it.Status == "trash" && q.Status != "trash":
		return false
	}
	if s := strings.ToLower(q.Search); s != "" &&
		!strings.Contains(strings.ToLower(it.Title), s) && !strings.Contains(strings.ToLower(it.Body), s) {
		return false
	}
	for tax, slugs := range q.Taxonomy {
		if !slices.ContainsFunc(slugs, func(s string) bool { return slices.Contains(it.Terms[tax], s) }) {
			return false
		}
	}
	for key, f := range q.Fields {
		if fmt.Sprint(it.Fields[key]) != fmt.Sprint(f.Value) {
			return false
		}
	}
	return true
}

func sortItems(items []content.Item, order []content.Order) {
	if len(order) == 0 {
		order = []content.Order{{By: "published_at", Dir: "desc"}}
	}
	o := order[0]
	key := func(it content.Item) string {
		switch o.By {
		case "title":
			return it.Title
		case "menu_order":
			return fmt.Sprintf("%08d", it.MenuOrder)
		case "created_at":
			return it.CreatedAt
		default:
			return it.PublishedAt
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if strings.EqualFold(o.Dir, "asc") {
			return key(items[i]) < key(items[j])
		}
		return key(items[i]) > key(items[j])
	})
}

func (c *Content) Get(_ context.Context, id string) (*content.Item, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, it := range c.items {
		if it.ID == id {
			it := it
			return &it, nil
		}
	}
	return nil, content.ErrNotFound
}

// GetBySlug matches locale exactly ("" matches only items without a locale).
func (c *Content) GetBySlug(_ context.Context, typ, slug, locale string) (*content.Item, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, it := range c.items {
		if it.Type == typ && it.Slug == slug && it.Locale == locale {
			it := it
			return &it, nil
		}
	}
	return nil, content.ErrNotFound
}

func (c *Content) Create(_ context.Context, in content.Input) (*content.Item, error) {
	it := c.Add(itemFromInput("", in))
	return &it, nil
}

func (c *Content) Update(_ context.Context, id string, in content.Input) (*content.Item, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.items {
		if c.items[i].ID == id {
			c.items[i] = itemFromInput(id, in)
			it := c.items[i]
			return &it, nil
		}
	}
	return nil, content.ErrNotFound
}

func itemFromInput(id string, in content.Input) content.Item {
	order := 0
	if in.MenuOrder != nil {
		order = *in.MenuOrder
	}
	return content.Item{ID: id, Type: in.Type, Slug: in.Slug, Title: in.Title, Excerpt: in.Excerpt, Body: in.Body,
		Fields: in.Fields, AuthorID: in.AuthorID, Status: in.Status, ParentID: in.ParentID, MenuOrder: order,
		Template: in.Template, FeaturedMediaID: in.FeaturedMediaID, SEO: in.SEO, Locale: in.Locale,
		PublishAt: in.PublishAt, Terms: in.Terms}
}

func (c *Content) Delete(_ context.Context, id string, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = slices.DeleteFunc(c.items, func(it content.Item) bool { return it.ID == id })
	return nil
}

func (c *Content) setStatus(id string, s content.Status) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.items {
		if c.items[i].ID == id {
			c.items[i].Status = s
			return nil
		}
	}
	return content.ErrNotFound
}

func (c *Content) Publish(_ context.Context, id string) error   { return c.setStatus(id, "published") }
func (c *Content) Unpublish(_ context.Context, id string) error { return c.setStatus(id, "draft") }

func (c *Content) Revisions(context.Context, string) ([]content.Revision, error) { return nil, nil }

func (c *Content) Restore(ctx context.Context, id, _ string) (*content.Item, error) {
	return c.Get(ctx, id)
}

func (c *Content) Validate(_ *content.Type, fields map[string]any) (map[string]any, content.FieldErrors) {
	return fields, nil
}

func (c *Content) Reindex(context.Context) error { return nil }

// Translations returns the item alone: the fake has no translation groups.
func (c *Content) Translations(ctx context.Context, id string) ([]content.Item, error) {
	it, err := c.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return []content.Item{*it}, nil
}

func (c *Content) CreateTranslation(context.Context, string, string) (*content.Item, error) {
	return nil, fmt.Errorf("fake: translations unsupported")
}
func (c *Content) LinkTranslation(context.Context, string, string) error {
	return fmt.Errorf("fake: translations unsupported")
}
func (c *Content) LinkTranslationGuarded(context.Context, string, string, []string) error {
	return fmt.Errorf("fake: guarded translations unsupported")
}
func (c *Content) UnlinkTranslation(context.Context, string) error { return nil }
