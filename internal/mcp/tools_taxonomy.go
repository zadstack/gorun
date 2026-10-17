package mcp

import (
	"context"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

type listTermsInput struct {
	Taxonomy string `json:"taxonomy" jsonschema:"taxonomy slug, e.g. category"`
	Search   string `json:"search,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
	Page     int    `json:"page,omitempty"`
	PerPage  int    `json:"per_page,omitempty"`
}

type createTermInput struct {
	Taxonomy    string `json:"taxonomy"`
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty" jsonschema:"derived from name when empty"`
	Description string `json:"description,omitempty"`
	ParentID    string `json:"parent_id,omitempty" jsonschema:"hierarchical taxonomies only"`
}

func taxonomyTools(svc taxonomy.Service) []kernel.MCPTool {
	return []kernel.MCPTool{
		{Name: "list_taxonomies", Scopes: []string{scopeView},
			Description: "List taxonomies (category, tag and custom ones) with the content types they apply to.",
			Handler: bind(func(ctx context.Context, _ struct{}) (any, error) {
				list, err := svc.Taxonomies(ctx)
				if list == nil {
					list = []taxonomy.Taxonomy{}
				}
				return map[string]any{"taxonomies": list}, err
			})},
		{Name: "list_terms", Scopes: []string{scopeView}, Input: listTermsInput{},
			Description: "List the terms of a taxonomy.",
			Handler: bind(func(ctx context.Context, in listTermsInput) (any, error) {
				page, per := paging(in.Page, in.PerPage)
				if _, err := svc.Get(ctx, in.Taxonomy); err != nil {
					return nil, err
				}
				terms, total, err := svc.Terms(ctx, in.Taxonomy, taxonomy.TermQuery{Search: in.Search, ParentID: in.ParentID, Page: page, PerPage: per})
				if err != nil {
					return nil, err
				}
				return newPage(terms, total, page, per), nil
			})},
		{Name: "create_term", Scopes: []string{scopeTaxonomy}, Input: createTermInput{},
			Description: "Create a term in a taxonomy.",
			Handler: bind(func(ctx context.Context, in createTermInput) (any, error) {
				return svc.SaveTerm(ctx, taxonomy.Term{Taxonomy: in.Taxonomy, Name: in.Name, Slug: in.Slug,
					Description: in.Description, ParentID: in.ParentID})
			})},
	}
}
