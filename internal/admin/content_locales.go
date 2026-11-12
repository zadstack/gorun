package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/redirects"
)

func parseContentLocales(raw string) ([]string, error) {
	tags := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	locales, err := i18n.ParseContentLocales(tags)
	if err != nil {
		return nil, err
	}
	for _, locale := range locales {
		if redirects.Reserved("/" + locale) {
			return nil, &SettingError{Key: i18n.ContentLocalesSetting, Code: "admin.settings.error_locale_route"}
		}
	}
	return locales, nil
}

// ValidateContentLocaleSettings must run inside the transaction that writes
// settings, including MCP and mixed general/mail updates. It prevents a
// concurrent content save from being hidden by a language removal.
func ValidateContentLocaleSettings(ctx context.Context, tx *sql.Tx, vals map[string]any) error {
	_, changesDefault := vals["site.locale"]
	_, changesLocales := vals[i18n.ContentLocalesSetting]
	if !changesDefault && !changesLocales {
		return nil
	}
	saved, err := generalSnapshot(ctx, tx)
	if err != nil {
		return err
	}
	previous := contentLocalesFromSnapshot(saved)
	locales, err := updatedContentLocales(previous, vals)
	if err != nil {
		return err
	}
	if changesLocales {
		vals[i18n.ContentLocalesSetting] = locales
	}
	if err := validatePublicDefault(saved, vals, locales); err != nil {
		return err
	}
	if err := validateLocaleRoutes(ctx, tx, previous, locales); err != nil {
		return err
	}
	return validateLocaleRemoval(ctx, tx, previous, locales)
}

func contentLocalesFromSnapshot(saved map[string]string) []string {
	var stored []string
	if json.Unmarshal([]byte(saved[i18n.ContentLocalesSetting]), &stored) == nil {
		if normalized, err := i18n.ParseContentLocales(stored); err == nil {
			return normalized
		}
	}
	return []string{"ar", "en"}
}

func updatedContentLocales(previous []string, vals map[string]any) ([]string, error) {
	raw, present := vals[i18n.ContentLocalesSetting]
	if !present {
		return previous, nil
	}
	next, typed := raw.([]string)
	if typed {
		if normalized, err := i18n.ParseContentLocales(next); err == nil {
			return normalized, nil
		}
	}
	return nil, &SettingError{Key: i18n.ContentLocalesSetting, Code: "admin.settings.error_content_locales"}
}

func validatePublicDefault(saved map[string]string, vals map[string]any, locales []string) error {
	locale := contentLocalesFromSnapshot(saved)[0]
	_ = json.Unmarshal([]byte(saved["site.locale"]), &locale)
	if next, ok := vals["site.locale"].(string); ok {
		locale = next
	}
	if !slices.Contains(locales, locale) {
		return &SettingError{Key: "site.locale", Code: "admin.settings.error_content_locale"}
	}
	return nil
}

func validateLocaleRoutes(ctx context.Context, tx *sql.Tx, previous, locales []string) error {
	for _, candidate := range locales {
		if redirects.Reserved("/" + candidate) {
			return &SettingError{Key: i18n.ContentLocalesSetting, Code: "admin.settings.error_locale_route"}
		}
		if slices.Contains(previous, candidate) {
			continue
		}
		if err := checkLocaleRoute(ctx, tx, candidate); err != nil {
			return err
		}
	}
	return nil
}

func validateLocaleRemoval(ctx context.Context, tx *sql.Tx, previous, locales []string) error {
	for _, removed := range previous {
		if slices.Contains(locales, removed) {
			continue
		}
		for _, table := range []string{"content", "menus", "terms"} {
			used, err := localeInUse(ctx, tx, table, removed)
			if err != nil {
				return err
			}
			if used {
				return &SettingError{Key: i18n.ContentLocalesSetting, Code: "admin.settings.error_locale_in_use"}
			}
		}
	}
	return nil
}

func localeInUse(ctx context.Context, tx *sql.Tx, table, locale string) (bool, error) {
	exists, err := localeTableExists(ctx, tx, table)
	if err != nil || !exists {
		return false, err
	}
	var used bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE locale=?)`, locale).Scan(&used)
	return used, err
}

func localeTableExists(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&exists)
	return exists, err
}

func checkLocaleRoute(ctx context.Context, tx *sql.Tx, locale string) error {
	for _, check := range []struct{ table, query string }{
		{"content", `SELECT EXISTS(SELECT 1 FROM content WHERE type='page' AND COALESCE(parent_id,'')='' AND status!='trash' AND lower(slug)=?)`},
		{"taxonomies", `SELECT EXISTS(SELECT 1 FROM taxonomies WHERE public=1 AND lower(slug)=?)`},
		{"content_types", `SELECT EXISTS(SELECT 1 FROM content_types WHERE public=1 AND (lower(slug)=? OR (has_archive=1 AND lower(archive_slug)=?)))`},
	} {
		exists, err := localeTableExists(ctx, tx, check.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		args := []any{strings.ToLower(locale)}
		if check.table == "content_types" {
			args = append(args, strings.ToLower(locale))
		}
		var collision bool
		if err := tx.QueryRowContext(ctx, check.query, args...).Scan(&collision); err != nil {
			return err
		}
		if collision {
			return &SettingError{Key: i18n.ContentLocalesSetting, Code: "admin.settings.error_locale_route"}
		}
	}
	return nil
}
