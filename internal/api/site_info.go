package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/theme"
)

// InformationValue distinguishes unavailable information from a known value and
// describes its source without exposing settings contents or filesystem paths.
type InformationValue struct {
	Value     *string `json:"value"`
	Source    string  `json:"source"`
	Available bool    `json:"available"`
	Reason    string  `json:"reason,omitempty"`
}

type PublicSiteIdentity struct {
	Title   InformationValue `json:"title"`
	Tagline InformationValue `json:"tagline"`
	BaseURL InformationValue `json:"base_url"`
}

type SiteLanguages struct {
	Default   InformationValue `json:"default"`
	Supported []string         `json:"supported" doc:"Locales accepted by the running i18n router; not a claim that every page is translated"`
	Source    string           `json:"source"`
	Direction string           `json:"direction"`
}

type SiteLogos struct {
	Primary InformationValue `json:"primary" doc:"Resolved public media URL for site.logo"`
}

// SiteInformation is additive; the authenticated /site structure contract is unchanged.
type SiteInformation struct {
	Version          InformationValue        `json:"version" doc:"Build of this running API instance, not an accepted or desired update version"`
	Identity         PublicSiteIdentity      `json:"identity"`
	Logos            SiteLogos               `json:"logos"`
	Languages        SiteLanguages           `json:"languages"`
	Appearance       *theme.AppearanceReport `json:"appearance"`
	AppearanceReason string                  `json:"appearance_reason,omitempty"`
	Limitations      []string                `json:"limitations"`
}

type SiteRuntime struct {
	Version            string    `json:"version"`
	APIContractVersion string    `json:"api_contract_version"`
	GoVersion          string    `json:"go_version"`
	OS                 string    `json:"os"`
	Arch               string    `json:"arch"`
	APIStartedAt       time.Time `json:"api_started_at"`
}

func information(value, source string) InformationValue {
	if value == "" {
		return InformationValue{Source: source, Reason: "not_configured"}
	}
	return InformationValue{Value: &value, Source: source, Available: true}
}

// Legacy/runtime URLs have not always passed the admin form's validation.
// Discovery must never turn credentials or signed query strings into public data.
func publicURLInformation(value InformationValue, relative bool) InformationValue {
	if value.Value == nil {
		return value
	}
	raw := *value.Value
	u, err := url.Parse(raw)
	valid := err == nil && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.Contains(raw, "\\")
	if valid {
		valid = (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
		if relative && u.Scheme == "" && u.Host == "" && strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
			valid = true
		}
	}
	if !valid {
		return InformationValue{Source: value.Source, Reason: "unsafe_public_url"}
	}
	return value
}

func settingInformation(ctx context.Context, d *kernel.Deps, key, fallback, fallbackSource string) (InformationValue, error) {
	var value string
	err := d.Settings.Get(ctx, key, &value)
	if errors.Is(err, settings.ErrNotFound) {
		if fallback != "" {
			return information(fallback, fallbackSource), nil
		}
		return information("", "settings:"+key), nil
	}
	if err != nil {
		return InformationValue{}, err
	}
	return information(value, "settings:"+key), nil
}

func registerSiteInformationOps(d *kernel.Deps) {
	// WithVersion is process configuration. Capture it at registration so an
	// accepted release setting (or a later embedded app boot) cannot replace it.
	version := CoreVersion
	running := SiteRuntime{Version: version, APIContractVersion: ContractVersion,
		GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, APIStartedAt: time.Now().UTC()}
	d.API.Add(Op("GET", "/site/info", "Public site identity, running build, languages and theme appearance with provenance",
		[]string{"site"}, nil, nil, SiteInformation{}, siteInformationHandler(d, version)))
	d.API.Add(Op("GET", "/site/runtime", "Protected running process information; no credentials or deployment paths",
		[]string{"site"}, []string{"settings.manage"}, nil, SiteRuntime{}, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			WriteItem(w, running)
		}))
}

func siteInformationHandler(d *kernel.Deps, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx := r.Context()
		out := SiteInformation{Version: information(version, "process.build"),
			Languages:   SiteLanguages{Supported: append([]string{}, i18n.Supported...), Source: "runtime:i18n.Supported"},
			Limitations: []string{"supported_languages_do_not_imply_translated_content", "only_site.logo_is_a_core_logo_setting"}}
		for _, field := range []struct {
			target                *InformationValue
			key, fallback, source string
		}{
			{&out.Identity.Title, "site.title", "", ""},
			{&out.Identity.Tagline, "site.tagline", "", ""},
			{&out.Identity.BaseURL, "site.base_url", d.Config.BaseURL, "config:base_url"},
			{&out.Languages.Default, "site.locale", "ar", "core:site_locale_default"},
			{&out.Logos.Primary, "site.logo", "", ""},
		} {
			value, err := settingInformation(ctx, d, field.key, field.fallback, field.source)
			if err != nil {
				Fail(w, r, err)
				return
			}
			*field.target = value
		}
		// Match kernel.BaseURL's empty-setting fallback and trailing slash normalization.
		base := out.Identity.BaseURL
		if base.Value == nil || strings.TrimSpace(*base.Value) == "" {
			out.Identity.BaseURL = information(strings.TrimRight(strings.TrimSpace(d.Config.BaseURL), "/"), "config:base_url")
		} else {
			out.Identity.BaseURL = information(strings.TrimRight(strings.TrimSpace(*base.Value), "/"), base.Source)
		}
		out.Identity.BaseURL = publicURLInformation(out.Identity.BaseURL, false)
		if out.Languages.Default.Value != nil && d.I18n != nil {
			out.Languages.Direction = d.I18n.Dir(*out.Languages.Default.Value)
		}
		// The theme renderer uses the translated core name when site.title is absent.
		if out.Identity.Title.Value == nil {
			var stored string
			if errors.Is(d.Settings.Get(ctx, "site.title", &stored), settings.ErrNotFound) {
				title := "GoRun"
				if out.Languages.Default.Value != nil && d.I18n != nil {
					title = d.I18n.T(*out.Languages.Default.Value, "core.app.name")
				}
				out.Identity.Title = information(title, "i18n:core.app.name")
			}
		}
		if out.Logos.Primary.Value != nil {
			id := *out.Logos.Primary.Value
			out.Logos.Primary = InformationValue{Source: "settings:site.logo", Reason: "media_module_unavailable"}
			if svc, ok := kernel.Get[media.Service](d, kernel.ServiceMedia); ok {
				logo, err := svc.Get(ctx, id)
				if err != nil && !errors.Is(err, media.ErrNotFound) {
					Fail(w, r, err)
					return
				}
				if err == nil && logo != nil {
					out.Logos.Primary = publicURLInformation(information(logo.URL, "settings:site.logo -> media.url"), true)
				} else {
					out.Logos.Primary.Reason = "media_not_found"
				}
			}
		}
		out.AppearanceReason = "theme_provider_unavailable"
		if provider, ok := kernel.Get[theme.AppearanceProvider](d, kernel.ServiceTheme); ok {
			report := provider.Appearance(ctx)
			out.Appearance = &report
			out.AppearanceReason = ""
		}
		WriteItem(w, out)
	}
}
