package analytics

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// Filter selects the events a query counts: those with From <= ts < To
// (UTC; day boundaries are UTC midnights), narrowed by the non-empty
// fields. Page is a path, Ref a referrer host ("direct" for none), Country
// an ISO code ("--" unknown), Device mobile, tablet, desktop or unknown.
//
// Submit events carry only a page, so with Ref, Country or Device set they
// all drop out: submissions (Summary.Submits, Forms) are 0 under those
// filters.
type Filter struct {
	From, To                   time.Time
	Page, Ref, Country, Device string
}

// filtered reports whether any dimension filter is set. A filtered query
// reads events for its whole range: daily holds one dim per row, so it
// cannot answer "views of /a from google.com".
func (f Filter) filtered() bool {
	return f.Page != "" || f.Ref != "" || f.Country != "" || f.Device != ""
}

// Summary is a period's headline numbers. Prev is the same numbers for the
// previous period of equal length, ending where this one starts (nil on
// Prev itself).
type Summary struct {
	Visitors, Views, Submits int
	ViewsPerVisitor          float64
	Prev                     *Summary
}

// Point is one bucket of Series, starting At (UTC).
type Point struct {
	At              time.Time
	Visitors, Views int
}

// Row is one key of Top.
type Row struct {
	Key             string
	Visitors, Views int
}

// FormRow is one form on one page: the page's views, the form's
// submissions there and Submits/Views (0 without views).
type FormRow struct {
	Form, Page     string
	Views, Submits int
	Rate           float64
}

// ErrRange is returned for a Filter whose From is not before its To.
var ErrRange = errors.New("analytics: empty or invalid range")

// ErrDim is returned by Top for a dim it does not know.
var ErrDim = errors.New("analytics: unknown dim")

// maxSeriesDays bounds a daily Series (about ten years of points).
const maxSeriesDays = 3660

// nowViewers is the window Now counts.
const nowViewers = 5 * time.Minute

// topDims are the dims Top accepts.
var topDims = []string{DimPage, DimRef, DimUTM, DimBrowser, DimOS, DimDevice, DimCountry}

// How reads split a range (spec §3, "Reads"):
//
//   - Whole UTC days before yesterday ("settled" days) read their views and
//     submits from daily. Yesterday is not settled: the hourly rollup can
//     leave its last hour out until the first run after midnight, so it is
//     read from events, like today and any partial day at either end.
//   - The daily part is used only when every settled day has its total row
//     (Rollup writes one per day, even a quiet one) and no dimension filter
//     is set; otherwise the whole range reads events.
//   - Visitors are never summed from daily: a range's (or a key's) visitors
//     are COUNT(DISTINCT visitor) over events for the whole range, on the
//     events(kind, ts) index.

// plan is how one range is read: daily for the settled days [dayFrom,
// dayTo) when useDaily, events for segs.
type plan struct {
	useDaily       bool
	dayFrom, dayTo string // day keys, half open
	segs           [][2]string
}

func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (f Filter) normalized() (Filter, error) {
	if f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) {
		return f, ErrRange
	}
	f.From, f.To = f.From.UTC(), f.To.UTC()
	return f, nil
}

// planFor splits f's range. It needs the handle to check daily covers the
// settled days.
func (s *Store) planFor(ctx context.Context, d *sql.DB, f Filter) (plan, error) {
	whole := plan{segs: [][2]string{{db.FormatTime(f.From), db.FormatTime(f.To)}}}
	if f.filtered() {
		return whole, nil
	}
	first := startOfDay(f.From)
	if first.Before(f.From) {
		first = first.AddDate(0, 0, 1) // From is inside a day: that day is partial
	}
	last := minTime(startOfDay(f.To), startOfDay(s.clock()).AddDate(0, 0, -1)) // exclusive
	if !first.Before(last) {
		return whole, nil
	}
	// Days before daily's first day were never rolled up (the backfill
	// starts at the first event): they read events, which normally hold
	// nothing there.
	var minDay sql.NullString
	if err := d.QueryRowContext(ctx, `SELECT MIN(day) FROM daily WHERE dim = 'total'`).Scan(&minDay); err != nil {
		return plan{}, err
	}
	if !minDay.Valid {
		return whole, nil
	}
	if md, err := time.Parse(time.DateOnly, minDay.String); err == nil && md.After(first) {
		first = md
	}
	if !first.Before(last) {
		return whole, nil
	}
	days := int(last.Sub(first) / (24 * time.Hour))
	var have int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM daily WHERE dim = 'total' AND key = '' AND day >= ? AND day < ?`,
		dayKey(first), dayKey(last)).Scan(&have); err != nil {
		return plan{}, err
	}
	if have != days {
		return whole, nil // not rolled up (yet): events are the truth
	}
	p := plan{useDaily: true, dayFrom: dayKey(first), dayTo: dayKey(last)}
	if f.From.Before(first) {
		p.segs = append(p.segs, [2]string{db.FormatTime(f.From), db.FormatTime(first)})
	}
	if last.Before(f.To) {
		p.segs = append(p.segs, [2]string{db.FormatTime(last), db.FormatTime(f.To)})
	}
	return p, nil
}

// filterSQL is the dimension filters as "AND ..." conditions.
func filterSQL(f Filter) (string, []any) {
	var b strings.Builder
	var args []any
	for _, c := range []struct{ col, v string }{{"path", f.Page}, {"ref_host", f.Ref}, {"country", f.Country}, {"device", f.Device}} {
		if c.v != "" {
			b.WriteString(` AND ` + c.col + ` = ?`)
			args = append(args, c.v)
		}
	}
	return b.String(), args
}

// segSQL is "(ts in seg 1 OR ts in seg 2 ...)"; ok is false when the plan
// reads nothing from events.
func segSQL(segs [][2]string) (string, []any, bool) {
	if len(segs) == 0 {
		return "", nil, false
	}
	parts := make([]string, len(segs))
	var args []any
	for i, sg := range segs {
		parts[i] = `(ts >= ? AND ts < ?)`
		args = append(args, sg[0], sg[1])
	}
	return `(` + strings.Join(parts, ` OR `) + `)`, args, true
}

// visitorQuery counts the distinct visitors of f's whole range. With kind
// and ts leading the WHERE, SQLite answers it with a range scan of
// events_kind_ts (TestVisitorQueryUsesKindTSIndex checks the plan).
func visitorQuery(f Filter) (string, []any) {
	fs, fargs := filterSQL(f)
	q := `SELECT COUNT(DISTINCT visitor) FROM events WHERE kind = 'view' AND ts >= ? AND ts < ? AND visitor <> ''` + fs
	return q, append([]any{db.FormatTime(f.From), db.FormatTime(f.To)}, fargs...)
}

// Summary returns f's visitors, views, submissions and views per visitor,
// with Prev for the equal-length period before it.
func (s *Store) Summary(ctx context.Context, f Filter) (Summary, error) {
	f, err := f.normalized()
	if err != nil {
		return Summary{}, err
	}
	cur, err := s.summary(ctx, f)
	if err != nil {
		return Summary{}, err
	}
	pf := f
	pf.From, pf.To = f.From.Add(-f.To.Sub(f.From)), f.From
	prev, err := s.summary(ctx, pf)
	if err != nil {
		return Summary{}, err
	}
	cur.Prev = &prev
	return cur, nil
}

func (s *Store) summary(ctx context.Context, f Filter) (Summary, error) {
	var out Summary
	err := s.With(ctx, func(d *sql.DB) error {
		p, err := s.planFor(ctx, d, f)
		if err != nil {
			return err
		}
		if p.useDaily {
			var v, sub int
			if err := d.QueryRowContext(ctx, `SELECT COALESCE(SUM(views), 0), COALESCE(SUM(submits), 0) FROM daily
				WHERE dim = 'total' AND key = '' AND day >= ? AND day < ?`, p.dayFrom, p.dayTo).Scan(&v, &sub); err != nil {
				return err
			}
			out.Views, out.Submits = v, sub
		}
		if seg, sargs, ok := segSQL(p.segs); ok {
			fs, fargs := filterSQL(f)
			var v, sub int
			if err := d.QueryRowContext(ctx, `SELECT COALESCE(SUM(kind = 'view'), 0), COALESCE(SUM(kind = 'submit'), 0) FROM events WHERE `+seg+fs,
				slices.Concat(sargs, fargs)...).Scan(&v, &sub); err != nil {
				return err
			}
			out.Views += v
			out.Submits += sub
		}
		q, args := visitorQuery(f)
		return d.QueryRowContext(ctx, q, args...).Scan(&out.Visitors)
	})
	if err != nil {
		return Summary{}, fmt.Errorf("analytics: summary: %w", err)
	}
	if out.Visitors > 0 {
		out.ViewsPerVisitor = float64(out.Views) / float64(out.Visitors)
	}
	return out, nil
}

// Series returns visitors and views per hour when f spans at most one day,
// else per UTC day, with a zero point for every empty bucket. A day point's
// visitors are that day's distinct visitors.
func (s *Store) Series(ctx context.Context, f Filter) ([]Point, error) {
	f, err := f.normalized()
	if err != nil {
		return nil, err
	}
	if f.To.Sub(f.From) <= 24*time.Hour {
		return s.hourly(ctx, f)
	}
	return s.daily(ctx, f)
}

func (s *Store) hourly(ctx context.Context, f Filter) ([]Point, error) {
	var pts []Point
	idx := map[string]int{}
	for at := f.From.Truncate(time.Hour); at.Before(f.To); at = at.Add(time.Hour) {
		idx[db.FormatTime(at)[:13]] = len(pts)
		pts = append(pts, Point{At: at})
	}
	err := s.With(ctx, func(d *sql.DB) error {
		fs, fargs := filterSQL(f)
		return scanBuckets(ctx, d, pts, idx, `SELECT substr(ts, 1, 13), COUNT(DISTINCT CASE WHEN visitor <> '' THEN visitor END), COUNT(*)
			FROM events WHERE kind = 'view' AND ts >= ? AND ts < ?`+fs+` GROUP BY 1`,
			append([]any{db.FormatTime(f.From), db.FormatTime(f.To)}, fargs...)...)
	})
	if err != nil {
		return nil, fmt.Errorf("analytics: series: %w", err)
	}
	return pts, nil
}

func (s *Store) daily(ctx context.Context, f Filter) ([]Point, error) {
	var pts []Point
	idx := map[string]int{}
	for at := startOfDay(f.From); at.Before(f.To); at = at.AddDate(0, 0, 1) {
		if len(pts) == maxSeriesDays {
			return nil, fmt.Errorf("%w: more than %d days", ErrRange, maxSeriesDays)
		}
		idx[dayKey(at)] = len(pts)
		pts = append(pts, Point{At: at})
	}
	err := s.With(ctx, func(d *sql.DB) error {
		p, err := s.planFor(ctx, d, f)
		if err != nil {
			return err
		}
		if p.useDaily {
			if err := scanBuckets(ctx, d, pts, idx, `SELECT day, visitors, views FROM daily
				WHERE dim = 'total' AND key = '' AND day >= ? AND day < ?`, p.dayFrom, p.dayTo); err != nil {
				return err
			}
		}
		if seg, sargs, ok := segSQL(p.segs); ok {
			fs, fargs := filterSQL(f)
			// Segments split on day boundaries, so each day is counted in
			// one place.
			return scanBuckets(ctx, d, pts, idx, `SELECT substr(ts, 1, 10), COUNT(DISTINCT CASE WHEN visitor <> '' THEN visitor END), COUNT(*)
				FROM events WHERE kind = 'view' AND `+seg+fs+` GROUP BY 1`, slices.Concat(sargs, fargs)...)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("analytics: series: %w", err)
	}
	return pts, nil
}

// scanBuckets adds (bucket, visitors, views) rows into pts.
func scanBuckets(ctx context.Context, d *sql.DB, pts []Point, idx map[string]int, q string, args ...any) error {
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var vis, views int
		if err := rows.Scan(&key, &vis, &views); err != nil {
			return err
		}
		if i, ok := idx[key]; ok {
			pts[i].Visitors += vis
			pts[i].Views += views
		}
	}
	return rows.Err()
}

// Top returns f's keys of dim (page, ref, utm, browser, os, device or
// country) by visitors, then views, then key; limit <= 0 returns all. A
// key's views come from daily for settled days; its visitors are counted
// distinct on events over the whole range. The utm key is
// "source/medium/campaign", as in daily, and views without utm are left
// out.
func (s *Store) Top(ctx context.Context, f Filter, dim string, limit int) ([]Row, error) {
	if !slices.Contains(topDims, dim) {
		return nil, fmt.Errorf("%w %q", ErrDim, dim)
	}
	f, err := f.normalized()
	if err != nil {
		return nil, err
	}
	ds := dimSQLs[dim]
	cond := ` AND kind = 'view'`
	if dim == DimUTM {
		cond += ` AND ` + utmKeySQL + ` <> ''`
	}
	byKey := map[string]*Row{}
	add := func(key string, vis, views int) {
		r := byKey[key]
		if r == nil {
			r = &Row{Key: key}
			byKey[key] = r
		}
		r.Visitors += vis
		r.Views += views
	}
	err = s.With(ctx, func(d *sql.DB) error {
		p, err := s.planFor(ctx, d, f)
		if err != nil {
			return err
		}
		fs, fargs := filterSQL(f)
		if p.useDaily {
			if err := scanKeys(ctx, d, add, `SELECT key, 0, SUM(views) FROM daily WHERE dim = ? AND day >= ? AND day < ? GROUP BY key`,
				dim, p.dayFrom, p.dayTo); err != nil {
				return err
			}
		}
		if seg, sargs, ok := segSQL(p.segs); ok {
			if err := scanKeys(ctx, d, add, `SELECT `+ds.key+`, 0, COUNT(*) FROM events WHERE `+seg+cond+fs+` GROUP BY 1`,
				slices.Concat(sargs, fargs)...); err != nil {
				return err
			}
		}
		return scanKeys(ctx, d, add, `SELECT `+ds.key+`, COUNT(DISTINCT visitor), 0 FROM events
			WHERE kind = 'view' AND ts >= ? AND ts < ? AND visitor <> ''`+cond+fs+` GROUP BY 1`,
			append([]any{db.FormatTime(f.From), db.FormatTime(f.To)}, fargs...)...)
	})
	if err != nil {
		return nil, fmt.Errorf("analytics: top %s: %w", dim, err)
	}
	out := make([]Row, 0, len(byKey))
	for _, r := range byKey {
		if r.Views > 0 || r.Visitors > 0 {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b Row) int {
		return cmp.Or(cmp.Compare(b.Visitors, a.Visitors), cmp.Compare(b.Views, a.Views), strings.Compare(a.Key, b.Key))
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func scanKeys(ctx context.Context, d *sql.DB, add func(string, int, int), q string, args ...any) error {
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var vis, views int
		if err := rows.Scan(&key, &vis, &views); err != nil {
			return err
		}
		add(key, vis, views)
	}
	return rows.Err()
}

// Forms returns, for every form submitted in f's range, per page it was
// submitted on: that page's views, the submissions and the conversion
// rate, most submissions first. A form with no submission in the range is
// not listed (the module does not know the site's forms).
func (s *Store) Forms(ctx context.Context, f Filter) ([]FormRow, error) {
	f, err := f.normalized()
	if err != nil {
		return nil, err
	}
	subs := map[string]int{}  // formKey -> submits
	views := map[string]int{} // page -> views
	err = s.With(ctx, func(d *sql.DB) error {
		p, err := s.planFor(ctx, d, f)
		if err != nil {
			return err
		}
		fs, fargs := filterSQL(f)
		addSub := func(key string, _, n int) { subs[key] += n }
		if p.useDaily {
			if err := scanKeys(ctx, d, addSub, `SELECT key, 0, SUM(submits) FROM daily WHERE dim = 'form' AND day >= ? AND day < ? GROUP BY key`,
				p.dayFrom, p.dayTo); err != nil {
				return err
			}
		}
		seg, sargs, hasSeg := segSQL(p.segs)
		if hasSeg {
			if err := scanKeys(ctx, d, addSub, `SELECT `+formKeySQL+`, 0, COUNT(*) FROM events WHERE kind = 'submit' AND `+seg+fs+` GROUP BY 1`,
				slices.Concat(sargs, fargs)...); err != nil {
				return err
			}
		}
		pages := map[string]bool{}
		for k := range subs {
			_, page := splitFormKey(k)
			pages[page] = true
		}
		if len(pages) == 0 {
			return nil
		}
		in := strings.TrimSuffix(strings.Repeat("?, ", len(pages)), ", ")
		var pargs []any
		for pg := range pages {
			pargs = append(pargs, pg)
		}
		addView := func(page string, _, n int) { views[page] += n }
		if p.useDaily {
			if err := scanKeys(ctx, d, addView, `SELECT key, 0, SUM(views) FROM daily WHERE dim = 'page' AND day >= ? AND day < ? AND key IN (`+in+`) GROUP BY key`,
				append([]any{p.dayFrom, p.dayTo}, pargs...)...); err != nil {
				return err
			}
		}
		if hasSeg {
			args := slices.Concat(sargs, fargs, pargs)
			return scanKeys(ctx, d, addView, `SELECT path, 0, COUNT(*) FROM events WHERE kind = 'view' AND `+seg+fs+` AND path IN (`+in+`) GROUP BY path`, args...)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("analytics: forms: %w", err)
	}
	var out []FormRow
	for k, n := range subs {
		if n == 0 {
			continue
		}
		form, page := splitFormKey(k)
		r := FormRow{Form: form, Page: page, Views: views[page], Submits: n}
		if r.Views > 0 {
			r.Rate = float64(r.Submits) / float64(r.Views)
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b FormRow) int {
		return cmp.Or(cmp.Compare(b.Submits, a.Submits), strings.Compare(a.Form, b.Form), strings.Compare(a.Page, b.Page))
	})
	return out, nil
}

// Now returns the distinct visitors with a view in the 5 minutes up to at
// (inclusive at both ends, at second precision).
func (s *Store) Now(ctx context.Context, at time.Time) (int, error) {
	var n int
	err := s.With(ctx, func(d *sql.DB) error {
		return d.QueryRowContext(ctx, `SELECT COUNT(DISTINCT visitor) FROM events WHERE kind = 'view' AND ts >= ? AND ts <= ? AND visitor <> ''`,
			db.FormatTime(at.Add(-nowViewers)), db.FormatTime(at)).Scan(&n)
	})
	if err != nil {
		return 0, fmt.Errorf("analytics: now: %w", err)
	}
	return n, nil
}
