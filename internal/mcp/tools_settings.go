package mcp

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/seo"
)

const maxSettingsPerCall = 20

// adminOnlySettings are General settings MCP never changes (they can take
// the site offline or reroute it).
var adminOnlySettings = map[string]bool{"site.base_url": true, "site.theme": true, "site.front_page": true}

// settingValidators is the exact allowlist: the admin General page (minus
// adminOnlySettings) and the SEO page, validated by the very functions
// those screens use, so the rules cannot drift.
var settingValidators = map[string]func(key, value string) (string, error){
	"site.title":                  admin.ValidateGeneralSetting,
	"site.tagline":                admin.ValidateGeneralSetting,
	"site.locale":                 admin.ValidateGeneralSetting,
	"site.timezone":               admin.ValidateGeneralSetting,
	"contact.email":               admin.ValidateGeneralSetting,
	"site.posts_per_page":         admin.ValidateGeneralSetting,
	"site.logo":                   validateLogoID, // existence checked against the media service in the tool
	"site.logo_dark":              validateLogoID,
	"site.logo_light":             validateLogoID,
	"site.icon":                   validateLogoID,
	seo.SettingTitleTemplate:      admin.ValidateSEOSetting,
	seo.SettingDefaultDescription: admin.ValidateSEOSetting,
	seo.SettingDefaultOGImage:     admin.ValidateSEOSetting,
	seo.SettingRobotsTxt:          admin.ValidateSEOSetting,
}

// validateLogoID accepts "" or something shaped like a media id; the tool
// then requires the media item to exist (like the admin General page).
func validateLogoID(key, value string) (string, error) {
	v := strings.TrimSpace(value)
	if len(v) > 64 || strings.ContainsAny(v, " \t\r\n/") {
		return "", &admin.SettingError{Key: key, Code: "admin.settings.error_logo"}
	}
	return v, nil
}

// settingErrorCode returns the i18n code of a validator error.
func settingErrorCode(err error) string {
	var ae *admin.SettingError
	var se *seo.SettingError
	switch {
	case errors.As(err, &ae):
		return ae.Code
	case errors.As(err, &se):
		return se.Code
	}
	return "invalid"
}

type updateSettingsInput struct {
	Settings map[string]any `json:"settings" jsonschema:"key → string value (site.posts_per_page may be a number). Allowed: site.title, site.tagline, site.locale, site.timezone, site.logo (media id), contact.email, site.posts_per_page (1-100), seo.title_template, seo.default_description, seo.default_og_image, seo.robots_txt"`
}

// validateSettings checks every key and value; nothing is written unless
// all pass.
func validateSettings(in map[string]any) (map[string]string, map[string]string) {
	vals, bad := map[string]string{}, map[string]string{}
	for k, raw := range in {
		validate, ok := settingValidators[k]
		switch {
		case adminOnlySettings[k]:
			bad[k] = "admin_only"
			continue
		case !ok:
			bad[k] = "not_allowed"
			continue
		}
		s, isString := raw.(string)
		if f, isNum := raw.(float64); isNum && k == "site.posts_per_page" && f == float64(int(f)) {
			s, isString = strconv.Itoa(int(f)), true
		}
		if !isString {
			bad[k] = "must_be_string"
			continue
		}
		v, err := validate(k, s)
		if err != nil {
			bad[k] = settingErrorCode(err)
			continue
		}
		vals[k] = v
	}
	return vals, bad
}

func settingsTool(d *kernel.Deps) kernel.MCPTool {
	return kernel.MCPTool{Name: "update_settings", Scopes: []string{scopeSettings}, Sensitive: true, Input: updateSettingsInput{},
		Description: "Change site identity and SEO settings (site.title, site.tagline, site.locale, site.timezone, site.logo, contact.email, site.posts_per_page, seo.*). " +
			"Base URL, theme and front page stay admin-only. All values are validated; nothing is saved if one is invalid.",
		Handler: bind(func(ctx context.Context, in updateSettingsInput) (any, error) {
			if len(in.Settings) == 0 || len(in.Settings) > maxSettingsPerCall {
				return nil, invalid("give between 1 and 20 settings", map[string]string{"settings": "count"})
			}
			vals, bad := validateSettings(in.Settings)
			if logo := vals["site.logo"]; logo != "" {
				ms, ok := kernel.Get[media.Service](d, kernel.ServiceMedia)
				if !ok {
					bad["site.logo"] = "admin.settings.error_logo"
				} else if _, err := ms.Get(ctx, logo); err != nil {
					bad["site.logo"] = "admin.settings.error_logo"
				}
			}
			if len(bad) > 0 {
				return nil, invalid("invalid settings; nothing was saved", bad)
			}
			keys := make([]string, 0, len(vals))
			for k := range vals {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			err := db.InTx(ctx, d.DB, func(tx *sql.Tx) error {
				for _, k := range keys {
					var v any = vals[k]
					if k == "site.posts_per_page" {
						v, _ = admin.PostsPerPage(vals[k]) // stored as a number, like the admin page
					}
					if err := d.Settings.SetTx(ctx, tx, k, v); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			d.Audit.Record(ctx, "settings.update", "settings", "", map[string][]string{"keys": keys})
			return map[string]any{"updated": keys}, nil
		})}
}
