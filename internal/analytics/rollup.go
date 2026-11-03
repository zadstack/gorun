package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// daily dims (spec §3). Each row is one (day, dim, key):
//
//   - total: key ""; views, visitors and submits of the day.
//   - page: key is the path; views and visitors of that page's views, and
//     the submits made on that page.
//   - ref, browser, os, device, country: the column's value; views only
//     (a submit carries none of these), submits 0.
//   - utm: key "source/medium/campaign" (each part may be empty, e.g.
//     "//spring" for a campaign without source and medium); views carrying
//     no utm value at all have no utm row. Top(dim="utm") uses the same key.
//   - form: key formKey(form, page); submits only, views and visitors 0
//     (Forms reads the page's views from the page dim).
//
// visitors is COUNT(DISTINCT visitor) over the day's views with a non-empty
// visitor. Summing it across days double counts a returning visitor, so a
// range's visitors are always counted on events (query.go).
const (
	DimTotal   = "total"
	DimPage    = "page"
	DimRef     = "ref"
	DimUTM     = "utm"
	DimBrowser = "browser"
	DimOS      = "os"
	DimDevice  = "device"
	DimCountry = "country"
	DimForm    = "form"
)

// utmKeySQL is the utm dim key of an events row; ” when it has no utm.
const utmKeySQL = `CASE WHEN utm_source = '' AND utm_medium = '' AND utm_campaign = '' THEN '' ELSE utm_source || '/' || utm_medium || '/' || utm_campaign END`

// formKeySQL is formKey in SQL.
const formKeySQL = `replace(form, char(9), '') || char(9) || path`

// dimSQL is how a dim is computed from events: its key expression and the
// condition a row must meet to count in it.
type dimSQL struct {
	key   string
	where string
}

var dimSQLs = map[string]dimSQL{
	DimTotal:   {key: `''`},
	DimPage:    {key: `path`},
	DimRef:     {key: `ref_host`, where: `kind = 'view'`},
	DimUTM:     {key: utmKeySQL, where: `kind = 'view' AND ` + utmKeySQL + ` <> ''`},
	DimBrowser: {key: `browser`, where: `kind = 'view'`},
	DimOS:      {key: `os`, where: `kind = 'view'`},
	DimDevice:  {key: `device`, where: `kind = 'view'`},
	DimCountry: {key: `country`, where: `kind = 'view'`},
	DimForm:    {key: formKeySQL, where: `kind = 'submit'`},
}

// rollupOrder is the order dims are written in (deterministic, for tests
// and diffs).
var rollupOrder = []string{DimTotal, DimPage, DimRef, DimUTM, DimBrowser, DimOS, DimDevice, DimCountry, DimForm}

// formKey is the form dim key: the form slug (tabs removed) and the page
// path, tab separated. cleanPath rejects tabs, so the key splits back
// unambiguously.
func formKey(form, page string) string {
	return strings.ReplaceAll(form, "\t", "") + "\t" + page
}

func splitFormKey(key string) (form, page string) {
	form, page, _ = strings.Cut(key, "\t")
	return form, page
}

// Rollup recomputes daily, with replace semantics, from the earlier of
// yesterday and the newest day already in daily, through today (now's UTC
// day). That is today and yesterday on every regular hourly run, and it
// also fills the days a stopped server missed. When daily is empty but
// events is not (first start, or daily was cleared), it backfills from the
// first event's day. Every recomputed day gets a total row, zero when the
// day had no events, so reads can tell a quiet day from one not rolled up.
// Running it twice gives the same daily.
func (s *Store) Rollup(ctx context.Context, now time.Time) error {
	today := startOfDay(now)
	return s.With(ctx, func(d *sql.DB) error {
		var maxDay, firstTS sql.NullString
		if err := d.QueryRowContext(ctx, `SELECT MAX(day) FROM daily`).Scan(&maxDay); err != nil {
			return fmt.Errorf("analytics: rollup: %w", err)
		}
		if err := d.QueryRowContext(ctx, `SELECT MIN(ts) FROM events`).Scan(&firstTS); err != nil {
			return fmt.Errorf("analytics: rollup: %w", err)
		}
		if !firstTS.Valid {
			return nil // no events: nothing to roll up
		}
		var start time.Time
		if maxDay.Valid {
			last, err := time.Parse(time.DateOnly, maxDay.String)
			if err != nil {
				return fmt.Errorf("analytics: rollup: bad daily day %q: %w", maxDay.String, err)
			}
			start = minTime(last, today.AddDate(0, 0, -1))
		} else {
			first, err := db.ParseTime(firstTS.String)
			if err != nil {
				return fmt.Errorf("analytics: rollup: bad events ts %q: %w", firstTS.String, err)
			}
			start = startOfDay(first)
		}
		if start.After(today) {
			start = today // events dated in the future: still recompute today
		}
		return db.InTx(ctx, d, func(tx *sql.Tx) error { return rollupDays(ctx, tx, start, today) })
	})
}

// rollupDays replaces the daily rows of the days first..last (inclusive).
func rollupDays(ctx context.Context, tx *sql.Tx, first, last time.Time) error {
	end := last.AddDate(0, 0, 1)
	if _, err := tx.ExecContext(ctx, `DELETE FROM daily WHERE day >= ? AND day <= ?`, dayKey(first), dayKey(last)); err != nil {
		return fmt.Errorf("analytics: rollup: clear %s..%s: %w", dayKey(first), dayKey(last), err)
	}
	from, to := db.FormatTime(first), db.FormatTime(end)
	for _, dim := range rollupOrder {
		ds := dimSQLs[dim]
		where := `ts >= ? AND ts < ?`
		if ds.where != "" {
			where += ` AND ` + ds.where
		}
		q := `INSERT INTO daily (day, dim, key, views, visitors, submits)
SELECT substr(ts, 1, 10), '` + dim + `', ` + ds.key + `,
       SUM(kind = 'view'),
       COUNT(DISTINCT CASE WHEN kind = 'view' AND visitor <> '' THEN visitor END),
       SUM(kind = 'submit')
FROM events WHERE ` + where + `
GROUP BY 1, 3`
		if _, err := tx.ExecContext(ctx, q, from, to); err != nil {
			return fmt.Errorf("analytics: rollup %s: %w", dim, err)
		}
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO daily (day, dim, key, views, visitors, submits) VALUES (?, 'total', '', 0, 0, 0)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for day := first; day.Before(end); day = day.AddDate(0, 0, 1) {
		if _, err := stmt.ExecContext(ctx, dayKey(day)); err != nil {
			return fmt.Errorf("analytics: rollup: zero total %s: %w", dayKey(day), err)
		}
	}
	return nil
}

// startOfDay is 00:00 UTC of t's UTC day.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
