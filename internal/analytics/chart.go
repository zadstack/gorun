package analytics

import (
	"html/template"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The dashboard chart is a server-rendered SVG, no JS library. Its time axis
// runs from the inline start to the inline end, so in RTL the first point
// is drawn on the right (the x coordinates are mirrored). The SVG is
// stretched to its box (preserveAspectRatio none, non-scaling strokes) and
// holds no text, so it stays readable at 320px; the dates and numbers are
// in the HTML around it and in a visually hidden table.

const (
	chartW, chartH = 640.0, 200.0
	chartPad       = 6.0
	sparkW, sparkH = 120.0, 32.0
)

// chartText is the chart's accessible name and description.
type chartText struct{ Title, Desc string }

// lineChart draws visitors (dashed) and views (solid) over pts.
func lineChart(pts []Point, txt chartText, rtl bool) template.HTML {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 200" preserveAspectRatio="none" role="img" aria-labelledby="an-chart-title an-chart-desc" class="block w-full h-48" data-chart`)
	if rtl {
		b.WriteString(` data-rtl`)
	}
	b.WriteString(`>`)
	b.WriteString(`<title id="an-chart-title">` + template.HTMLEscapeString(txt.Title) + `</title>`)
	b.WriteString(`<desc id="an-chart-desc">` + template.HTMLEscapeString(txt.Desc) + `</desc>`)
	for i := 0; i <= 4; i++ {
		y := num(chartPad + (chartH-2*chartPad)*float64(i)/4)
		b.WriteString(`<line x1="0" x2="640" y1="` + y + `" y2="` + y + `" stroke="var(--line)" stroke-width="1" vector-effect="non-scaling-stroke"/>`)
	}
	if len(pts) > 0 {
		top := 1
		for _, p := range pts {
			top = max(top, p.Views, p.Visitors)
		}
		views := make([]float64, len(pts))
		visitors := make([]float64, len(pts))
		for i, p := range pts {
			views[i], visitors[i] = float64(p.Views), float64(p.Visitors)
		}
		b.WriteString(polyline(views, float64(top), chartW, chartH, rtl, `stroke="var(--blue)" stroke-width="2.5" data-line="views"`))
		b.WriteString(polyline(visitors, float64(top), chartW, chartH, rtl, `stroke="var(--green)" stroke-width="2.5" stroke-dasharray="6 4" data-line="visitors"`))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()) //nolint:gosec // built from escaped text and numbers
}

// sparkline is the widget's decorative views line (the numbers are beside it).
func sparkline(pts []Point, rtl bool) template.HTML {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 32" preserveAspectRatio="none" aria-hidden="true" focusable="false" class="block w-full h-8">`)
	if len(pts) > 0 {
		top, vs := 1, make([]float64, len(pts))
		for i, p := range pts {
			top, vs[i] = max(top, p.Views), float64(p.Views)
		}
		b.WriteString(polyline(vs, float64(top), sparkW, sparkH, rtl, `stroke="var(--blue)" stroke-width="2"`))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()) //nolint:gosec // numbers only
}

func polyline(vs []float64, top, w, h float64, rtl bool, attrs string) string {
	pad := chartPad * h / chartH
	step := 0.0
	if len(vs) > 1 {
		step = (w - 2*pad) / float64(len(vs)-1)
	}
	var b strings.Builder
	b.WriteString(`<polyline fill="none" stroke-linejoin="round" stroke-linecap="round" vector-effect="non-scaling-stroke" ` + attrs + ` points="`)
	for i, v := range vs {
		x := pad + step*float64(i)
		if len(vs) == 1 {
			x = w / 2
		}
		if rtl {
			x = w - x
		}
		y := pad + (h-2*pad)*(1-v/top)
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(num(x) + "," + num(y))
	}
	b.WriteString(`"/>`)
	return b.String()
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }

// Locale number formatting: every locale, ar included, uses the plain
// Western form (1,234, 2.5, +100%), like the rest of the admin. localize is
// the single place a locale-specific form would go.
func localize(_, s string) string { return s }

func group(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

func formatInt(loc string, n int) string {
	s := strconv.Itoa(n)
	sign := ""
	if n < 0 {
		sign, s = "-", s[1:]
	}
	return localize(loc, sign+group(s))
}

// formatFloat formats f with one decimal.
func formatFloat(loc string, f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		f = 0
	}
	s := strconv.FormatFloat(math.Abs(f), 'f', 1, 64)
	ip, frac, _ := strings.Cut(s, ".")
	sign := ""
	if f < 0 && s != "0.0" {
		sign = "-"
	}
	return localize(loc, sign+group(ip)+"."+frac)
}

// formatPct formats a 0..1 rate as a whole percent.
func formatPct(loc string, rate float64) string {
	return localize(loc, strconv.Itoa(int(math.Round(rate*100)))+"%")
}

// change is a card's comparison with the previous period: a signed percent
// (empty when the previous period had nothing) and a direction word, so it
// never relies on colour.
type change struct {
	Dir string // up, down or flat
	Pct string
}

func changeOf(loc string, cur, prev float64) change {
	if prev == 0 {
		if cur == 0 {
			return change{Dir: "flat", Pct: localize(loc, "0%")}
		}
		return change{Dir: "up"}
	}
	p := int(math.Round((cur - prev) / prev * 100))
	switch {
	case p > 0:
		return change{Dir: "up", Pct: "+" + localize(loc, strconv.Itoa(p)+"%")}
	case p < 0:
		return change{Dir: "down", Pct: "−" + localize(loc, strconv.Itoa(-p)+"%")}
	}
	return change{Dir: "flat", Pct: localize(loc, "0%")}
}

// displayPath decodes a stored (percent-encoded) path for display. A value
// that does not decode to clean UTF-8 text, or that holds a control or a
// format character (bidi overrides such as U+202E, zero-width characters),
// is shown as stored.
func displayPath(p string) string {
	d, err := url.PathUnescape(p)
	if err != nil || !utf8.ValidString(d) || strings.IndexFunc(d, unsafeRune) >= 0 {
		return p
	}
	return d
}

func unsafeRune(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }
