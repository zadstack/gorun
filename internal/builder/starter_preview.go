package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/theme"
)

func previewOptions(d *kernel.Deps, ctx context.Context, id string) (theme.BuilderPreviewOptions, bool, error) {
	options := theme.BuilderPreviewOptions{Identity: brand.Default()}
	var raw string
	err := d.DB.QueryRowContext(ctx, `SELECT s.value_json FROM settings s,json_each(s.value_json,'$.pages') p WHERE s.key LIKE 'builder.starter.%' AND COALESCE(json_extract(s.value_json,'$.active'),0)=0 AND p.value=? ORDER BY s.updated_at DESC LIMIT 1`, id).Scan(&raw)
	if err == nil {
		var run starterRun
		if err := json.Unmarshal([]byte(raw), &run); err != nil {
			return options, false, err
		}
		options.Theme = runTheme(&run)
		options.Title = run.Title
		options.Tagline = run.Tagline
		options.Identity = run.Identity
		options.Logo = run.Profile.Logo
		ms, ok := kernel.Get[menus.Service](d, kernel.ServiceMenus)
		if ok {
			all, err := ms.List(ctx)
			if err != nil {
				return options, false, err
			}
			for _, menu := range all {
				if menu.ID != run.MenuID {
					continue
				}
				options.Menu = &menus.Menu{Name: menu.Name, Locale: menu.Locale}
				for _, item := range menu.Items {
					var slug string
					if err := d.DB.QueryRowContext(ctx, `SELECT slug FROM content WHERE id=?`, item.RefID).Scan(&slug); err != nil {
						return options, false, err
					}
					url := "/" + slug
					if item.RefID == run.Pages["home"] {
						url = "/"
					}
					options.Menu.Items = append(options.Menu.Items, menus.Item{Label: item.Label, URL: url})
				}
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return options, false, err
	} else {
		ts, ok := kernel.Get[theme.Service](d, kernel.ServiceTheme)
		if !ok {
			return options, false, nil
		}
		options.Theme = ts.Active(ctx).Slug
		options.Title = d.Settings.GetString(ctx, "site.title", "GoRun")
		options.Tagline = d.Settings.GetString(ctx, "site.tagline", "")
		if err := d.Settings.Get(ctx, "builder.identity", &options.Identity); err != nil && !errors.Is(err, settings.ErrNotFound) {
			return options, false, err
		}
	}
	options.CSS = builderCSS
	return options, options.Theme != "", nil
}
