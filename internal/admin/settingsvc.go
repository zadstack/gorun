package admin

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	_ "time/tzdata" // timezone validation must not depend on the host's zoneinfo

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/theme"
)

const maxSettingLen = 500

// generalKeys are the General screen and settings API fields.
var (
	generalKeys = []string{"site.title", "site.tagline", "site.locale", "site.timezone", "site.base_url", "site.theme", "site.front_page",
		"site.logo", "contact.email", "site.posts_per_page"}
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// generalInput is the Settings → General form.
type generalInput struct {
	Title        string `form:"title"`
	Tagline      string `form:"tagline"`
	Locale       string `form:"locale"`
	Timezone     string `form:"timezone"`
	BaseURL      string `form:"base_url"`
	Theme        string `form:"theme"`
	FrontPage    string `form:"front_page"`
	Logo         string `form:"logo"`           // media id
	ContactEmail string `form:"contact_email"`  // contact form recipient
	PostsPerPage string `form:"posts_per_page"` // 1–100, stored as a number
}

func (h *handlers) contentService() (content.Service, bool) {
	return kernel.Get[content.Service](h.d, kernel.ServiceContent)
}

func (h *handlers) themeService() (theme.Service, bool) {
	return kernel.Get[theme.Service](h.d, kernel.ServiceTheme)
}

func (h *handlers) mediaService() (media.Service, bool) {
	return kernel.Get[media.Service](h.d, kernel.ServiceMedia)
}

// validLogo accepts "" or the id of an existing media item (any short id
// when the media module is absent).
func (h *handlers) validLogo(ctx context.Context, id string) bool {
	if id == "" {
		return true
	}
	if ms, ok := h.mediaService(); ok {
		_, err := ms.Get(ctx, id)
		return err == nil
	}
	return len(id) <= 64 && !strings.ContainsAny(id, " \t\r\n/")
}

// normalizeBaseURL accepts an absolute http(s) URL with a host and no path,
// query, fragment or credentials; it returns it without a trailing slash.
func normalizeBaseURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

func validTimezone(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// validateGeneral returns the settings to store and field errors (i18n
// keys).
func (h *handlers) validateGeneral(ctx context.Context, in generalInput) (map[string]any, map[string]string) {
	fields := map[string]string{}
	vals := map[string]any{}
	if strings.TrimSpace(in.PostsPerPage) == "" {
		in.PostsPerPage = "10" // the default listing size
	}
	for _, f := range []struct{ field, key, value string }{
		{"title", "site.title", in.Title}, {"tagline", "site.tagline", in.Tagline}, {"locale", "site.locale", in.Locale},
		{"timezone", "site.timezone", in.Timezone}, {"base_url", "site.base_url", in.BaseURL},
		{"contact_email", "contact.email", in.ContactEmail}, {"posts_per_page", "site.posts_per_page", in.PostsPerPage},
	} {
		v, err := ValidateGeneralSetting(f.key, f.value)
		var se *SettingError
		if errors.As(err, &se) {
			fields[f.field] = se.Code
			continue
		}
		vals[f.key] = v
	}
	if n, ok := PostsPerPage(in.PostsPerPage); ok {
		vals["site.posts_per_page"] = n // the site reads it as a JSON number
	}
	if logo := strings.TrimSpace(in.Logo); h.validLogo(ctx, logo) {
		vals["site.logo"] = logo
	} else {
		fields["logo"] = "admin.settings.error_logo"
	}
	if th := strings.TrimSpace(in.Theme); h.validTheme(ctx, th) {
		vals["site.theme"] = th
	} else {
		fields["theme"] = "admin.settings.error_theme"
	}
	if fp := strings.TrimSpace(in.FrontPage); h.validFrontPage(ctx, fp) {
		vals["site.front_page"] = fp
	} else {
		fields["front_page"] = "admin.settings.error_front_page"
	}
	return vals, fields
}

func (h *handlers) validTheme(ctx context.Context, slug string) bool {
	if slug == "" {
		return true
	}
	if ts, ok := h.themeService(); ok {
		list, err := ts.List(ctx)
		if err == nil {
			return slices.ContainsFunc(list, func(i theme.Info) bool { return i.Slug == slug })
		}
	}
	return slugRe.MatchString(slug)
}

func (h *handlers) validFrontPage(ctx context.Context, id string) bool {
	if id == "" {
		return true
	}
	if cs, ok := h.contentService(); ok {
		it, err := cs.Get(ctx, id)
		if err != nil {
			return false
		}
		typ, err := cs.GetType(ctx, it.Type)
		return err == nil && typ.Public
	}
	return len(id) <= 64 && !strings.ContainsAny(id, " \t\r\n")
}

// saveSettings stores vals atomically and audits the changed key names
// (never values).
func (h *handlers) saveSettings(ctx context.Context, vals map[string]any) error {
	return h.saveSettingsGuarded(ctx, vals, nil)
}

func (h *handlers) saveSettingsGuarded(ctx context.Context, vals map[string]any, expected *string) error {
	keys := slices.Sorted(maps.Keys(vals))
	err := db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if expected != nil {
			current, err := generalSnapshot(ctx, tx)
			if err != nil {
				return err
			}
			if *expected == "" || generalEditToken(current) != *expected {
				return errGeneralConflict
			}
		}
		for _, k := range keys {
			if err := h.d.Settings.SetTx(ctx, tx, k, vals[k]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	h.d.Audit.Record(ctx, "settings.update", "settings", "", map[string][]string{"keys": keys})
	return nil
}
