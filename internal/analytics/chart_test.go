package analytics

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// pointXs returns the x of the first and last pair of an SVG points list.
func pointXs(t *testing.T, points string) (first, last float64) {
	t.Helper()
	pairs := strings.Fields(points)
	x := func(p string) float64 {
		v, err := strconv.ParseFloat(strings.SplitN(p, ",", 2)[0], 64)
		if err != nil {
			t.Fatalf("bad point %q", p)
		}
		return v
	}
	return x(pairs[0]), x(pairs[len(pairs)-1])
}

func chartPoints(n int) []Point {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{At: start.AddDate(0, 0, i), Visitors: i, Views: 2 * i}
	}
	return pts
}

func TestChartMirroredRTL(t *testing.T) {
	pts := chartPoints(7)
	for _, rtl := range []bool{false, true} {
		svg := string(lineChart(pts, chartText{Title: "T", Desc: "D"}, rtl))
		m := pointsRe.FindAllStringSubmatch(svg, -1)
		if len(m) != 2 {
			t.Fatalf("rtl=%v: %d lines, want 2 (visitors, views)", rtl, len(m))
		}
		for _, line := range m {
			first, last := pointXs(t, line[1])
			if rtl && first <= last || !rtl && first >= last {
				t.Errorf("rtl=%v: first x %v, last x %v", rtl, first, last)
			}
		}
	}
}

func TestChartAccessible(t *testing.T) {
	svg := string(lineChart(chartPoints(3), chartText{Title: "Visitors & views", Desc: "Daily <counts>"}, false))
	for _, want := range []string{`role="img"`, `<title id="`, `<desc id="`, `aria-labelledby="`, "Visitors &amp; views", "Daily &lt;counts&gt;"} {
		if !strings.Contains(svg, want) {
			t.Errorf("chart lacks %q:\n%s", want, svg)
		}
	}
}

func TestChartEmptyAndSingle(t *testing.T) {
	for _, pts := range [][]Point{nil, chartPoints(1), {{At: time.Now()}, {At: time.Now()}}} {
		svg := string(lineChart(pts, chartText{Title: "T"}, false))
		if strings.Contains(svg, "NaN") || strings.Contains(svg, "Inf") {
			t.Errorf("%d points: bad numbers in %s", len(pts), svg)
		}
	}
}

func TestSparkline(t *testing.T) {
	svg := string(sparkline(chartPoints(7), true))
	m := pointsRe.FindStringSubmatch(svg)
	if m == nil || !strings.Contains(svg, `aria-hidden="true"`) {
		t.Fatalf("sparkline: %s", svg)
	}
	if first, last := pointXs(t, m[1]); first <= last {
		t.Error("rtl sparkline not mirrored")
	}
}

func TestFormatNumberAndChange(t *testing.T) {
	cases := []struct {
		loc  string
		n    int
		want string
	}{{"en", 0, "0"}, {"en", 1234567, "1,234,567"}, {"en", -1200, "-1,200"}, {"ar", 1234, "١٬٢٣٤"}}
	for _, c := range cases {
		if got := formatInt(c.loc, c.n); got != c.want {
			t.Errorf("formatInt(%s, %d) = %q, want %q", c.loc, c.n, got, c.want)
		}
	}
	if got := formatFloat("en", 1.25); got != "1.3" && got != "1.2" {
		t.Errorf("formatFloat = %q", got)
	}
	if got := formatFloat("ar", 2.5); got != "٢٫٥" {
		t.Errorf("formatFloat ar = %q", got)
	}
	ch := []struct {
		cur, prev float64
		dir, pct  string
	}{{2, 1, "up", "+100%"}, {1, 2, "down", "−50%"}, {3, 3, "flat", "0%"}, {0, 0, "flat", "0%"}, {5, 0, "up", ""}}
	for _, c := range ch {
		got := changeOf("en", c.cur, c.prev)
		if got.Dir != c.dir || got.Pct != c.pct {
			t.Errorf("changeOf(%v, %v) = %+v, want %s %q", c.cur, c.prev, got, c.dir, c.pct)
		}
	}
	if got := changeOf("ar", 2, 1); got.Pct != "+١٠٠٪" {
		t.Errorf("ar change = %q", got.Pct)
	}
}

func TestDisplayPath(t *testing.T) {
	for in, want := range map[string]string{"/caf%C3%A9": "/café", "/a%2": "/a%2", "/plain": "/plain", "/a%20b": "/a b"} {
		if got := displayPath(in); got != want {
			t.Errorf("displayPath(%q) = %q, want %q", in, got, want)
		}
	}
}
