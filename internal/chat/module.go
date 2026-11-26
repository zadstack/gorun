// Package chat prints a live-chat widget (Chatwoot first) on the pages the
// theme renders, from three settings the services layer writes:
// chat.provider, chat.base_url and chat.website_token. The site's CSP is
// widened for the configured host only, and only on the responses that
// carry the widget. The website token is public: it is in every page.
package chat

import (
	"context"
	"database/sql"
	"io/fs"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
)

// Module name, settings and providers.
const (
	ModuleName = "chat"

	SettingProvider     = "chat.provider"
	SettingBaseURL      = "chat.base_url"
	SettingWebsiteToken = "chat.website_token"
	// SettingPrefix covers every chat setting (turning the service off).
	SettingPrefix = "chat."

	ProviderChatwoot = "chatwoot"
)

// Providers are the chat providers the widget knows.
var Providers = []string{ProviderChatwoot}

// Module is the chat kernel.Module.
type Module struct {
	settings *settings.Store
}

// New returns the chat module.
func New() *Module { return &Module{} }

// Name implements kernel.Module.
func (*Module) Name() string { return ModuleName }

// Migrations implements kernel.Module: chat keeps its settings in the
// settings table and has no tables of its own.
func (*Module) Migrations() fs.FS { return nil }

// Enabled implements kernel.OptionalModule: on unless the operator turned
// the addon off. Off, no page gets the widget whatever the settings say.
func (*Module) Enabled(ctx context.Context, d *sql.DB) (bool, error) {
	return kernel.AddonEnabled(ctx, d, ModuleName)
}

// Register installs the widget middleware on the public router.
func (m *Module) Register(d *kernel.Deps) error {
	m.settings = d.Settings
	d.Router.Public.Use(m.inject)
	return nil
}

// Widget is a complete, valid chat configuration.
type Widget struct {
	Provider, BaseURL, WebsiteToken string
}

// Load reads the chat settings; ok is false when the provider is empty or
// unknown, or a value is missing or not valid (a value written by hand past
// the services layer is checked again here, so it never reaches the CSP).
func Load(ctx context.Context, s *settings.Store) (w Widget, ok bool) {
	w = Widget{
		Provider:     s.GetString(ctx, SettingProvider, ""),
		BaseURL:      s.GetString(ctx, SettingBaseURL, ""),
		WebsiteToken: s.GetString(ctx, SettingWebsiteToken, ""),
	}
	if w.Provider != ProviderChatwoot || !ValidToken(w.WebsiteToken) {
		return w, false
	}
	base, ok := NormalizeBaseURL(w.BaseURL)
	if !ok || base != w.BaseURL {
		return w, false
	}
	return w, true
}

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)

// ValidToken reports a website token of 8 to 64 ASCII letters and digits.
func ValidToken(t string) bool { return tokenRe.MatchString(t) }

var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// NormalizeBaseURL accepts only a bare https origin, https://host[:port]
// with at most a trailing "/": no path, query, fragment, userinfo, wildcard
// or IP-literal brackets. It returns the origin lower-cased and without the
// trailing slash, so it is safe to place in a CSP source list.
func NormalizeBaseURL(raw string) (string, bool) {
	if raw == "" || strings.ContainsAny(raw, " \t\r\n;,'\"*\\?#@") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" ||
		(u.Path != "" && u.Path != "/") {
		return "", false
	}
	host, port := u.Hostname(), u.Port()
	host = strings.ToLower(host)
	if host == "" || len(host) > 253 || !hostRe.MatchString(host) {
		return "", false
	}
	if strings.HasSuffix(u.Host, ":") { // "https://host:" (empty port)
		return "", false
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", false
		}
		host = net.JoinHostPort(host, port)
	}
	return "https://" + host, true
}
