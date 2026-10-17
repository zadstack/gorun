package menus

import "context"

type Item struct {
	ID        string `json:"id"`
	MenuID    string `json:"menu_id"`
	ParentID  string `json:"parent_id"`
	Position  int    `json:"position"`
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	RefID     string `json:"ref_id"`
	URL       string `json:"url"`
	Target    string `json:"target"`
	CSSClass  string `json:"css_class"`
	OpenInNew bool   `json:"open_in_new"`
	Children  []Item `json:"children"`
}

type Menu struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Locale   string `json:"locale"` // the public-site locale it is for; "" = every locale
	Items    []Item `json:"items"`
}

type Service interface {
	List(ctx context.Context) ([]Menu, error)
	Get(ctx context.Context, slug string) (*Menu, error)
	ByLocation(ctx context.Context, loc string) (*Menu, error)
	// ByLocationLocale is the menu at loc for locale, else the one for every locale.
	ByLocationLocale(ctx context.Context, loc, locale string) (*Menu, error)
	Save(ctx context.Context, m Menu) (*Menu, error)
	Delete(ctx context.Context, id string) error
	SaveItems(ctx context.Context, menuID string, items []Item) error
}

// Resolved URL for kind=content/term is computed by site via hook filter "menus.resolve_url"
