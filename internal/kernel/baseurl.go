package kernel

import (
	"context"
	"strings"

	"github.com/zadstack/gorun/internal/settings"
)

// SettingBaseURL is the admin-editable public base URL; it wins over
// Config.BaseURL (GORUN_BASE_URL).
const SettingBaseURL = "site.base_url"

// BaseURL is the one source of the site's public base URL (no trailing
// slash): the site.base_url setting when set, else Config.BaseURL. Use it
// for absolute links and the redirects own-host check. Never for cookie
// Secure flags or HSTS: those follow the request (httpx.SecureCookie,
// httpx.RequestIsHTTPS), so a wrong setting cannot lock admins out.
func BaseURL(ctx context.Context, d *Deps) string {
	if d == nil {
		return ""
	}
	return BaseURLOf(ctx, d.Settings, d.Config.BaseURL)
}

// BaseURLOf is BaseURL for code that holds the settings store and the
// configured base URL instead of *Deps.
func BaseURLOf(ctx context.Context, st *settings.Store, configured string) string {
	if st != nil {
		if v := strings.TrimSpace(st.GetString(ctx, SettingBaseURL, "")); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return strings.TrimRight(strings.TrimSpace(configured), "/")
}
