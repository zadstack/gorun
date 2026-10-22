package theme

import (
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/settings"
)

// AppearanceProvider is optional so existing Service implementations remain
// compatible. Appearance performs no writes or external requests.
type AppearanceProvider interface {
	Appearance(context.Context) AppearanceReport
}

// AppearanceValue identifies a public declaration, or explains why no value
// can safely be reported. Available does not mean browser-computed style.
type AppearanceValue struct {
	Value     *string `json:"value"`
	Source    string  `json:"source"`
	Available bool    `json:"available"`
	Reason    string  `json:"reason,omitempty"`
}

// AppearanceTheme intentionally excludes paths, settings schemas and secrets.
type AppearanceTheme struct {
	Slug    AppearanceValue `json:"slug"`
	Name    AppearanceValue `json:"name"`
	Version AppearanceValue `json:"version"`
}

// AppearanceReport describes static declarations and validated identity
// settings, not the full CSS cascade. ObservedAt is the last filesystem read;
// settings are read for each report. InvalidateCache or restarting refreshes
// cached files. Only one theme snapshot is retained per service.
type AppearanceReport struct {
	Theme AppearanceTheme `json:"theme"`
	// Colors and Fonts are the effective values after site customisation
	// (identity settings) was applied to the theme's declarations; each
	// value names its source. Declared keeps the theme's own declarations.
	Colors      map[string]AppearanceValue `json:"colors" doc:"Effective values after site identity settings; source says which setting or theme token supplied each"`
	Fonts       map[string]AppearanceValue `json:"fonts" doc:"Effective values after site identity settings"`
	Declared    *AppearanceDeclared        `json:"declared" doc:"The theme's own declarations before site customisation; null when the theme cannot be read"`
	Overrides   []AppearanceOverride       `json:"overrides" doc:"Site settings that changed a declared value, with provenance"`
	Scheme      AppearanceValue            `json:"scheme" doc:"Site colour scheme setting (light, dark) or the theme default"`
	Radius      AppearanceValue            `json:"radius" doc:"Site corner-radius setting or the theme default"`
	CustomCSS   AppearanceCustomCSS        `json:"custom_css"`
	ObservedAt  time.Time                  `json:"observed_at"`
	Limitations []string                   `json:"limitations"`
}

// AppearanceDeclared is the theme's declaration layer only.
type AppearanceDeclared struct {
	Colors map[string]AppearanceValue `json:"colors"`
	Fonts  map[string]AppearanceValue `json:"fonts"`
}

// AppearanceOverride records one effective value that a site setting
// supplied instead of the theme declaration.
type AppearanceOverride struct {
	Role     string  `json:"role" doc:"colors.<role> or fonts.<role>"`
	Source   string  `json:"source"`
	Value    string  `json:"value"`
	Declared *string `json:"declared" doc:"The theme's declared value it replaced; null when the theme declared none"`
}

// AppearanceCustomCSS says whether site custom CSS is applied after the
// theme styles. Its effect cannot be reported per value without a browser.
type AppearanceCustomCSS struct {
	Present bool   `json:"present"`
	Bytes   int    `json:"bytes"`
	Source  string `json:"source"`
	Status  string `json:"status" enum:"none,applied_requires_browser_evaluation,invalid_not_applied"`
}

type appearanceSnapshot struct {
	key    string
	report AppearanceReport
}

type appearanceToken struct {
	name, value, source, reason string
}

var _ AppearanceProvider = (*service)(nil)

func appearanceKnown(value, source string) AppearanceValue {
	if value == "" {
		return appearanceMissing(source, "not_declared")
	}
	return AppearanceValue{Value: &value, Source: source, Available: true}
}

func appearanceMissing(source, reason string) AppearanceValue {
	return AppearanceValue{Source: source, Reason: reason}
}

// Appearance reuses the overview's color roles, CSS validation and identity
// settings rules. Its deliberately limited CSS reader also understands the
// :where roots used by shipped themes, without claiming to evaluate CSS.
func (s *service) Appearance(ctx context.Context) AppearanceReport {
	info := s.Active(ctx)
	source := "default:site.theme"
	if s.d.Config.Theme != "" {
		source = "config:theme"
	}
	if s.d.Settings != nil {
		var configured string
		err := s.d.Settings.Get(ctx, SettingTheme, &configured)
		if err != nil && !errors.Is(err, settings.ErrNotFound) {
			return unavailableAppearance("settings:site.theme", "settings_read_failed")
		}
		if err == nil {
			source = "settings:site.theme"
		}
	}
	// The loader rejects invalid identifiers, but Active preserves the raw
	// selection for protected diagnostics. Never expose it as a public slug or
	// interpolate it into provenance: legacy settings may contain private paths.
	if !slugRe.MatchString(info.Slug) {
		return unavailableAppearance(source, "invalid_theme_identifier")
	}
	key := info.Slug + "\x00" + info.Name + "\x00" + info.Version
	s.appearanceMu.Lock()
	if s.appearance == nil || s.appearance.key != key {
		s.appearance = &appearanceSnapshot{key: key, report: s.readAppearance(info)}
	}
	report := cloneAppearance(s.appearance.report)
	s.appearanceMu.Unlock()
	report.Theme.Slug = appearanceKnown(info.Slug, source)
	report.Declared = &AppearanceDeclared{Colors: maps.Clone(report.Colors), Fonts: maps.Clone(report.Fonts)}
	report.Overrides = []AppearanceOverride{}
	report.Scheme = appearanceMissing("theme:default", "theme_default")
	report.Radius = appearanceMissing("theme:default", "theme_default")
	report.CustomCSS = AppearanceCustomCSS{Status: "none"}
	if info.Dir == "" || s.d.Settings == nil {
		return report
	}
	override := func(kind, role, source, value string, declared map[string]AppearanceValue) {
		o := AppearanceOverride{Role: kind + "." + role, Source: source, Value: value}
		if d, ok := declared[role]; ok && d.Value != nil {
			v := *d.Value
			o.Declared = &v
		}
		report.Overrides = append(report.Overrides, o)
	}

	// Only validated settings that rendering actually accepts are public.
	if supportsIdentity(info) {
		setting := func(key string) string { return "settings:" + SettingKey(info.Slug, key) }
		values := map[string]string{}
		for _, key := range []string{idScheme, idAccent, idBackground, idText, idBody, idHeading, idCustomCSS, idRadius} {
			var value string
			err := s.d.Settings.Get(ctx, SettingKey(info.Slug, key), &value)
			if err != nil && !errors.Is(err, settings.ErrNotFound) {
				obscureAppearance(&report, setting(key), "settings_read_failed")
				return report
			}
			values[key] = value
		}
		get := func(key string) string { return values[key] }
		if scheme := get(idScheme); scheme == "dark" || scheme == "light" {
			report.Scheme = appearanceKnown(scheme, setting(idScheme))
		}
		if get(idScheme) == "dark" {
			palette := map[string]string{}
			for _, token := range darkPalette {
				palette[token[0]] = token[1]
			}
			for role, token := range map[string]string{"background": "--bg", "text": "--ink", "surface": "--panel", "muted": "--muted", "line": "--line"} {
				report.Colors[role] = appearanceKnown(palette[token], setting(idScheme))
				override("colors", role, setting(idScheme), palette[token], report.Declared.Colors)
			}
		}
		for role, key := range map[string]string{"background": idBackground, "text": idText, "accent": idAccent, "link": idAccent} {
			if value := get(key); hexColor.MatchString(value) {
				report.Colors[role] = appearanceKnown(value, setting(key))
				override("colors", role, setting(key), value, report.Declared.Colors)
			}
		}
		for role, key := range map[string]string{"body": idBody, "heading": idHeading} {
			if font, ok := findFont(get(key)); ok {
				report.Fonts[role] = appearanceKnown(font.Family, setting(key))
				override("fonts", role, setting(key), font.Family, report.Declared.Fonts)
			}
		}
		if r, ok := identityRadii[get(idRadius)]; ok {
			report.Radius = appearanceKnown(r, setting(idRadius))
		}
		if css := get(idCustomCSS); css != "" {
			report.CustomCSS = AppearanceCustomCSS{Present: true, Bytes: len(css), Source: setting(idCustomCSS), Status: "invalid_not_applied"}
			if checkCustomCSS(css) == "" {
				report.CustomCSS.Status = "applied_requires_browser_evaluation"
				obscureAppearance(&report, setting(idCustomCSS), "custom_css_requires_browser_evaluation")
			}
		}
	}
	var identity brand.Identity
	err := s.d.Settings.Get(ctx, "builder.identity", &identity)
	if err != nil && !errors.Is(err, settings.ErrNotFound) {
		obscureAppearance(&report, "settings:builder.identity", "settings_read_failed")
	} else if err == nil && brand.Validate(identity) == nil {
		// Rendering depends on the page's templates and stylesheet order. A saved
		// builder identity alone is not proof that it controls every page.
		obscureAppearance(&report, "settings:builder.identity", "builder_identity_requires_page_evaluation")
	}
	return report
}

func obscureAppearance(report *AppearanceReport, source, reason string) {
	for key := range report.Colors {
		report.Colors[key] = appearanceMissing(source, reason)
	}
	for key := range report.Fonts {
		report.Fonts[key] = appearanceMissing(source, reason)
	}
	report.Limitations = append(report.Limitations, reason)
}

func cloneAppearance(in AppearanceReport) AppearanceReport {
	clone := func(value AppearanceValue) AppearanceValue {
		if value.Value != nil {
			copied := *value.Value
			value.Value = &copied
		}
		return value
	}
	out := in
	out.Theme = AppearanceTheme{Slug: clone(in.Theme.Slug), Name: clone(in.Theme.Name), Version: clone(in.Theme.Version)}
	out.Colors, out.Fonts = maps.Clone(in.Colors), maps.Clone(in.Fonts)
	for key, value := range out.Colors {
		out.Colors[key] = clone(value)
	}
	for key, value := range out.Fonts {
		out.Fonts[key] = clone(value)
	}
	out.Limitations = slices.Clone(in.Limitations)
	out.Overrides = slices.Clone(in.Overrides)
	if in.Declared != nil {
		d := AppearanceDeclared{Colors: maps.Clone(in.Declared.Colors), Fonts: maps.Clone(in.Declared.Fonts)}
		for key, value := range d.Colors {
			d.Colors[key] = clone(value)
		}
		for key, value := range d.Fonts {
			d.Fonts[key] = clone(value)
		}
		out.Declared = &d
	}
	return out
}

func newAppearanceReport() AppearanceReport {
	report := AppearanceReport{
		Colors: map[string]AppearanceValue{}, Fonts: map[string]AppearanceValue{}, ObservedAt: time.Now().UTC(),
		Limitations: []string{
			"declared_tokens_not_browser_computed_styles",
			"stylesheet_load_order_media_queries_page_overrides_and_external_styles_are_not_evaluated",
			"theme_files_cached_until_theme_change_cache_invalidation_or_restart",
		},
	}
	for _, role := range colorRoles {
		report.Colors[role.Key] = appearanceMissing("theme:css", "no_supported_declaration")
	}
	for _, role := range []string{"body", "heading"} {
		report.Fonts[role] = appearanceMissing("theme:css", "no_supported_declaration")
	}
	return report
}

func unavailableAppearance(source, reason string) AppearanceReport {
	report := newAppearanceReport()
	value := appearanceMissing(source, reason)
	report.Theme = AppearanceTheme{Slug: value, Name: value, Version: value}
	obscureAppearance(&report, source, reason)
	return report
}

func (s *service) readAppearance(info Info) AppearanceReport {
	report := newAppearanceReport()
	source := "theme:" + info.Slug + ":manifest"
	report.Theme = AppearanceTheme{Name: appearanceKnown(info.Name, source), Version: appearanceKnown(info.Version, source)}
	chain, err := loadChain(s.roots(), info.Slug)
	if err != nil {
		report.Limitations = append(report.Limitations, "theme_or_parent_unavailable")
		return report
	}
	slices.Reverse(chain)
	var tokens []appearanceToken
	for _, item := range chain {
		values, limited := readAppearanceTokens(item, info.Slug)
		tokens = append(tokens, values...)
		if limited && !slices.Contains(report.Limitations, "some_stylesheets_unreadable_or_scan_limit_reached") {
			report.Limitations = append(report.Limitations, "some_stylesheets_unreadable_or_scan_limit_reached")
		}
	}
	values := map[string]appearanceToken{}
	for _, token := range tokens {
		values[token.name] = token
	}
	var colors []designToken
	resolvedReasons := map[string]string{}
	for _, token := range tokens {
		if values[token.name] != token {
			continue
		}
		value, reason := resolveAppearanceToken(token.value, values, 0)
		resolvedReasons[token.name] = reason
		if safeCSSRe.MatchString(value) && colorRe.MatchString(value) {
			colors = append(colors, designToken{Name: token.name, Value: value})
		}
	}
	for _, role := range appearanceRoles(colors) {
		token := values[role.Token.Name]
		reason := token.reason
		if reason == "" {
			reason = resolvedReasons[token.name]
		}
		if reason != "" {
			report.Colors[role.Key] = appearanceMissing(token.source, reason)
		} else {
			report.Colors[role.Key] = appearanceKnown(role.Token.Value, token.source)
		}
	}
	for role, names := range map[string][]string{"body": {"--font", "--font-body", "--font-ui"}, "heading": {"--font-heading", "--font-display"}} {
		for _, name := range names {
			if token, ok := values[name]; ok {
				if token.reason != "" {
					report.Fonts[role] = appearanceMissing(token.source, token.reason)
					break
				}
				value, reason := resolveAppearanceToken(token.value, values, 0)
				if reason != "" {
					report.Fonts[role] = appearanceMissing(token.source, reason)
					break
				}
				if value != "" && safeCSSRe.MatchString(value) {
					report.Fonts[role] = appearanceKnown(value, token.source)
					break
				}
			}
		}
	}
	return report
}

var (
	appearanceCommentRE   = regexp.MustCompile(`(?s)/\*.*?\*/`)
	appearanceBlockRE     = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	appearanceReferenceRE = regexp.MustCompile(`^var\((--[a-zA-Z0-9-]+)\)$`)
)

func resolveAppearanceToken(value string, tokens map[string]appearanceToken, depth int) (string, string) {
	if depth >= 8 {
		return "", "unresolved_css_variable"
	}
	if match := appearanceReferenceRE.FindStringSubmatch(value); match != nil {
		token, ok := tokens[match[1]]
		if !ok {
			return "", "unresolved_css_variable"
		}
		resolved, reason := resolveAppearanceToken(token.value, tokens, depth+1)
		if token.reason != "" {
			reason = token.reason
		}
		return resolved, reason
	}
	if strings.Contains(value, "var(") {
		return "", "unresolved_css_variable"
	}
	return value, ""
}

// readAppearanceTokens bounds local file IO and retains only known root
// declarations. Arbitrary selectors and conditional/locale-specific rules
// are not represented as universal site values.
func readAppearanceTokens(info Info, activeSlug string) ([]appearanceToken, bool) {
	files, err := filepath.Glob(filepath.Join(info.Dir, "assets", "*.css"))
	if err != nil {
		return nil, true
	}
	limited := len(files) > 64
	if limited {
		files = files[:64]
	}
	var root, scoped, conditional []appearanceToken
	budget := int64(8 << 20)
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			limited = true
			continue
		}
		limit := min(budget, 2<<20)
		body, err := io.ReadAll(io.LimitReader(f, limit+1))
		f.Close()
		budget -= int64(len(body))
		if err != nil || int64(len(body)) > limit {
			limited = true
			if budget <= 0 {
				break
			}
			continue
		}
		css := appearanceCommentRE.ReplaceAllString(string(body), "")
		position, depth := 0, 0
		for _, block := range appearanceBlockRE.FindAllStringSubmatchIndex(css, -1) {
			before := css[position:block[0]]
			depth += strings.Count(before, "{") - strings.Count(before, "}")
			position = block[1]
			selector := strings.TrimSpace(css[block[2]:block[3]])
			target := &root
			reason := ""
			switch selector {
			case ":root", ":where(:root)":
			case `html[data-theme="` + activeSlug + `"]`, `:where(html[data-theme="` + activeSlug + `"])`:
				target = &scoped
			default:
				if strings.Contains(selector, `html[data-theme="`+activeSlug+`"][dir=`) {
					reason = "conditional_css_requires_page_evaluation"
				} else {
					continue
				}
			}
			if depth != 0 {
				reason = "conditional_css_requires_page_evaluation"
			}
			if reason != "" {
				target = &conditional
			}
			for _, match := range cssVarRe.FindAllStringSubmatch(css[block[4]:block[5]], -1) {
				name := "--" + match[1]
				*target = append(*target, appearanceToken{name: name, value: strings.TrimSpace(match[2]), source: "theme:" + info.Slug + ":css-token:" + name, reason: reason})
			}
		}
		if budget <= 0 {
			limited = true
			break
		}
	}
	return append(append(root, scoped...), conditional...), limited
}

// appearanceRoles prefers explicit role tokens over prefixed brand aliases.
// Semantic variants such as --on-accent and --accent-ink describe components
// of a palette, not the site's primary accent or text. Keep overview's legacy
// heuristic unchanged for compatibility with its existing presentation.
func appearanceRoles(colors []designToken) []colorRole {
	canonicalNames := map[string]bool{}
	for _, role := range colorRoles {
		for _, name := range role.Names {
			canonicalNames[name] = true
		}
	}
	variants := map[string]bool{"on": true, "hover": true, "soft": true, "strong": true, "dark": true, "light": true, "active": true, "focus": true, "disabled": true, "button": true, "selection": true}
	var canonical, aliases []designToken
	for _, token := range colors {
		name := strings.TrimPrefix(token.Name, "--")
		if canonicalNames[name] {
			canonical = append(canonical, token)
			continue
		}
		segments := strings.Split(name, "-")
		safe := len(segments) > 1
		for _, prefix := range segments[:len(segments)-1] {
			if canonicalNames[prefix] || variants[prefix] {
				safe = false
				break
			}
		}
		if safe {
			aliases = append(aliases, token)
		}
	}
	chosen := map[string]colorRole{}
	for _, role := range roles(aliases) {
		chosen[role.Key] = role
	}
	for _, role := range roles(canonical) {
		chosen[role.Key] = role
	}
	out := make([]colorRole, 0, len(chosen))
	for _, role := range colorRoles {
		if value, ok := chosen[role.Key]; ok {
			out = append(out, value)
		}
	}
	return out
}
