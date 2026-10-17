package taxonomy

import "context"

type Taxonomy struct {
	Slug         string            `json:"slug"`
	Name         map[string]string `json:"name"`
	Singular     map[string]string `json:"singular"`
	Hierarchical bool              `json:"hierarchical"`
	Public       bool              `json:"public"`
	System       bool              `json:"system"`
	Types        []string          `json:"types"`
}

type Term struct {
	ID          string         `json:"id"`
	Taxonomy    string         `json:"taxonomy"`
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	ParentID    string         `json:"parent_id"`
	Meta        map[string]any `json:"meta"`
	Count       int            `json:"count"`
}

type Service interface {
	Taxonomies(ctx context.Context) ([]Taxonomy, error)
	Get(ctx context.Context, slug string) (*Taxonomy, error)
	Save(ctx context.Context, t Taxonomy) error
	Delete(ctx context.Context, slug string) error
	Terms(ctx context.Context, taxonomy string, q TermQuery) ([]Term, int, error)
	GetTerm(ctx context.Context, id string) (*Term, error)
	GetTermBySlug(ctx context.Context, taxonomy, slug string) (*Term, error)
	SaveTerm(ctx context.Context, t Term) (*Term, error)
	DeleteTerm(ctx context.Context, id string) error
	SetContentTerms(ctx context.Context, contentID string, terms map[string][]string) error
	ContentTerms(ctx context.Context, contentID string) (map[string][]string, error)
	ContentIDs(ctx context.Context, taxonomy string, termSlugs []string) ([]string, error)
}

type TermQuery struct {
	Search   string `json:"search"`
	ParentID string `json:"parent_id"`
	Page     int    `json:"page"`
	PerPage  int    `json:"per_page"`
}
