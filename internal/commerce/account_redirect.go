package commerce

import (
	"context"
	"errors"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/redirects"
)

const (
	// legacyAccountPath is WooCommerce's account page; old links and
	// bookmarks point there.
	legacyAccountPath = "/my-account"
	// settingAccountRedirect records that the default redirect was created
	// (its id), so it is seeded once and a deliberate delete sticks.
	settingAccountRedirect = "commerce.my_account_redirect"
)

// seedAccountRedirect adds the default 301 /my-account → /account (lookups
// ignore the trailing slash, so /my-account/ is covered too) when the
// redirects module is booted, it was never seeded before, and neither a
// redirect nor a page owns /my-account. Safe to call on every boot.
func seedAccountRedirect(ctx context.Context, d *kernel.Deps) error {
	rs, ok := kernel.Get[redirects.Service](d, kernel.ServiceRedirects)
	if !ok || d.Settings.GetString(ctx, settingAccountRedirect, "") != "" {
		return nil
	}
	if owned, err := accountPathOwned(ctx, d, rs); err != nil || owned {
		return err
	}
	saved, err := rs.Save(ctx, redirects.Redirect{FromPath: legacyAccountPath, ToURL: "/account", Status: 301, Enabled: true})
	if err != nil {
		return err
	}
	return d.Settings.Set(ctx, settingAccountRedirect, saved.ID)
}

// accountPathOwned reports a redirect from /my-account, or a page with the
// slug my-account (in the site locale, no locale, or any supported one).
func accountPathOwned(ctx context.Context, d *kernel.Deps, rs redirects.Service) (bool, error) {
	key := redirects.NormalizePath(legacyAccountPath)
	list, _, err := rs.List(ctx, redirects.ListQuery{Search: key, PerPage: 100})
	if err != nil {
		return false, err
	}
	for _, r := range list {
		if redirects.NormalizePath(r.FromPath) == key {
			return true, nil
		}
	}
	cs, ok := kernel.Get[content.Service](d, kernel.ServiceContent)
	if !ok {
		return false, nil
	}
	locales := append([]string{d.Settings.GetString(ctx, "site.locale", i18n.DefaultLocale), ""}, i18n.Supported...)
	for _, loc := range locales {
		_, err := cs.GetBySlug(ctx, "page", "my-account", loc)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, content.ErrNotFound) {
			return false, err
		}
	}
	return false, nil
}
