package mcp

import (
	"context"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

func (s *server) siteTool() kernel.MCPTool {
	return kernel.MCPTool{Name: "get_site_structure", Scopes: []string{scopeView},
		Description: "Everything needed to understand the site: identity, content types with fields, taxonomies, menus, active theme and template names. Call this first.",
		Handler:     bind(func(ctx context.Context, _ struct{}) (any, error) { return s.siteStructure(ctx) })}
}

func (s *server) contentTypes(ctx context.Context) ([]content.Type, error) {
	svc, ok := kernel.Get[content.Service](s.d, kernel.ServiceContent)
	if !ok {
		return []content.Type{}, nil
	}
	types, err := svc.Types(ctx)
	if types == nil {
		types = []content.Type{}
	}
	return types, err
}

// siteStructure mirrors GET /api/v1/site (api.SiteStructure).
func (s *server) siteStructure(ctx context.Context) (any, error) {
	d := s.d
	locale := d.Settings.GetString(ctx, "site.locale", "ar")
	out := api.SiteStructure{
		Site: api.SiteInfo{Title: d.Settings.GetString(ctx, "site.title", ""), Tagline: d.Settings.GetString(ctx, "site.tagline", ""),
			BaseURL: kernel.BaseURL(ctx, d), Locale: locale, Dir: "rtl"},
		Taxonomies: []taxonomy.Taxonomy{}, Menus: []menus.Menu{}, Templates: []theme.TemplateFile{},
	}
	if d.I18n != nil {
		out.Site.Dir = d.I18n.Dir(locale)
	}
	var err error
	if out.Types, err = s.contentTypes(ctx); err != nil {
		return nil, err
	}
	if svc, ok := kernel.Get[taxonomy.Service](d, kernel.ServiceTaxonomy); ok {
		if list, err := svc.Taxonomies(ctx); err != nil {
			return nil, err
		} else if list != nil {
			out.Taxonomies = list
		}
	}
	if svc, ok := kernel.Get[menus.Service](d, kernel.ServiceMenus); ok {
		if list, err := svc.List(ctx); err != nil {
			return nil, err
		} else if list != nil {
			out.Menus = list
		}
	}
	if svc, ok := kernel.Get[theme.Service](d, kernel.ServiceTheme); ok {
		info := svc.Active(ctx)
		out.Theme = &info
		if files, err := svc.Templates(ctx); err != nil {
			return nil, err
		} else if files != nil {
			out.Templates = files
		}
	}
	return out, nil
}
