package theme

import (
	"context"
	"net/http"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

type Info struct {
	Slug      string          `json:"slug"`
	Name      string          `json:"name"`
	Version   string          `json:"version"`
	Parent    string          `json:"parent"`
	Locations []string        `json:"locations"`
	Settings  []content.Field `json:"settings"`
	Supports  []string        `json:"supports"`
	// Logos are optional theme-shipped marks by variant (primary, dark,
	// light, icon): asset paths relative to the theme's assets/ directory,
	// served at /themes/<slug>/assets/<path>. Site settings take precedence.
	Logos map[string]string `json:"logos,omitempty"`
	Dir   string            `json:"dir"`
}

type Context struct {
	Site       SiteInfo
	Request    *http.Request
	Kind       string
	Content    *content.Item
	Items      []content.Item
	Term       *taxonomy.Term
	Pagination Pagination
	Query      map[string]string
	User       *auth.User
	Theme      Info
	Extra      map[string]any
	Locale     string
	Dir        string
	Status     int
	// Alternates are the page in each locale it exists in (hreflang links and language
	// switchers); empty when it exists in one locale only.
	Alternates []Alternate
}

// Alternate is a page's URL in one locale; Label is the locale's own name.
type Alternate struct {
	Locale, Label, URL string
}

type Service interface {
	Active(ctx context.Context) Info
	List(ctx context.Context) ([]Info, error)
	Activate(ctx context.Context, slug string) error
	Resolve(req kernel.KindRequest) (templateName string, err error)
	Render(ctx context.Context, w http.ResponseWriter, req kernel.KindRequest, tc *Context) error // renders with layout; on template error → 500 page + log
	Templates(ctx context.Context) ([]TemplateFile, error)
	ReadTemplate(ctx context.Context, name string) (string, error)
	TemplateDetails(ctx context.Context, name string) (TemplateDTO, error)
	SaveOverride(ctx context.Context, name, body string, draft bool) error
	Overrides(ctx context.Context) ([]Override, error)
	PublishOverride(ctx context.Context, name string) error
	DeleteOverride(ctx context.Context, name string) error
}

type Pagination struct {
	Page    int  `json:"page"`
	PerPage int  `json:"per_page"`
	Total   int  `json:"total"`
	Pages   int  `json:"pages"`
	HasPrev bool `json:"has_prev"`
	HasNext bool `json:"has_next"`
}

type SiteInfo struct {
	Title   string `json:"title"`
	Tagline string `json:"tagline"`
	BaseURL string `json:"base_url"`
	Locale  string `json:"locale"`
	Dir     string `json:"dir"`
	Theme   string `json:"theme"`
}

// TemplateFile is a template visible to the theme; Source is "theme",
// "parent", "override", "draft", or "pattern".
type TemplateFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Source string `json:"source"`
}

type Override struct {
	Name      string `json:"name"`
	Draft     bool   `json:"draft"`
	UpdatedAt string `json:"updated_at"`
}
