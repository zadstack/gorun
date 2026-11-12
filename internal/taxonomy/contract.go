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
	// Locale is the public content language of the term (BCP-47, one of the site's content
	// languages); it defaults to the site language.
	Locale string `json:"locale"`
	// TranslationGroup links the term to its translations in other locales ("" = none). It is
	// read-only on save and changes only through the translation operations.
	TranslationGroup string `json:"translation_group"`
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
	// TermTranslations returns the term and its translations, ordered by locale.
	TermTranslations(ctx context.Context, id string) ([]Term, error)
	// CreateTermTranslation creates a term in t.Locale (name, slug and description from t;
	// description defaults to the source's) linked to the term id's group. The parent becomes
	// the parent's translation in that locale, when there is one.
	CreateTermTranslation(ctx context.Context, id string, t Term) (*Term, error)
	// LinkTermTranslation puts other (and its translations) into the group of id.
	LinkTermTranslation(ctx context.Context, id, other string) error
	// UnlinkTermTranslation takes the term out of its group.
	UnlinkTermTranslation(ctx context.Context, id string) error
}

type TermQuery struct {
	Search   string `json:"search"`
	ParentID string `json:"parent_id"`
	// Locale keeps the terms of one content language.
	Locale string `json:"locale"`
	// TranslationGroup keeps the members of one translation group.
	TranslationGroup string `json:"translation_group"`
	Page             int    `json:"page"`
	PerPage          int    `json:"per_page"`
}
