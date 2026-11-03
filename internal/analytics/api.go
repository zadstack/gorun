package analytics

import (
	"errors"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// apiSummaryPath is GET /api/v1/analytics/summary.
const apiSummaryPath = "/v1/analytics/summary"

// API limits: the longest range one request may ask for (the dashboard's
// own custom range is bounded by maxSeriesDays) and the longest filter
// value (the same bound as the dashboard's filters).
const (
	maxAPIDays      = 400
	maxFilterLength = 512
)

// SummaryJSON is a period's headline numbers in the API.
type SummaryJSON struct {
	Visitors        int          `json:"visitors"`
	Views           int          `json:"views"`
	Submits         int          `json:"submits"`
	ViewsPerVisitor float64      `json:"views_per_visitor"`
	Prev            *SummaryJSON `json:"prev,omitempty"`
}

// PointJSON is one series bucket (hourly for a range of a day or less,
// else daily), starting at At (UTC).
type PointJSON struct {
	At       time.Time `json:"at"`
	Visitors int       `json:"visitors"`
	Views    int       `json:"views"`
}

// RowJSON is one key of a top list. Paths are percent-encoded as stored;
// campaign keys are source/medium/campaign.
type RowJSON struct {
	Key      string `json:"key"`
	Visitors int    `json:"visitors"`
	Views    int    `json:"views"`
}

// SummaryResponse is the body of GET /api/v1/analytics/summary.
type SummaryResponse struct {
	From    time.Time            `json:"from"`
	To      time.Time            `json:"to"`
	Summary SummaryJSON          `json:"summary"`
	Series  []PointJSON          `json:"series"`
	Top     map[string][]RowJSON `json:"top"`
}

// apiTops maps the response's top lists to their dims.
var apiTops = []struct{ name, dim string }{
	{"pages", DimPage}, {"sources", DimRef}, {"campaigns", DimUTM}, {"countries", DimCountry},
	{"devices", DimDevice}, {"browsers", DimBrowser}, {"os", DimOS},
}

func (m *Module) registerAPI(d *kernel.Deps) {
	params := []kernel.APIParam{
		{Name: "from", In: "query", Type: "string", Description: "Start: a UTC day (YYYY-MM-DD) or an RFC 3339 time. Default: 6 days before to."},
		{Name: "to", In: "query", Type: "string", Description: "End: a UTC day, included (YYYY-MM-DD), or an RFC 3339 time, excluded. Default: the end of today (UTC)."},
		{Name: "page", In: "query", Type: "string", Description: "Only this path (as stored, percent-encoded)."},
		{Name: "ref", In: "query", Type: "string", Description: "Only this referrer host (direct for none). Submissions are 0 under this filter."},
		{Name: "country", In: "query", Type: "string", Description: "Only this ISO country code (-- unknown). Submissions are 0 under this filter."},
		{Name: "device", In: "query", Type: "string", Description: "Only mobile, tablet, desktop or unknown. Submissions are 0 under this filter."},
	}
	d.API.Add(kernel.APIOperation{Method: "GET", Path: apiSummaryPath, Summary: "Site analytics for a period: headline numbers with the previous period, the visitors/views series and the top 10 of each table",
		Tags: []string{"analytics"}, Scopes: []string{PermView}, Params: params, Response: SummaryResponse{}, Handler: m.apiSummary,
		Contract: &kernel.APIContract{SuccessStatuses: []int{http.StatusOK}, Errors: []kernel.APIErrorResponse{
			{Status: http.StatusBadRequest, Code: "validation_failed", Description: "from or to is not a date or time, the range is empty or longer than 400 days, or a filter is longer than 512 bytes"}}}})
}

// apiRange reads from/to. A bare day for to includes that day.
func apiRange(r *http.Request, now time.Time) (Filter, map[string]string) {
	q := r.URL.Query()
	bad := map[string]string{}
	parse := func(name string, endDay bool) (time.Time, bool) {
		v := q.Get(name)
		if v == "" {
			return time.Time{}, false
		}
		if t, err := time.Parse(time.DateOnly, v); err == nil {
			if endDay {
				t = t.AddDate(0, 0, 1)
			}
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.UTC(), true
		}
		bad[name] = "want YYYY-MM-DD or an RFC 3339 time"
		return time.Time{}, false
	}
	from, okFrom := parse("from", false)
	to, okTo := parse("to", true)
	if !okTo {
		to = startOfDay(now).AddDate(0, 0, 1)
	}
	if !okFrom {
		from = startOfDay(to.Add(-time.Nanosecond)).AddDate(0, 0, -6)
	}
	switch {
	case len(bad) > 0:
	case !to.After(from):
		bad["to"] = "must be after from"
	case to.Sub(from) > maxAPIDays*24*time.Hour:
		bad["to"] = "the range is longer than 400 days; ask for at most 400 days per request"
	}
	f := Filter{From: from, To: to}
	for _, p := range []struct {
		name string
		dst  *string
	}{{"page", &f.Page}, {"ref", &f.Ref}, {"country", &f.Country}, {"device", &f.Device}} {
		v := q.Get(p.name)
		if len(v) > maxFilterLength {
			bad[p.name] = "longer than 512 bytes"
			continue
		}
		*p.dst = v
	}
	return f, bad
}

func (m *Module) apiSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, bad := apiRange(r, m.store.clock())
	if len(bad) > 0 {
		httpx.Error(w, http.StatusBadRequest, "validation_failed", "invalid range", bad)
		return
	}
	fail := func(err error) {
		if errors.Is(err, ErrRange) {
			httpx.Error(w, http.StatusBadRequest, "validation_failed", "invalid range", map[string]string{"to": err.Error()})
			return
		}
		m.log.Error("analytics: api summary", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
	}
	sum, err := m.store.Summary(ctx, f)
	if err != nil {
		fail(err)
		return
	}
	pts, err := m.store.Series(ctx, f)
	if err != nil {
		fail(err)
		return
	}
	out := SummaryResponse{From: f.From, To: f.To, Summary: summaryJSON(sum), Series: make([]PointJSON, 0, len(pts)), Top: map[string][]RowJSON{}}
	for _, p := range pts {
		out.Series = append(out.Series, PointJSON{At: p.At.UTC(), Visitors: p.Visitors, Views: p.Views})
	}
	for _, t := range apiTops {
		rows, err := m.store.Top(ctx, f, t.dim, topLimit)
		if err != nil {
			fail(err)
			return
		}
		list := make([]RowJSON, 0, len(rows))
		for _, row := range rows {
			list = append(list, RowJSON(row))
		}
		out.Top[t.name] = list
	}
	httpx.JSON(w, http.StatusOK, out, nil)
}

func summaryJSON(s Summary) SummaryJSON {
	out := SummaryJSON{Visitors: s.Visitors, Views: s.Views, Submits: s.Submits, ViewsPerVisitor: s.ViewsPerVisitor}
	if s.Prev != nil {
		p := summaryJSON(*s.Prev)
		out.Prev = &p
	}
	return out
}
