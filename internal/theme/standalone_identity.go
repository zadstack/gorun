package theme

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/kernel"
)

// StandaloneIdentity is the published identity subset safe for core-owned
// pages outside the theme renderer. It never includes custom CSS or drafts.
// CSS is generated from validated tokens; callers must use a CSP nonce when
// embedding it. FontStylesheets contains only bundled or allowlisted fonts.
type StandaloneIdentity struct {
	CSS             string
	FontStylesheets []string
}

// StandaloneIdentityFor also works when the theme module is disabled.
func StandaloneIdentityFor(ctx context.Context, d *kernel.Deps) StandaloneIdentity {
	if svc, ok := kernel.Get[interface {
		StandaloneIdentity(context.Context) StandaloneIdentity
	}](d, kernel.ServiceTheme); ok {
		return svc.StandaloneIdentity(ctx)
	}
	return StandaloneBrand(standaloneBuilderIdentity(ctx, d))
}

func standaloneBuilderIdentity(ctx context.Context, d *kernel.Deps) brand.Identity {
	var identity brand.Identity
	if d != nil && d.Settings != nil && d.Settings.Get(ctx, "builder.identity", &identity) == nil && brand.Validate(identity) == nil {
		return identity
	}
	return brand.Default()
}

func (s *service) StandaloneIdentity(ctx context.Context) StandaloneIdentity {
	identity := standaloneBuilderIdentity(ctx, s.d)
	accent, background, text := s.EffectiveIdentityColors(ctx)
	info := s.Active(ctx)
	published := func(key string) string {
		if !SupportsIdentity(info) || s.d.Settings == nil {
			return ""
		}
		return s.d.Settings.GetString(ctx, SettingKey(info.Slug, key), "")
	}
	// Appearance deliberately reports unknown when custom CSS can change a
	// browser cascade. Standalone pages exclude that CSS, so explicit safe
	// identity tokens remain applicable even in that case.
	if published(idScheme) == "dark" {
		background, text = identityDarkBackground, identityDarkInk
	}
	for _, item := range []struct {
		key  string
		dest *string
	}{{idAccent, &accent}, {idBackground, &background}, {idText, &text}} {
		if value := published(item.key); hexColor.MatchString(value) {
			*item.dest = value
		}
	}
	for _, item := range []struct {
		value string
		dest  *string
	}{{accent, &identity.Accent}, {background, &identity.Background}, {text, &identity.Text}} {
		if hexColor.MatchString(item.value) {
			color, _ := parseColor(item.value)
			*item.dest = fmt.Sprintf("#%02x%02x%02x", int(color[0]), int(color[1]), int(color[2]))
		}
	}
	// Hand-edited settings and a theme's low-contrast declaration must not
	// make authentication forms unreadable. Retain its accent fill, whose
	// contrasting label is derived by brand, and fall back for text/surface.
	if brand.Validate(identity) != nil {
		fallback := standaloneBuilderIdentity(ctx, s.d)
		identity.Background, identity.Text = fallback.Background, fallback.Text
	}
	out := StandaloneBrand(identity)
	if !SupportsIdentity(info) || s.d.Settings == nil {
		return out
	}
	fonts := buildIdentity(func(key string) string {
		switch key {
		case idBody, idHeading, idLatinFont:
			return published(key)
		default:
			return ""
		}
	})
	// Standalone module pages have no data-theme attribute. The generated
	// selectors contain only fixed prefixes and validated font families.
	out.CSS += strings.ReplaceAll(fonts.CSS, ":root:root[data-theme]", ":root:root")
	if (published(idBody) == "zad" || published(idHeading) == "zad") && !slices.Contains(out.FontStylesheets, "/static/admin/zad-font.css") {
		out.FontStylesheets = append(out.FontStylesheets, "/static/admin/zad-font.css")
	}
	if len(fonts.Google) > 0 {
		out.FontStylesheets = append(out.FontStylesheets, "https://fonts.googleapis.com/css2?family="+strings.Join(fonts.Google, "&family=")+"&display=swap")
	}
	return out
}

// StandaloneBrand derives surfaces, accent labels and readable links from a
// validated identity. Invalid input uses the standard GoRun identity.
func StandaloneBrand(identity brand.Identity) StandaloneIdentity {
	if brand.Validate(identity) != nil {
		identity = brand.Default()
	}
	link := readableInk(identity.Accent, identity.Background)
	scheme := "light"
	background, _ := parseColor(identity.Background)
	if luminance(background) < 0.5 {
		scheme = "dark"
	}
	css := brand.CSS(identity) + fmt.Sprintf(`:root{color-scheme:%s;--standalone-bg:%s;--standalone-panel:%s;--standalone-ink:%s;--standalone-muted:%s;--standalone-accent:%s;--standalone-link:%s;--standalone-on-accent:%s;--standalone-line:color-mix(in srgb,%s 55%%,%s);--standalone-tint:color-mix(in srgb,%s 8%%,%s);}`, scheme, identity.Background, identity.Background, identity.Text, identity.Text, identity.Accent, link, brand.OnAccent(identity.Accent), identity.Text, identity.Background, identity.Accent, identity.Background)
	out := StandaloneIdentity{CSS: css}
	if identity.Font == "zad" {
		out.FontStylesheets = []string{"/static/admin/zad-font.css"}
	}
	return out
}
