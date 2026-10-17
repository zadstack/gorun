package content

import (
	"context"
	"errors"
)

type FieldType string // text textarea richtext number date datetime boolean select multiselect repeater relationship image file json

type Field struct {
	Key        string            `json:"key"`
	Label      map[string]string `json:"label"`
	Type       FieldType         `json:"type"`
	Required   bool              `json:"required"`
	Indexed    bool              `json:"indexed"`
	Multiple   bool              `json:"multiple"`
	Default    any               `json:"default"`
	Help       map[string]string `json:"help,omitempty"`
	Options    []Option          `json:"options,omitempty"`
	Min        *float64          `json:"min,omitempty"`
	Max        *float64          `json:"max,omitempty"`
	Step       *float64          `json:"step,omitempty"`
	SubFields  []Field           `json:"sub_fields,omitempty"`
	TargetType string            `json:"target_type,omitempty"`
	Mime       []string          `json:"mime,omitempty"`
}

type Option struct {
	Value string            `json:"value"`
	Label map[string]string `json:"label"`
}

type Type struct {
	Slug         string            `json:"slug"`
	Name         map[string]string `json:"name"`
	Singular     map[string]string `json:"singular"`
	Plural       map[string]string `json:"plural"`
	Icon         string            `json:"icon"`
	Public       bool              `json:"public"`
	HasArchive   bool              `json:"has_archive"`
	Hierarchical bool              `json:"hierarchical"`
	System       bool              `json:"system"`
	ArchiveSlug  string            `json:"archive_slug"`
	Supports     []string          `json:"supports"`
	Fields       []Field           `json:"fields"`
	Taxonomies   []string          `json:"taxonomies"`
	Templates    []string          `json:"templates"`
	MenuPosition int               `json:"menu_position"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
}

type Status string // draft pending scheduled published private trash

type Item struct {
	ID              string              `json:"id"`
	Type            string              `json:"type"`
	Slug            string              `json:"slug"`
	Title           string              `json:"title"`
	Excerpt         string              `json:"excerpt"`
	Body            string              `json:"body"`
	BodyFormat      string              `json:"body_format"`
	Fields          map[string]any      `json:"fields"`
	AuthorID        string              `json:"author_id"`
	Status          Status              `json:"status"`
	ParentID        string              `json:"parent_id"`
	MenuOrder       int                 `json:"menu_order"`
	Template        string              `json:"template"`
	FeaturedMediaID string              `json:"featured_media_id"`
	SEO             map[string]any      `json:"seo"`
	Locale          string              `json:"locale"`
	PublishAt       string              `json:"publish_at"`
	PublishedAt     string              `json:"published_at"`
	CreatedAt       string              `json:"created_at"`
	UpdatedAt       string              `json:"updated_at"`
	Terms           map[string][]string `json:"terms"` // taxonomy → term slugs (filled by Query/Get)
	// TranslationGroup links the item to its translations ("" = none); changed only through the
	// translation operations, never by Create or Update.
	TranslationGroup string `json:"translation_group"`
}

// Input is the writable state of an item. Create builds an item from it;
// Update REPLACES the item's writable state with it in full (zero values
// clear fields). To change some fields, load the item, convert it with
// InputFromItem, modify, then call Update. MenuOrder nil means 0.
type Input struct {
	Type            string              `json:"type"`
	Slug            string              `json:"slug"`
	Title           string              `json:"title"`
	Excerpt         string              `json:"excerpt"`
	Body            string              `json:"body"`
	Fields          map[string]any      `json:"fields"`
	AuthorID        string              `json:"author_id"`
	Status          Status              `json:"status"`
	ParentID        string              `json:"parent_id"`
	MenuOrder       *int                `json:"menu_order"`
	Template        string              `json:"template"`
	FeaturedMediaID string              `json:"featured_media_id"`
	SEO             map[string]any      `json:"seo"`
	Locale          string              `json:"locale"`
	PublishAt       string              `json:"publish_at"`
	Terms           map[string][]string `json:"terms"`
}

// InputFromItem returns the Input that reproduces it (all writable fields,
// including Terms), for load-modify-Update.
func InputFromItem(it *Item) Input {
	order := it.MenuOrder
	return Input{
		Type: it.Type, Slug: it.Slug, Title: it.Title, Excerpt: it.Excerpt, Body: it.Body, Fields: it.Fields,
		AuthorID: it.AuthorID, Status: it.Status, ParentID: it.ParentID, MenuOrder: &order, Template: it.Template,
		FeaturedMediaID: it.FeaturedMediaID, SEO: it.SEO, Locale: it.Locale, PublishAt: it.PublishAt, Terms: it.Terms,
	}
}

type FieldErrors map[string]string

type Filter struct {
	Op    string `json:"op"`
	Value any    `json:"value"`
} // eq ne gt lt gte lte in like

type Order struct {
	By  string `json:"by"`
	Dir string `json:"dir"`
}

type Query struct {
	Type         string              `json:"type"`
	Status       string              `json:"status"`
	Search       string              `json:"search"`
	AuthorID     string              `json:"author_id"`
	ParentID     string              `json:"parent_id"`
	Locale       string              `json:"locale"`
	Taxonomy     map[string][]string `json:"taxonomy"`
	Fields       map[string]Filter   `json:"fields"`
	Order        []Order             `json:"order"`
	Page         int                 `json:"page"`
	PerPage      int                 `json:"per_page"`
	IncludeTrash bool                `json:"include_trash"`
	// PublicLocale keeps the items of one public-site locale plus those stored without a locale
	// (the site's own listings); it is not part of the REST or MCP query.
	PublicLocale string `json:"-"`
	// Template selects an exact rendering template for native administrative pickers.
	Template string `json:"-"`
	// PublicOnly excludes internal content types and protected pages before counting and pagination.
	PublicOnly bool `json:"-"`
}

type Page[T any] struct {
	Items   []T `json:"items"`
	Total   int `json:"total"`
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
}

type Revision struct {
	ID        string `json:"id"`
	ContentID string `json:"content_id"`
	AuthorID  string `json:"author_id"`
	CreatedAt string `json:"created_at"`
	Snapshot  Item   `json:"snapshot"`
}

type Service interface {
	Types(ctx context.Context) ([]Type, error)
	GetType(ctx context.Context, slug string) (*Type, error)
	SaveType(ctx context.Context, t Type) error
	DeleteType(ctx context.Context, slug string) error
	Query(ctx context.Context, q Query) (Page[Item], error)
	Get(ctx context.Context, id string) (*Item, error)
	GetBySlug(ctx context.Context, typ, slug, locale string) (*Item, error)
	Create(ctx context.Context, in Input) (*Item, error)
	Update(ctx context.Context, id string, in Input) (*Item, error) // full replace; see Input
	Delete(ctx context.Context, id string, hard bool) error
	Publish(ctx context.Context, id string) error
	Unpublish(ctx context.Context, id string) error
	Revisions(ctx context.Context, id string) ([]Revision, error)
	Restore(ctx context.Context, id, revisionID string) (*Item, error)
	Validate(t *Type, fields map[string]any) (map[string]any, FieldErrors)
	Reindex(ctx context.Context) error
	// Translations returns the item and the items linked to it as translations, by locale.
	Translations(ctx context.Context, id string) ([]Item, error)
	// CreateTranslation copies the item into locale as a linked draft.
	CreateTranslation(ctx context.Context, id, locale string) (*Item, error)
	// LinkTranslation links other (and its translations) to the item's translations.
	LinkTranslation(ctx context.Context, id, other string) error
	// LinkTranslationGuarded atomically requires the exact current union of member IDs.
	LinkTranslationGuarded(ctx context.Context, id, other string, expectedIDs []string) error
	// UnlinkTranslation takes the item out of its translations.
	UnlinkTranslation(ctx context.Context, id string) error
}

var ErrNotFound = errors.New("content: not found")

type ValidationError struct {
	Fields FieldErrors `json:"fields"`
}
