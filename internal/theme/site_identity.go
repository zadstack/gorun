package theme

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/content"
)

// Site identity: colors, fonts, scheme, corner radius and custom CSS that a
// theme declaring the "identity" capability (theme.json "supports") accepts
// from Appearance → Theme settings, so a site gets its own look without
// overriding theme parts. Values are stored per theme as
// theme.<slug>.identity_* (and theme.<slug>.custom_css) and rendered by the
// {{identity .}} func after the theme's own stylesheets. Empty values keep
// the theme's design.
//
// The capability is not inherited: a child theme gets the settings only
// when its own theme.json declares it, so brand themes built on a catalog
// parent (with their own private tokens) are never restyled by settings
// they were not designed for.

const (
	idAccent     = "identity_accent"
	idBackground = "identity_background"
	idText       = "identity_text"
	idHeading    = "identity_heading_font"
	idBody       = "identity_body_font"
	idScheme     = "identity_scheme"
	idRadius     = "identity_radius"
	idLatinFont  = "identity_latin_font"
	idSpacing    = "identity_spacing"
	idCustomCSS  = "custom_css"

	// maxCustomCSS bounds the custom stylesheet stored in settings.
	maxCustomCSS = 20000
)

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// supportIdentity is the theme.json "supports" value that enables identity.
const supportIdentity = "identity"

// supportsIdentity reports whether the theme itself (not its parents)
// declares the identity capability.
func supportsIdentity(info Info) bool { return slices.Contains(info.Supports, supportIdentity) }

// SupportsIdentity is the exported form of supportsIdentity for public
// handlers (e.g. /site/identity.css).
func SupportsIdentity(info Info) bool { return supportsIdentity(info) }

// identityFont is a font offered by the identity settings: Arabic-capable
// families from Google Fonts (allowed by the CSP), plus the bundled Zad font.
type identityFont struct {
	Value, Label, Family, Google string
}

var identityFonts = []identityFont{
	{"zad", "Zad", "Zad,'Zad Font',Tahoma,sans-serif", ""},
	{"ibm-plex-arabic", "IBM Plex Sans Arabic", "'IBM Plex Sans Arabic',system-ui,sans-serif", "IBM+Plex+Sans+Arabic:wght@400;500;600;700"},
	{"noto-naskh", "Noto Naskh Arabic", "'Noto Naskh Arabic',Georgia,serif", "Noto+Naskh+Arabic:wght@400;600;700"},
	{"noto-kufi", "Noto Kufi Arabic", "'Noto Kufi Arabic',system-ui,sans-serif", "Noto+Kufi+Arabic:wght@400;600;700"},
	{"readex", "Readex Pro", "'Readex Pro',system-ui,sans-serif", "Readex+Pro:wght@400;500;600;700"},
	{"tajawal", "Tajawal", "Tajawal,system-ui,sans-serif", "Tajawal:wght@400;500;700"},
	{"cairo", "Cairo", "Cairo,system-ui,sans-serif", "Cairo:wght@400;600;700"},
	{"amiri", "Amiri", "Amiri,Georgia,serif", "Amiri:wght@400;700"},
}

// latinFonts are offered for Latin-script (English) text; the Arabic
// families above keep rendering Arabic. All load from Google Fonts.
var latinFonts = []identityFont{
	{"inter", "Inter", "Inter,system-ui,sans-serif", "Inter:wght@400;500;600;700"},
	{"source-serif", "Source Serif 4", "'Source Serif 4',Georgia,serif", "Source+Serif+4:wght@400;600;700"},
	{"ibm-plex-sans", "IBM Plex Sans", "'IBM Plex Sans',system-ui,sans-serif", "IBM+Plex+Sans:wght@400;500;600;700"},
	{"manrope", "Manrope", "Manrope,system-ui,sans-serif", "Manrope:wght@400;500;600;700"},
}

var identityRadii = map[string]string{"square": "0px", "soft": "8px", "round": "16px"}

// identitySpacing scales the --space-N tokens shared by the catalog themes;
// "normal" keeps the theme values.
var identitySpacing = map[string]float64{"compact": 0.85, "normal": 1, "relaxed": 1.2}

// spaceBase are the --space-1..7 pixel values of themes/_base/tokens.css.
var spaceBase = []float64{4, 8, 12, 16, 24, 32, 48}

func lbl(ar, en string) map[string]string { return map[string]string{"ar": ar, "en": en} }

func identityFontOptions() []content.Option { return fontOptions(identityFonts) }

func fontOptions(fonts []identityFont) []content.Option {
	out := []content.Option{{Value: "", Label: lbl("خط القالب", "Theme font")}}
	for _, f := range fonts {
		out = append(out, content.Option{Value: f.Value, Label: lbl(f.Label, f.Label)})
	}
	return out
}

// identityFields are appended to the settings schema of a theme that
// supports identity (a theme declaring the same key keeps its own definition).
func identityFields() []content.Field {
	fontHelp := lbl("كل الخطوط تدعم العربية. Zad مضمّن في GoRun؛ البقية تُحمَّل من Google Fonts.", "All fonts support Arabic. Zad ships with GoRun; the others load from Google Fonts.")
	colorHelp := lbl("لون بصيغة hex مثل #1f6feb. اتركه فارغًا لاستخدام لون القالب.", "A hex color such as #1f6feb. Leave empty to keep the theme color.")
	return []content.Field{
		{Key: idAccent, Type: "text", Label: lbl("اللون الأساسي", "Accent color"), Help: colorHelp},
		{Key: idBackground, Type: "text", Label: lbl("لون الخلفية", "Background color"), Help: colorHelp},
		{Key: idText, Type: "text", Label: lbl("لون النص", "Text color"), Help: colorHelp},
		{Key: idHeading, Type: "select", Label: lbl("خط العناوين", "Heading font"), Options: identityFontOptions(), Help: fontHelp},
		{Key: idBody, Type: "select", Label: lbl("خط النص", "Body font"), Options: identityFontOptions(), Help: fontHelp},
		{Key: idLatinFont, Type: "select", Label: lbl("الخط اللاتيني", "Latin font"), Options: fontOptions(latinFonts),
			Help: lbl("يُستخدم للنص الإنجليزي فقط؛ خطوط العربية أعلاه تبقى للعربية.", "Used for English text only; the Arabic fonts above stay for Arabic.")},
		{Key: idScheme, Type: "select", Label: lbl("الوضع", "Color scheme"), Options: []content.Option{
			{Value: "", Label: lbl("حسب القالب", "Theme default")},
			{Value: "light", Label: lbl("فاتح", "Light")},
			{Value: "dark", Label: lbl("داكن", "Dark")},
		}, Help: lbl("الداكن يطبّق لوحة ألوان داكنة متناسقة؛ الألوان التي تحددها أعلاه تبقى لها الأولوية.", "Dark applies a matching dark palette; colors set above still win.")},
		{Key: idRadius, Type: "select", Label: lbl("الحواف", "Corners"), Options: []content.Option{
			{Value: "", Label: lbl("حسب القالب", "Theme default")},
			{Value: "square", Label: lbl("حادة", "Square")},
			{Value: "soft", Label: lbl("ناعمة", "Soft")},
			{Value: "round", Label: lbl("دائرية", "Round")},
		}},
		{Key: idSpacing, Type: "select", Label: lbl("المسافات", "Spacing"), Options: []content.Option{
			{Value: "", Label: lbl("حسب القالب", "Theme default")},
			{Value: "compact", Label: lbl("مضغوط", "Compact")},
			{Value: "normal", Label: lbl("عادي", "Normal")},
			{Value: "relaxed", Label: lbl("مريح", "Relaxed")},
		}, Help: lbl("يغيّر مقياس المسافات بين العناصر في كل القالب.", "Scales the gaps between elements across the whole theme.")},
		{Key: idCustomCSS, Type: "textarea", Label: lbl("CSS مخصص", "Custom CSS"),
			Help: lbl("يُضاف بعد أنماط القالب ويبقى بعد تحديثه. بلا @import ولا وسوم HTML؛ بحد أقصى 20000 حرف.", "Added after the theme styles and kept across theme updates. No @import or HTML tags; up to 20000 characters.")},
	}
}

// withIdentity returns schema plus the identity fields it does not define.
func withIdentity(schema []content.Field) []content.Field {
	for _, f := range identityFields() {
		if !slices.ContainsFunc(schema, func(g content.Field) bool { return g.Key == f.Key }) {
			schema = append(schema, f)
		}
	}
	return schema
}

// validateIdentity checks identity values the content field rules cannot
// express; it returns an i18n error key per invalid field.
func validateIdentity(vals map[string]any) map[string]string {
	errs := map[string]string{}
	for _, k := range []string{idAccent, idBackground, idText} {
		if v := displayValue(vals[k]); v != "" && !hexColor.MatchString(v) {
			errs[k] = "theme.identity.error_color"
		}
	}
	if v := displayValue(vals[idCustomCSS]); v != "" {
		if err := checkCustomCSS(v); err != "" {
			errs[idCustomCSS] = err
		}
	}
	known := func(key string, ok func(string) bool) {
		if v := displayValue(vals[key]); v != "" && !ok(v) {
			errs[key] = "theme.identity.error_option"
		}
	}
	inFonts := func(fonts []identityFont) func(string) bool {
		return func(v string) bool { _, ok := findFontIn(fonts, v); return ok }
	}
	known(idHeading, inFonts(identityFonts))
	known(idBody, inFonts(identityFonts))
	known(idLatinFont, inFonts(latinFonts))
	known(idScheme, func(v string) bool { return v == "light" || v == "dark" })
	known(idRadius, func(v string) bool { _, ok := identityRadii[v]; return ok })
	known(idSpacing, func(v string) bool { _, ok := identitySpacing[v]; return ok })
	return errs
}

func checkCustomCSS(css string) string {
	low := strings.ToLower(css)
	switch {
	case len(css) > maxCustomCSS:
		return "theme.identity.error_css_size"
	case strings.Contains(low, "<"), strings.Contains(low, "@import"), strings.Contains(low, "expression("), strings.Contains(low, "javascript:"):
		return "theme.identity.error_css"
	}
	return ""
}

func findFont(v string) (identityFont, bool) { return findFontIn(identityFonts, v) }

func findFontIn(fonts []identityFont, v string) (identityFont, bool) {
	i := slices.IndexFunc(fonts, func(f identityFont) bool { return f.Value == v })
	if i < 0 {
		return identityFont{}, false
	}
	return fonts[i], true
}

// identityOnAccent is the text color for an accent fill (buttons, CTA
// bands): white when it reaches WCAG AA 4.5:1, else near-black, else black
// (one of white and black always reaches 4.58:1). Themes print it as
// var(--on-accent,#fff), so a light accent such as amber keeps its buttons
// readable instead of being rejected.
func identityOnAccent(accent string) string {
	for _, c := range []string{"#ffffff", "#111111"} {
		if contrast(c, accent) >= 4.5 {
			return c
		}
	}
	return "#000000"
}

// darkPalette is the scheme=dark base, overridden by explicit colors.
var darkPalette = [][2]string{
	{"--bg", "#111418"}, {"--panel", "#181c22"}, {"--panel-2", "#20252d"}, {"--ink", "#eef1f4"},
	{"--muted", "#a3acb8"}, {"--line", "rgba(238,241,244,.14)"}, {"--line-strong", "rgba(238,241,244,.3)"},
	{"--focus-ring", "#d7dee8"},
}

// identityParts holds the validated identity CSS body and optional Google Fonts
// families (already vetted against identityFonts).
type identityParts struct {
	CSS    string
	Google []string
}

func buildIdentity(get func(key string) string) identityParts {
	var vars [][2]string
	var rules []string
	var google []string
	switch get(idScheme) {
	case "dark":
		vars = append(vars, darkPalette...)
		vars = append(vars, [2]string{"color-scheme", "dark"})
	case "light":
		vars = append(vars, [2]string{"color-scheme", "light"})
	}
	color := func(key string, names ...string) {
		if v := get(key); hexColor.MatchString(v) {
			for _, n := range names {
				vars = append(vars, [2]string{n, v})
			}
		}
	}
	color(idBackground, "--bg", "--gp-bg", "--paper")
	color(idText, "--ink", "--gp-text")
	color(idAccent, "--green", "--blue", "--accent", "--gp-accent", "--link")
	if v := get(idAccent); hexColor.MatchString(v) {
		on := identityOnAccent(v)
		vars = append(vars, [2]string{"--green-hover", "color-mix(in srgb," + v + " 82%,#000)"}, [2]string{"--green-soft", "color-mix(in srgb," + v + " 12%,transparent)"},
			[2]string{"--on-accent", on}, [2]string{"--gp-on-accent", on})
	}
	if f, ok := findFont(get(idBody)); ok {
		vars = append(vars, [2]string{"--font", f.Family}, [2]string{"--font-ui", f.Family})
		if f.Google != "" {
			google = append(google, f.Google)
		}
		rules = append(rules, `:root:root[data-theme] body{font-family:var(--font)}`)
	}
	if f, ok := findFont(get(idHeading)); ok {
		vars = append(vars, [2]string{"--font-heading", f.Family})
		if f.Google != "" && !slices.Contains(google, f.Google) {
			google = append(google, f.Google)
		}
		rules = append(rules, `:root:root[data-theme] :where(h1,h2,h3,h4){font-family:var(--font-heading)}`)
	}
	if f, ok := findFontIn(latinFonts, get(idLatinFont)); ok {
		vars = append(vars, [2]string{"--font-latin", f.Family})
		if f.Google != "" && !slices.Contains(google, f.Google) {
			google = append(google, f.Google)
		}
		rules = append(rules, `:root:root[data-theme] :where(body,h1,h2,h3,h4,p,li,a,button,input):lang(en){font-family:var(--font-latin)}`)
	}
	if r, ok := identityRadii[get(idRadius)]; ok {
		vars = append(vars, [2]string{"--radius", r}, [2]string{"--gp-radius", r})
	}
	if k, ok := identitySpacing[get(idSpacing)]; ok && k != 1 {
		for i, base := range spaceBase {
			vars = append(vars, [2]string{"--space-" + strconv.Itoa(i+1), strconv.FormatFloat(math.Round(base*k*100)/100, 'f', -1, 64) + "px"})
		}
	}
	custom := get(idCustomCSS)
	if checkCustomCSS(custom) != "" {
		custom = ""
	}
	if len(vars) == 0 && len(rules) == 0 && custom == "" {
		return identityParts{}
	}
	var b strings.Builder
	if len(vars) > 0 {
		// :root:root[data-theme] (0,3,0) outranks the themes' html[data-theme="…"] tokens.
		b.WriteString(":root:root[data-theme]{")
		for _, v := range vars {
			b.WriteString(v[0] + ":" + v[1] + ";")
		}
		b.WriteString("}")
		if get(idScheme) == "dark" || get(idBackground) != "" || get(idText) != "" {
			b.WriteString(":root:root[data-theme] body{background:var(--bg);color:var(--ink)}")
		}
	}
	b.WriteString(strings.Join(rules, ""))
	if custom != "" {
		b.WriteString("\n/* custom css */\n" + custom)
	}
	return identityParts{CSS: b.String(), Google: google}
}

// IdentityStylesheet returns the cached site-identity CSS body for settings
// (empty when nothing is set). Used by /site/identity.css.
func IdentityStylesheet(get func(key string) string) string {
	return buildIdentity(get).CSS
}

// contentHash returns a short hex digest for cache-busting stylesheet URLs.
func contentHash(css string) string {
	sum := sha256.Sum256([]byte(css))
	return hex.EncodeToString(sum[:8])
}

// identityCSS builds font <link>s plus a cached stylesheet href for the stored
// values (already read from settings); "" when nothing is set. Every value is
// validated again here, so a hand-edited setting cannot inject markup.
// nonce is unused (kept for call-site compatibility); identity CSS is no longer
// an inline <style> block.
func identityCSS(get func(key string) string, _ string) template.HTML {
	parts := buildIdentity(get)
	if parts.CSS == "" && len(parts.Google) == 0 {
		return ""
	}
	var b strings.Builder
	if len(parts.Google) > 0 {
		// Families come from identityFonts only; Google Fonts expects them unescaped.
		q := "family=" + strings.Join(parts.Google, "&family=") + "&display=swap"
		b.WriteString(`<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin><link rel="stylesheet" href="https://fonts.googleapis.com/css2?` + template.HTMLEscapeString(q) + `">`)
	}
	if parts.CSS != "" {
		b.WriteString(`<link rel="stylesheet" href="/site/identity.css?v=` + contentHash(parts.CSS) + `" data-gorun-identity>`)
	}
	return template.HTML(b.String())
}

// identityInlineCSS is identityCSS for request-scoped previews: the draft
// is not stored, so the stylesheet is printed inline under the CSP nonce
// instead of linking /site/identity.css.
func identityInlineCSS(get func(key string) string, nonce string) template.HTML {
	parts := buildIdentity(get)
	if parts.CSS == "" && len(parts.Google) == 0 {
		return ""
	}
	var b strings.Builder
	if len(parts.Google) > 0 {
		q := "family=" + strings.Join(parts.Google, "&family=") + "&display=swap"
		b.WriteString(`<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin><link rel="stylesheet" href="https://fonts.googleapis.com/css2?` + template.HTMLEscapeString(q) + `">`)
	}
	if parts.CSS != "" {
		b.WriteString(`<style nonce="` + template.HTMLEscapeString(nonce) + `" data-gorun-identity>` + parts.CSS + `</style>`)
	}
	return template.HTML(b.String())
}
