package theme

import (
	"fmt"
	"html/template"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The identity part of the site overview: which theme colors play which
// role (text, background, accent…), WCAG contrast between them, the
// theme's fonts, and a component specimen drawn with those values.
// Everything is read from the theme's files; nothing here is editable.

// Color roles, in display order. Each lists the last segment of a custom
// property name that may play it (--zs-paper, --bg, --panel…).
var colorRoles = []struct {
	Key   string
	Names []string
}{
	{"background", []string{"paper", "bg", "background", "base", "page"}},
	{"text", []string{"ink", "text", "fg", "foreground"}},
	{"accent", []string{"accent", "primary", "brand", "green", "gold"}},
	{"link", []string{"link", "blue"}},
	{"muted", []string{"muted"}},
	{"surface", []string{"surface", "panel", "card"}},
	{"line", []string{"line", "border"}},
	{"success", []string{"success"}},
	{"warning", []string{"warning", "amber"}},
	{"danger", []string{"danger", "red", "error"}},
}

type colorRole struct {
	Key   string // background, text, …: theme.overview.role_<key>
	Token designToken
}

type contrastRow struct {
	Key            string // theme.overview.pair_<key>
	Fg, Bg         string
	Style          template.CSS
	Ratio          string
	Level          string // aa | aa_large | fail
	FgName, BgName string
}

// specimenStyle holds the inline styles of the component specimen.
type specimenStyle struct {
	Page, Heading, Body, Muted, Link, Primary, Secondary, Input, Card, Badge, Notice, Line template.CSS
}

type themeFonts struct {
	Heading, Body string   // font-family stacks
	Families      []string // families named by the theme (Google Fonts and CSS)
	Links         []string // Google Fonts stylesheets the theme loads
}

// roles assigns theme colors to roles by their names; a role takes the
// first color whose last name segment matches and that is not a variant
// (hover, soft, strong, dark, light, 2…).
func roles(colors []designToken) []colorRole {
	var out []colorRole
	used := map[string]bool{}
	for _, r := range colorRoles {
		for _, want := range r.Names {
			found := false
			// Later tokens (the child theme's stylesheets) win over the parent's.
			for i := len(colors) - 1; i >= 0; i-- {
				c := colors[i]
				segs := strings.Split(strings.TrimPrefix(c.Name, "--"), "-")
				if used[c.Name] || segs[len(segs)-1] != want {
					continue
				}
				if _, ok := parseColor(c.Value); !ok {
					continue
				}
				out = append(out, colorRole{Key: r.Key, Token: c})
				used[c.Name] = true
				found = true
				break
			}
			if found {
				break
			}
		}
	}
	return out
}

func roleValue(rs []colorRole, key string) (designToken, bool) {
	for _, r := range rs {
		if r.Key == key {
			return r.Token, true
		}
	}
	return designToken{}, false
}

var rgbFuncRe = regexp.MustCompile(`^rgba?\(\s*(\d{1,3})[\s,]+(\d{1,3})[\s,]+(\d{1,3})`)

// parseColor reads #rgb, #rrggbb, #rrggbbaa and rgb()/rgba() (alpha ignored).
func parseColor(v string) ([3]float64, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if m := rgbFuncRe.FindStringSubmatch(v); m != nil {
		var c [3]float64
		for i := range 3 {
			n, _ := strconv.Atoi(m[i+1])
			if n > 255 {
				return c, false
			}
			c[i] = float64(n)
		}
		return c, true
	}
	if !strings.HasPrefix(v, "#") {
		return [3]float64{}, false
	}
	h := v[1:]
	switch len(h) {
	case 3, 4:
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	case 6, 8:
		h = h[:6]
	default:
		return [3]float64{}, false
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return [3]float64{}, false
	}
	return [3]float64{float64(n >> 16 & 0xff), float64(n >> 8 & 0xff), float64(n & 0xff)}, true
}

// luminance is WCAG 2 relative luminance.
func luminance(c [3]float64) float64 {
	var l [3]float64
	for i, v := range c {
		s := v / 255
		if s <= 0.03928 {
			l[i] = s / 12.92
		} else {
			l[i] = math.Pow((s+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
}

// contrast is the WCAG ratio between two colors (1…21); 0 when unreadable.
func contrast(a, b string) float64 {
	ca, ok1 := parseColor(a)
	cb, ok2 := parseColor(b)
	if !ok1 || !ok2 {
		return 0
	}
	la, lb := luminance(ca), luminance(cb)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func level(ratio float64) string {
	switch {
	case ratio >= 4.5:
		return "aa"
	case ratio >= 3:
		return "aa_large"
	}
	return "fail"
}

// onAccent picks the text color for a filled accent: the theme's text or
// background color, else white or black, whichever reads best.
func onAccent(rs []colorRole, accent string) string {
	best, bestRatio := "#ffffff", 0.0
	var cands []string
	for _, k := range []string{"text", "background"} {
		if t, ok := roleValue(rs, k); ok {
			cands = append(cands, t.Value)
		}
	}
	for _, c := range append(cands, "#ffffff", "#000000") {
		if r := contrast(c, accent); r > bestRatio {
			best, bestRatio = c, r
		}
		if bestRatio >= 4.5 {
			break
		}
	}
	return best
}

// contrastRows checks the pairs a reader meets: text, muted text and links
// on the background, text on surfaces, and text on the accent.
func contrastRows(rs []colorRole) []contrastRow {
	var out []contrastRow
	add := func(key, fgRole, bgRole string) {
		fg, ok1 := roleValue(rs, fgRole)
		bg, ok2 := roleValue(rs, bgRole)
		if !ok1 || !ok2 {
			return
		}
		out = append(out, row(key, fg.Value, bg.Value, fg.Name, bg.Name))
	}
	add("text_bg", "text", "background")
	add("muted_bg", "muted", "background")
	add("link_bg", "link", "background")
	add("text_surface", "text", "surface")
	if acc, ok := roleValue(rs, "accent"); ok {
		fg := onAccent(rs, acc.Value)
		out = append(out, row("on_accent", fg, acc.Value, fg, acc.Name))
	}
	return out
}

func row(key, fg, bg, fgName, bgName string) contrastRow {
	r := contrast(fg, bg)
	return contrastRow{Key: key, Fg: fg, Bg: bg, FgName: fgName, BgName: bgName, Ratio: fmt.Sprintf("%.2f", r), Level: level(r),
		Style: safeStyle("color: %s; background: %s", fg, bg)}
}

// safeStyle formats a style from values that passed safeCSSRe; any other
// value leaves the style empty.
func safeStyle(format string, vals ...string) template.CSS {
	args := make([]any, len(vals))
	for i, v := range vals {
		if !safeCSSRe.MatchString(v) {
			return ""
		}
		args[i] = v
	}
	return template.CSS(fmt.Sprintf(format, args...))
}

// specimen builds the component specimen's styles from the roles, with
// neutral fallbacks for roles the theme does not name.
func specimen(rs []colorRole, fonts themeFonts, radius string) specimenStyle {
	get := func(k, def string) string {
		if t, ok := roleValue(rs, k); ok {
			return t.Value
		}
		return def
	}
	bg, ink := get("background", "#ffffff"), get("text", "#111111")
	accent := get("accent", ink)
	surface, line := get("surface", bg), get("line", "#cccccc")
	muted, link := get("muted", ink), get("link", accent)
	if radius == "" || !safeCSSRe.MatchString(radius) {
		radius = "6px"
	}
	body, heading := fonts.Body, fonts.Heading
	if body == "" {
		body = "system-ui, sans-serif"
	}
	if heading == "" {
		heading = body
	}
	return specimenStyle{
		Page:      safeStyle("background: %s; color: %s; font-family: %s", bg, ink, body),
		Heading:   safeStyle("font-family: %s; color: %s", heading, ink),
		Body:      safeStyle("color: %s", ink),
		Muted:     safeStyle("color: %s", muted),
		Link:      safeStyle("color: %s", link),
		Primary:   safeStyle("background: %s; color: %s; border: 1px solid %s; border-radius: %s", accent, onAccent(rs, accent), accent, radius),
		Secondary: safeStyle("background: transparent; color: %s; border: 1px solid %s; border-radius: %s", ink, line, radius),
		Input:     safeStyle("background: %s; color: %s; border: 1px solid %s; border-radius: %s", surface, ink, line, radius),
		Card:      safeStyle("background: %s; border: 1px solid %s; border-radius: %s", surface, line, radius),
		Badge:     safeStyle("color: %s; border: 1px solid %s; border-radius: %s", accent, accent, radius),
		Notice:    safeStyle("background: %s; border: 1px solid %s; border-radius: %s; color: %s", surface, accent, radius, ink),
		Line:      safeStyle("border-color: %s", line),
	}
}

var (
	googleFontsRe = regexp.MustCompile(`https://fonts\.googleapis\.com/css2\?[^"'\s<>]+`)
	fontDeclRe    = regexp.MustCompile(`(?s)([^{}]*)\{[^{}]*?font-family\s*:\s*([^;{}]+)`)
	headingSelRe  = regexp.MustCompile(`\bh[1-3]\b`)
	bodySelRe     = regexp.MustCompile(`(^|[\s,])(html|body)\b`)
)

// fonts finds the theme's fonts: Google Fonts stylesheets linked from its
// templates and parts, font custom properties, and font-family rules
// (headings for the heading stack, body/html for the body stack).
func (s *service) fonts(chain []Info, fontTokens []designToken) themeFonts {
	var f themeFonts
	seenLink, seenFam := map[string]bool{}, map[string]bool{}
	addFam := func(fam string) {
		fam = strings.Trim(strings.TrimSpace(fam), `"'`)
		if fam != "" && !seenFam[fam] && len(f.Families) < 12 {
			seenFam[fam] = true
			f.Families = append(f.Families, fam)
		}
	}
	for _, info := range chain {
		for _, dir := range []string{"templates", "parts", "components"} {
			files, _ := filepath.Glob(filepath.Join(info.Dir, dir, "*.html"))
			for _, file := range files {
				b, err := os.ReadFile(file)
				if err != nil || len(b) > 1<<20 {
					continue
				}
				for _, link := range googleFontsRe.FindAllString(string(b), -1) {
					link = strings.ReplaceAll(link, "&amp;", "&")
					if seenLink[link] || len(f.Links) >= 4 {
						continue
					}
					seenLink[link] = true
					f.Links = append(f.Links, link)
					// Not url.Query: css2 URLs carry ";" (wght@400;700), which it drops.
					_, query, _ := strings.Cut(link, "?")
					for _, part := range strings.Split(query, "&") {
						if fam, ok := strings.CutPrefix(part, "family="); ok {
							fam, _, _ = strings.Cut(fam, ":")
							if name, err := url.QueryUnescape(fam); err == nil {
								addFam(name)
							}
						}
					}
				}
			}
		}
	}
	for _, t := range fontTokens {
		if strings.Contains(t.Name, "head") || strings.Contains(t.Name, "display") {
			f.Heading = or(f.Heading, t.Value)
		} else if !strings.Contains(t.Name, "mono") && !strings.Contains(t.Name, "code") {
			f.Body = or(f.Body, t.Value)
		}
		addFam(strings.Split(t.Value, ",")[0])
	}
	for _, info := range chain {
		files, _ := filepath.Glob(filepath.Join(info.Dir, "assets", "*.css"))
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err != nil || len(b) > 2<<20 {
				continue
			}
			for _, m := range fontDeclRe.FindAllStringSubmatch(string(b), -1) {
				sel, stack := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
				if strings.Contains(stack, "var(") || strings.Contains(stack, "monospace") {
					continue
				}
				switch {
				case headingSelRe.MatchString(sel):
					f.Heading = or(f.Heading, stack)
				case bodySelRe.MatchString(sel):
					f.Body = or(f.Body, stack)
				}
				addFam(strings.Split(stack, ",")[0])
			}
		}
	}
	// Themes that only link Google Fonts: the first family for the body,
	// the second (if any) for headings.
	if f.Body == "" && len(f.Families) > 0 {
		f.Body = quoteFamily(f.Families[0]) + ", system-ui, sans-serif"
	}
	if f.Heading == "" && len(f.Families) > 1 {
		f.Heading = quoteFamily(f.Families[1]) + ", " + f.Body
	}
	return f
}

func quoteFamily(f string) string {
	if strings.ContainsAny(f, " ") {
		return `"` + f + `"`
	}
	return f
}

// themeChain is the active theme with its parents, parent first.
func (s *service) themeChain(t Info) []Info {
	chain := []Info{t}
	for p := t.Parent; p != "" && len(chain) < 4; {
		info, err := findInfo(s.roots(), p)
		if err != nil {
			break
		}
		chain = append([]Info{info}, chain...)
		p = info.Parent
	}
	return chain
}
