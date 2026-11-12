package mcp

import (
	"context"
	"errors"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

type listTermsInput struct {
	Taxonomy string `json:"taxonomy" jsonschema:"taxonomy slug, e.g. category"`
	Search   string `json:"search,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
	Locale   string `json:"locale,omitempty" jsonschema:"only terms in this content language, e.g. ar"`
	// TranslationGroup lists one term and its translations (the translation_group of a term).
	TranslationGroup string `json:"translation_group,omitempty" jsonschema:"only the members of this translation group"`
	Page             int    `json:"page,omitempty"`
	PerPage          int    `json:"per_page,omitempty"`
}

type createTermInput struct {
	Taxonomy    string `json:"taxonomy"`
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty" jsonschema:"derived from name when empty"`
	Description string `json:"description,omitempty"`
	ParentID    string `json:"parent_id,omitempty" jsonschema:"hierarchical taxonomies only"`
	Locale      string `json:"locale,omitempty" jsonschema:"content language of the term; defaults to the site language"`
	// TranslationOf links the new term as a translation of another term of the same taxonomy.
	TranslationOf string `json:"translation_of,omitempty" jsonschema:"id of a term of the same taxonomy this term translates; its parent maps to the parent's translation"`
}

func taxonomyTools(svc taxonomy.Service) []kernel.MCPTool {
	return []kernel.MCPTool{
		{Name: "list_taxonomies", Title: "List taxonomies", Toolset: kernel.MCPToolsetTaxonomy, ReadOnly: true, Scopes: []string{scopeView},
			Description: "List taxonomies (category, tag and custom ones) with the content types they apply to.",
			Handler: bind(func(ctx context.Context, _ struct{}) (any, error) {
				list, err := svc.Taxonomies(ctx)
				if list == nil {
					list = []taxonomy.Taxonomy{}
				}
				return map[string]any{"taxonomies": list}, err
			})},
		{Name: "list_terms", Title: "List terms", Toolset: kernel.MCPToolsetTaxonomy, ReadOnly: true, Scopes: []string{scopeView}, Input: listTermsInput{},
			Description: "List the terms of a taxonomy, optionally of one content language (locale) or one translation group. Each term carries locale and translation_group.",
			Handler: bind(func(ctx context.Context, in listTermsInput) (any, error) {
				page, per := paging(in.Page, in.PerPage)
				if _, err := svc.Get(ctx, in.Taxonomy); err != nil {
					return nil, err
				}
				terms, total, err := svc.Terms(ctx, in.Taxonomy, taxonomy.TermQuery{Search: in.Search, ParentID: in.ParentID,
					Locale: in.Locale, TranslationGroup: in.TranslationGroup, Page: page, PerPage: per})
				if err != nil {
					return nil, err
				}
				return newPage(terms, total, page, per), nil
			})},
		{Name: "create_term", Title: "Create term", Toolset: kernel.MCPToolsetTaxonomy, Scopes: []string{scopeTaxonomy}, Input: createTermInput{},
			Description: "Create a term in a taxonomy, in a content language (locale, default the site language). With translation_of it becomes the translation of that term (same taxonomy, another locale).",
			Handler: bind(func(ctx context.Context, in createTermInput) (any, error) {
				t := taxonomy.Term{Taxonomy: in.Taxonomy, Name: in.Name, Slug: in.Slug,
					Description: in.Description, ParentID: in.ParentID, Locale: in.Locale}
				if in.TranslationOf == "" {
					return svc.SaveTerm(ctx, t)
				}
				src, err := svc.GetTerm(ctx, in.TranslationOf)
				if errors.Is(err, taxonomy.ErrNotFound) || (err == nil && src.Taxonomy != in.Taxonomy) {
					return nil, &content.ValidationError{Fields: content.FieldErrors{"translation_of": "taxonomy.validation.translation_taxonomy"}}
				}
				if err != nil {
					return nil, err
				}
				return svc.CreateTermTranslation(ctx, src.ID, t)
			})},
	}
}
