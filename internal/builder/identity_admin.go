package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

type identityMedia struct {
	Name, ID, Value, Label string
	Multiple, CanPick      bool
}

// registerIdentity keeps the Website → Design entry. The identity page
// itself (/admin/appearance/identity) is owned by the theme module, which
// renders the site identity contract; builder.identity remains a fallback
// layer the page can import from.
func registerIdentity(d *kernel.Deps) error {
	if _, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates); !ok {
		return errors.New("builder: admin templates unavailable")
	}
	d.Router.Admin.With(auth.RequirePerm("settings.manage")).Get("/site-design", siteDesignHome)
	return nil
}

func previewBrand(d *kernel.Deps, ctx context.Context, id string) string {
	identity := brand.Default()
	var raw string
	// Pending starters preview their proposed identity without changing the live site.
	err := d.DB.QueryRowContext(ctx, `SELECT json_extract(s.value_json,'$.identity') FROM settings s,json_each(s.value_json,'$.pages') p WHERE s.key LIKE 'builder.starter.%' AND COALESCE(json_extract(s.value_json,'$.active'),0)=0 AND p.value=? ORDER BY s.updated_at DESC LIMIT 1`, id).Scan(&raw)
	if err == nil && json.Unmarshal([]byte(raw), &identity) == nil && brand.Validate(identity) == nil {
		return brand.CSS(identity)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		d.Log.Warn("builder: preview identity lookup failed", "err", err)
	}
	if err := d.Settings.Get(ctx, "builder.identity", &identity); err != nil {
		identity = brand.Default()
	}
	return brand.CSS(identity)
}

func identityError(d *kernel.Deps, ctx context.Context, err error) string {
	for _, entry := range []struct {
		err error
		key string
	}{{brand.ErrColors, "identity.colors_invalid"}, {brand.ErrFont, "identity.font_invalid"}, {brand.ErrWidth, "identity.width_invalid"}, {brand.ErrTextContrast, "identity.text_contrast"}, {brand.ErrButtonContrast, "identity.button_contrast"}} {
		if errors.Is(err, entry.err) {
			return d.I18n.T(i18n.Locale(ctx), entry.key)
		}
	}
	if i18n.Locale(ctx) == "ar" {
		return "تحقق من اسم الموقع والشعار والإعدادات وحاول الحفظ مرة أخرى."
	}
	return "Check the site name, logo and settings, then try saving again."
}
