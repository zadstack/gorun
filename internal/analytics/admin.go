package analytics

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

// Admin paths (under /admin).
const (
	dashPath   = "/analytics"
	nowPath    = "/analytics/now"
	exportPath = "/analytics/export.csv"
)

// topLimit is how many rows a dashboard table shows before "Show all";
// showAllLimit caps "Show all" (the CSV export has every row).
const (
	topLimit     = 10
	showAllLimit = 500
)

// adminUI serves the dashboard, its CSV export, the "now" counter, the
// dashboard widget and the settings page.
type adminUI struct {
	m   *Module
	d   *kernel.Deps
	tpl *httpx.Templates
}

// registerAdmin adds Site → Analytics (analytics.view), Settings →
// Analytics (settings.manage) and the dashboard widget. Without the admin
// renderer it does nothing.
func (m *Module) registerAdmin(d *kernel.Deps) {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return
	}
	a := &adminUI{m: m, d: d, tpl: tpl}
	d.Admin.AddNav(kernel.NavItem{ID: "site.analytics", LabelKey: "analytics.nav", Icon: "chart-line",
		Href: "/admin" + dashPath, Permission: PermView, Group: "site", Position: 30})
	d.Admin.AddNav(kernel.NavItem{ID: "settings.analytics", LabelKey: "analytics.settings.nav", Icon: "chart-line",
		Href: "/admin" + settingsPath, Permission: permSettings, Group: "settings", Position: 450})
	d.Admin.AddWidget("analytics", a.widget)

	v := d.Router.Admin.With(auth.RequirePerm(PermView))
	v.Get(dashPath, a.dashboard)
	v.Get(nowPath, a.now)
	v.Get(exportPath, a.export)

	s := d.Router.Admin.With(auth.RequirePerm(permSettings))
	s.Get(settingsPath, a.settingsPage)
	s.Post(settingsPath, a.saveSettings)
	s.Post(settingsPath+"/delete", a.deleteData)
}

func (a *adminUI) t(ctx context.Context, key string, args ...any) string {
	if a.d.I18n == nil {
		return key
	}
	return a.d.I18n.T(i18n.Locale(ctx), key, args...)
}

func (a *adminUI) rtl(ctx context.Context) bool {
	return a.d.I18n != nil && a.d.I18n.Dir(i18n.Locale(ctx)) == "rtl"
}

func (a *adminUI) fail(w http.ResponseWriter, r *http.Request, err error) {
	a.d.Log.Error("analytics: admin", "err", err, "path", r.URL.Path)
	a.tpl.RenderError(w, r, http.StatusInternalServerError)
}

// ---- period and filters --------------------------------------------------

// period is the dashboard's range: [From, To) in UTC. Key is today, 7d,
// 30d, 12m or custom; First and Last are the inclusive days shown.
type period struct {
	Key         string
	From, To    time.Time
	First, Last string
	Invalid     bool // a custom range was asked for and could not be read
}

var periodKeys = []string{"today", "7d", "30d", "12m", "custom"}

func parsePeriod(q url.Values, now time.Time) period {
	today := startOfDay(now)
	tomorrow := today.AddDate(0, 0, 1)
	mk := func(key string, from time.Time) period {
		return period{Key: key, From: from, To: tomorrow, First: dayKey(from), Last: dayKey(today)}
	}
	switch q.Get("period") {
	case "today":
		return mk("today", today)
	case "30d":
		return mk("30d", today.AddDate(0, 0, -29))
	case "12m":
		return mk("12m", today.AddDate(-1, 0, 1))
	case "custom":
		from, err1 := time.Parse(time.DateOnly, q.Get("from"))
		last, err2 := time.Parse(time.DateOnly, q.Get("to"))
		if err1 == nil && err2 == nil && !last.Before(from) && last.Sub(from) < maxSeriesDays*24*time.Hour {
			return period{Key: "custom", From: from, To: last.AddDate(0, 0, 1), First: dayKey(from), Last: dayKey(last)}
		}
		p := mk("7d", today.AddDate(0, 0, -6))
		p.Invalid = true
		return p
	}
	return mk("7d", today.AddDate(0, 0, -6))
}

// filterDims are the dashboard filters, in chip order, with their query
// parameter (the same as the dim name).
var filterDims = []string{DimPage, DimRef, DimCountry, DimDevice}

// dashView is one dashboard request's state: the period, the filters and the
// query that reproduces both (for links).
type dashView struct {
	p    period
	f    Filter
	base url.Values
}

func (a *adminUI) viewOf(r *http.Request) dashView {
	q := r.URL.Query()
	p := parsePeriod(q, a.m.store.clock())
	base := url.Values{"period": {p.Key}}
	if p.Key == "custom" {
		base.Set("from", p.First)
		base.Set("to", p.Last)
	}
	f := Filter{From: p.From, To: p.To}
	for _, dim := range filterDims {
		v := q.Get(dim)
		if v == "" || len(v) > maxFilterLength {
			continue
		}
		base.Set(dim, v)
		switch dim {
		case DimPage:
			f.Page = v
		case DimRef:
			f.Ref = v
		case DimCountry:
			f.Country = v
		case DimDevice:
			f.Device = v
		}
	}
	return dashView{p: p, f: f, base: base}
}

// link is the dashboard URL with set applied ("" deletes a parameter).
func (v dashView) link(path string, set ...string) string {
	q := url.Values{}
	for k, vs := range v.base {
		q[k] = append([]string(nil), vs...)
	}
	for i := 0; i+1 < len(set); i += 2 {
		if set[i+1] == "" {
			q.Del(set[i])
		} else {
			q.Set(set[i], set[i+1])
		}
	}
	if len(q) == 0 {
		return "/admin" + path
	}
	return "/admin" + path + "?" + q.Encode()
}

// sourceFiltered reports a filter that submissions cannot honour (they
// carry only a page).
func (f Filter) sourceFiltered() bool { return f.Ref != "" || f.Country != "" || f.Device != "" }

// ---- dashboard ------------------------------------------------------------

type periodOpt struct {
	Key, Href string
	Current   bool
}

type chip struct {
	Dim, Value, Remove string
}

type card struct {
	Name, Value string
	Change      change
	NA          bool // not available under the current filters
}

type seriesRow struct{ Label, Visitors, Views string }

type tableRow struct {
	Key, Display, Href, Visitors, Views string
	Share                               int // percent of the period's visitors
}

type panel struct {
	Dim, CSV, More string
	Rows           []tableRow
	All            bool
	Capped         bool // "Show all" stopped at Max rows
	Max            int  // the "Show all" cap, when All
}

type tableGroup struct {
	ID     string
	Panels []panel
}

type formView struct {
	Form, Page, Href, Views, Submits, Rate string
}

type dashPage struct {
	Period          period
	Periods         []periodOpt
	CustomFrom      string
	CustomTo        string
	Hidden          []hiddenField // filters kept by the period form
	Chips           []chip
	Clear           string
	Cards           []card
	Chart           template.HTML
	Hourly          bool
	Series          []seriesRow
	FirstLabel      string
	LastLabel       string
	Groups          []tableGroup
	FormsShown      bool
	FormsUnfiltered bool
	Forms           []formView
	FormsCSV        string
	SeriesCSV       string
	Now             string
	NowURL          string
}

type hiddenField struct{ Name, Value string }

// tableDims groups the dashboard tables; a group with several dims is
// shown as tabs (stacked sections without JS).
var tableDims = []struct {
	id   string
	dims []string
}{
	{"pages", []string{DimPage}},
	{"sources", []string{DimRef, DimUTM}},
	{"countries", []string{DimCountry}},
	{"devices", []string{DimDevice, DimBrowser, DimOS}},
}

func isFilterDim(dim string) bool {
	for _, d := range filterDims {
		if d == dim {
			return true
		}
	}
	return false
}

// display is how a key of dim is shown (paths decoded, known words
// translated). Unknown values are shown as stored.
func (a *adminUI) display(ctx context.Context, dim, key string) string {
	switch dim {
	case DimPage:
		return displayPath(key)
	case DimRef:
		if key == "direct" {
			return a.t(ctx, "analytics.value.direct")
		}
	case DimCountry:
		if key == unknownCountry {
			return a.t(ctx, "analytics.value.unknown")
		}
	case DimDevice:
		switch key {
		case "mobile", "tablet", "desktop", "unknown":
			return a.t(ctx, "analytics.device."+key)
		}
	case DimBrowser, DimOS:
		if key == "other" {
			return a.t(ctx, "analytics.value.other")
		}
	}
	if key == "" {
		return a.t(ctx, "analytics.value.none")
	}
	return key
}

func (a *adminUI) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loc := i18n.Locale(ctx)
	v := a.viewOf(r)
	st := a.m.store
	sum, err := st.Summary(ctx, v.f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	pts, err := st.Series(ctx, v.f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	data := dashPage{Period: v.p, CustomFrom: v.p.First, CustomTo: v.p.Last, NowURL: "/admin" + nowPath,
		Hourly: v.p.To.Sub(v.p.From) <= 24*time.Hour, FormsUnfiltered: v.f.sourceFiltered()}
	for _, k := range periodKeys {
		if k == "custom" {
			continue
		}
		data.Periods = append(data.Periods, periodOpt{Key: k, Href: v.link(dashPath, "period", k, "from", "", "to", ""), Current: v.p.Key == k})
	}
	for _, dim := range filterDims {
		if val := v.base.Get(dim); val != "" {
			data.Chips = append(data.Chips, chip{Dim: dim, Value: a.display(ctx, dim, val), Remove: v.link(dashPath, dim, "")})
			data.Hidden = append(data.Hidden, hiddenField{Name: dim, Value: val})
		}
	}
	if len(data.Chips) > 0 {
		set := []string{}
		for _, dim := range filterDims {
			set = append(set, dim, "")
		}
		data.Clear = v.link(dashPath, set...)
	}

	prev := Summary{}
	if sum.Prev != nil {
		prev = *sum.Prev
	}
	data.Cards = []card{
		{Name: "visitors", Value: formatInt(loc, sum.Visitors), Change: changeOf(loc, float64(sum.Visitors), float64(prev.Visitors))},
		{Name: "views", Value: formatInt(loc, sum.Views), Change: changeOf(loc, float64(sum.Views), float64(prev.Views))},
		{Name: "views_per_visitor", Value: formatFloat(loc, sum.ViewsPerVisitor), Change: changeOf(loc, sum.ViewsPerVisitor, prev.ViewsPerVisitor)},
		{Name: "submits", Value: formatInt(loc, sum.Submits), Change: changeOf(loc, float64(sum.Submits), float64(prev.Submits)), NA: data.FormsUnfiltered},
	}

	data.Chart = lineChart(pts, a.chartText(ctx, pts, data.Hourly), a.rtl(ctx))
	for _, p := range pts {
		data.Series = append(data.Series, seriesRow{Label: pointLabel(p.At, data.Hourly), Visitors: formatInt(loc, p.Visitors), Views: formatInt(loc, p.Views)})
	}
	if len(data.Series) > 0 {
		data.FirstLabel, data.LastLabel = data.Series[0].Label, data.Series[len(data.Series)-1].Label
	}
	data.SeriesCSV = v.link(exportPath, "table", "series")

	all := r.URL.Query().Get("all")
	for _, g := range tableDims {
		tg := tableGroup{ID: g.id}
		for _, dim := range g.dims {
			limit := topLimit
			if all == dim {
				limit = showAllLimit
			}
			rows, err := st.Top(ctx, v.f, dim, limit+1) // one more tells whether rows were left out
			if err != nil {
				a.fail(w, r, err)
				return
			}
			pn := panel{Dim: dim, CSV: v.link(exportPath, "table", dim), All: all == dim, Max: showAllLimit}
			if len(rows) > limit {
				rows = rows[:limit]
				if pn.All {
					pn.Capped = true
				} else {
					pn.More = v.link(dashPath, "all", dim) + "#t-" + dim
				}
			}
			for _, row := range rows {
				tr := tableRow{Key: row.Key, Display: a.display(ctx, dim, row.Key),
					Visitors: formatInt(loc, row.Visitors), Views: formatInt(loc, row.Views)}
				if sum.Visitors > 0 {
					tr.Share = min(100, row.Visitors*100/sum.Visitors)
				}
				if isFilterDim(dim) && v.base.Get(dim) != row.Key {
					tr.Href = v.link(dashPath, dim, row.Key)
				}
				pn.Rows = append(pn.Rows, tr)
			}
			tg.Panels = append(tg.Panels, pn)
		}
		data.Groups = append(data.Groups, tg)
	}

	if !data.FormsUnfiltered {
		forms, err := st.Forms(ctx, v.f)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		for _, f := range forms {
			data.Forms = append(data.Forms, formView{Form: f.Form, Page: displayPath(f.Page), Href: "/admin/forms/" + url.PathEscape(f.Form),
				Views: formatInt(loc, f.Views), Submits: formatInt(loc, f.Submits), Rate: formatPct(loc, f.Rate)})
		}
	}
	data.FormsShown = len(data.Forms) > 0 || data.FormsUnfiltered
	data.FormsCSV = v.link(exportPath, "table", "forms")

	n, err := st.Now(ctx, st.clock())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	data.Now = formatInt(loc, n)
	a.tpl.Render(w, r, "pages/analytics/dashboard.html", data)
}

func pointLabel(at time.Time, hourly bool) string {
	if hourly {
		return at.UTC().Format("15:04")
	}
	return at.UTC().Format(time.DateOnly)
}

func (a *adminUI) chartText(ctx context.Context, pts []Point, hourly bool) chartText {
	txt := chartText{Title: a.t(ctx, "analytics.chart.title")}
	if len(pts) == 0 {
		return txt
	}
	peak := pts[0]
	for _, p := range pts {
		if p.Views > peak.Views {
			peak = p
		}
	}
	loc := i18n.Locale(ctx)
	key := "analytics.chart.desc_daily"
	if hourly {
		key = "analytics.chart.desc_hourly"
	}
	txt.Desc = a.t(ctx, key, pointLabel(pts[0].At, hourly), pointLabel(pts[len(pts)-1].At, hourly),
		formatInt(loc, peak.Views), pointLabel(peak.At, hourly))
	return txt
}

// now answers {"visitors": n, "label": "<n formatted>"} for the dashboard's
// 30-second refresh.
func (a *adminUI) now(w http.ResponseWriter, r *http.Request) {
	st := a.m.store
	n, err := st.Now(r.Context(), st.clock())
	if err != nil {
		a.d.Log.Error("analytics: now", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Visitors int    `json:"visitors"`
		Label    string `json:"label"`
	}{n, formatInt(i18n.Locale(r.Context()), n)})
}

// ---- CSV ------------------------------------------------------------------

// export writes one dashboard table as CSV (UTF-8 with BOM), honouring the
// period and filters: ?table= a Top dim, forms or series. Cells starting
// with = + - @ (or a tab or CR) are prefixed with ' so spreadsheets never
// evaluate them.
func (a *adminUI) export(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := a.viewOf(r)
	table := r.URL.Query().Get("table")
	var recs [][]string
	switch {
	case table == "series":
		pts, err := a.m.store.Series(ctx, v.f)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		recs = append(recs, []string{"at", "visitors", "views"})
		for _, p := range pts {
			recs = append(recs, []string{p.At.UTC().Format(time.RFC3339), strconv.Itoa(p.Visitors), strconv.Itoa(p.Views)})
		}
	case table == "forms":
		forms, err := a.m.store.Forms(ctx, v.f)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		recs = append(recs, []string{"form", "page", "views", "submits", "rate"})
		for _, f := range forms {
			recs = append(recs, []string{f.Form, f.Page, strconv.Itoa(f.Views), strconv.Itoa(f.Submits), strconv.FormatFloat(f.Rate, 'f', 4, 64)})
		}
	case isTopDim(table):
		rows, err := a.m.store.Top(ctx, v.f, table, 0)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		recs = append(recs, []string{"key", "visitors", "views"})
		for _, row := range rows {
			recs = append(recs, []string{row.Key, strconv.Itoa(row.Visitors), strconv.Itoa(row.Views)})
		}
	default:
		a.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	var buf bytes.Buffer
	buf.WriteString("\xef\xbb\xbf")
	cw := csv.NewWriter(&buf)
	for _, rec := range recs {
		for i := range rec {
			rec[i] = csvSafe(rec[i])
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		a.fail(w, r, err)
		return
	}
	name := "analytics-" + table + "-" + v.p.First + "-" + v.p.Last + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = buf.WriteTo(w)
}

func isTopDim(dim string) bool {
	for _, d := range topDims {
		if d == dim {
			return true
		}
	}
	return false
}

// csvSafe neutralises spreadsheet formulas (same rule as site's export).
func csvSafe(v string) string {
	if v != "" && (v[0] == '=' || v[0] == '+' || v[0] == '-' || v[0] == '@' || v[0] == '\t' || v[0] == '\r') {
		return "'" + v
	}
	return v
}

// ---- widget ---------------------------------------------------------------

type widgetData struct {
	Visitors, Views string
	VisitorsChange  change
	ViewsChange     change
	Spark           template.HTML
}

// widget is the dashboard card: the last 7 days' visitors and views with a
// sparkline, linking to the full page. Empty without analytics.view.
func (a *adminUI) widget(ctx context.Context, r *http.Request) (template.HTML, error) {
	if !auth.Can(ctx, PermView) {
		return "", nil
	}
	loc := i18n.Locale(ctx)
	st := a.m.store
	p := parsePeriod(url.Values{}, st.clock())
	f := Filter{From: p.From, To: p.To}
	sum, err := st.Summary(ctx, f)
	if err != nil {
		return "", err
	}
	pts, err := st.Series(ctx, f)
	if err != nil {
		return "", err
	}
	prev := Summary{}
	if sum.Prev != nil {
		prev = *sum.Prev
	}
	data := widgetData{Visitors: formatInt(loc, sum.Visitors), Views: formatInt(loc, sum.Views),
		VisitorsChange: changeOf(loc, float64(sum.Visitors), float64(prev.Visitors)),
		ViewsChange:    changeOf(loc, float64(sum.Views), float64(prev.Views)),
		Spark:          sparkline(pts, a.rtl(ctx))}
	bw := &bufferWriter{header: http.Header{}}
	a.tpl.RenderWith(bw, r, http.StatusOK, "", "pages/analytics/widget.html", data)
	if bw.status >= http.StatusBadRequest {
		return "", errors.New("analytics: render widget")
	}
	return template.HTML(bw.buf.String()), nil //nolint:gosec // output of html/template
}

// bufferWriter captures a rendered fragment.
type bufferWriter struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (b *bufferWriter) Header() http.Header { return b.header }
func (b *bufferWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.buf.Write(p)
}
func (b *bufferWriter) WriteHeader(code int) { b.status = code }
