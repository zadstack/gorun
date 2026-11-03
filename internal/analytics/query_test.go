package analytics

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func rangeOf(from, to time.Time) Filter { return Filter{From: from, To: to} }

func summary(t *testing.T, s *Store, f Filter) Summary {
	t.Helper()
	sum, err := s.Summary(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestMultiDayVisitorsDistinct(t *testing.T) {
	s := clockStore(t)
	// One salt per day makes the same hash on three days impossible in
	// production; the explicit hashes test the counting rule itself.
	seed(t, s,
		view("2026-10-30T10:00:00Z", "/a", "v1"),
		view("2026-10-31T10:00:00Z", "/a", "v1"),
		view("2026-11-01T10:00:00Z", "/a", "v1"),
		view("2026-11-01T11:00:00Z", "/b", "v2"),
	)
	if err := s.Rollup(context.Background(), testNow); err != nil {
		t.Fatal(err)
	}
	var sumDaily int
	for _, r := range dumpDaily(t, s) {
		if r.Dim == "total" {
			sumDaily += r.Visitors
		}
	}
	if sumDaily != 4 {
		t.Fatalf("daily visitors sum to %d, want 4 (the trap this test guards)", sumDaily)
	}
	f := rangeOf(day(2026, 10, 30), day(2026, 11, 2))
	got := summary(t, s, f)
	if got.Visitors != 2 || got.Views != 4 {
		t.Fatalf("3-day summary = %d visitors %d views, want 2 and 4", got.Visitors, got.Views)
	}
	if got.ViewsPerVisitor != 2 {
		t.Errorf("views per visitor = %v, want 2", got.ViewsPerVisitor)
	}
	rows, err := s.Top(context.Background(), f, "page", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0] != (Row{Key: "/a", Visitors: 1, Views: 3}) || rows[1] != (Row{Key: "/b", Visitors: 1, Views: 1}) {
		t.Errorf("top pages = %+v", rows)
	}
}

// TestVisitorQueryUsesKindTSIndex checks the range visitor count is a
// range scan of the covering index events(kind, ts, visitor), not a table
// scan nor a lookup of every row.
func TestVisitorQueryUsesKindTSIndex(t *testing.T) {
	s := clockStore(t)
	q, args := visitorQuery(rangeOf(day(2026, 10, 1), day(2026, 11, 1)))
	var plan []string
	err := s.With(context.Background(), func(d *sql.DB) error {
		rows, err := d.Query(`EXPLAIN QUERY PLAN `+q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				return err
			}
			plan = append(plan, detail)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if p := strings.Join(plan, "; "); !strings.Contains(p, "COVERING INDEX events_kind_ts_visitor") {
		t.Fatalf("plan does not use the covering index events_kind_ts_visitor: %s", p)
	}
}

func TestSummaryPrevPeriod(t *testing.T) {
	s := clockStore(t)
	seed(t, s,
		// previous period: Oct 28 - Oct 29
		view("2026-10-28T10:00:00Z", "/a", "p1"),
		view("2026-10-29T10:00:00Z", "/a", "p2"),
		view("2026-10-29T11:00:00Z", "/a", "p2"),
		submit("2026-10-29T12:00:00Z", "contact", "/a"),
		// current period: Oct 30 - Oct 31
		view("2026-10-30T10:00:00Z", "/a", "c1"),
		// outside both
		view("2026-10-27T23:59:59Z", "/a", "x"),
		view("2026-11-01T00:00:00Z", "/a", "x"),
	)
	if err := s.Rollup(context.Background(), testNow); err != nil {
		t.Fatal(err)
	}
	got := summary(t, s, rangeOf(day(2026, 10, 30), day(2026, 11, 1)))
	if got.Visitors != 1 || got.Views != 1 || got.Submits != 0 {
		t.Errorf("current = %+v", got)
	}
	if got.Prev == nil {
		t.Fatal("no Prev")
	}
	p := *got.Prev
	if p.Visitors != 2 || p.Views != 3 || p.Submits != 1 || p.ViewsPerVisitor != 1.5 || p.Prev != nil {
		t.Errorf("prev = %+v, want 2 visitors 3 views 1 submit 1.5 per visitor", p)
	}

	// Today's prev is yesterday.
	seed(t, s, view("2026-11-02T10:00:00Z", "/a", "y"), view("2026-11-03T10:00:00Z", "/a", "t"), view("2026-11-03T11:00:00Z", "/a", "t"))
	today := summary(t, s, rangeOf(day(2026, 11, 3), day(2026, 11, 4)))
	if today.Views != 2 || today.Visitors != 1 || today.Prev.Views != 1 {
		t.Errorf("today = %+v prev %+v", today, *today.Prev)
	}

	if _, err := s.Summary(context.Background(), rangeOf(day(2026, 11, 3), day(2026, 11, 3))); err == nil {
		t.Error("an empty range is not an error")
	}
}

func TestSeriesHourlyForSingleDay(t *testing.T) {
	s := clockStore(t)
	seed(t, s,
		view("2026-11-03T00:10:00Z", "/a", "v1"),
		view("2026-11-03T10:05:00Z", "/a", "v1"),
		view("2026-11-03T10:45:00Z", "/b", "v2"),
		view("2026-11-02T10:00:00Z", "/a", "v1"), // yesterday: not in today's series
	)
	pts, err := s.Series(context.Background(), rangeOf(day(2026, 11, 3), day(2026, 11, 4)))
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 24 {
		t.Fatalf("%d points, want 24 hourly", len(pts))
	}
	for i, p := range pts {
		if want := day(2026, 11, 3).Add(time.Duration(i) * time.Hour); !p.At.Equal(want) {
			t.Fatalf("point %d at %v, want %v", i, p.At, want)
		}
	}
	if pts[0] != (Point{At: pts[0].At, Visitors: 1, Views: 1}) || pts[10] != (Point{At: pts[10].At, Visitors: 2, Views: 2}) || pts[5].Views != 0 {
		t.Errorf("points 0, 5, 10 = %+v %+v %+v", pts[0], pts[5], pts[10])
	}

	// A week is daily: seven points, past days from daily, today from events.
	if err := s.Rollup(context.Background(), testNow); err != nil {
		t.Fatal(err)
	}
	pts, err = s.Series(context.Background(), rangeOf(day(2026, 10, 28), day(2026, 11, 4)))
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 7 || !pts[0].At.Equal(day(2026, 10, 28)) {
		t.Fatalf("weekly series = %+v", pts)
	}
	if pts[5].Views != 1 || pts[6].Views != 3 || pts[6].Visitors != 2 || pts[0].Views != 0 {
		t.Errorf("weekly series = %+v", pts)
	}
}

func TestTopFiltered(t *testing.T) {
	s := clockStore(t)
	ref := func(e Event, host string) Event { return with(e, func(e *Event) { e.RefHost = host }) }
	seed(t, s,
		ref(view("2026-11-01T10:00:00Z", "/a", "v1"), "google.com"),
		ref(view("2026-11-01T11:00:00Z", "/a", "v2"), "google.com"),
		ref(view("2026-11-01T12:00:00Z", "/a", "v3"), "bing.com"),
		ref(view("2026-11-01T13:00:00Z", "/b", "v4"), "duck.com"),
		ref(view("2026-11-03T10:00:00Z", "/a", "v5"), "google.com"),
	)
	if err := s.Rollup(context.Background(), testNow); err != nil {
		t.Fatal(err)
	}
	f := rangeOf(day(2026, 10, 28), day(2026, 11, 4))
	all, err := s.Top(context.Background(), f, "ref", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0] != (Row{Key: "google.com", Visitors: 3, Views: 3}) {
		t.Fatalf("unfiltered refs = %+v", all)
	}
	f.Page = "/a"
	got, err := s.Top(context.Background(), f, "ref", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{{Key: "google.com", Visitors: 3, Views: 3}, {Key: "bing.com", Visitors: 1, Views: 1}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("refs for /a = %+v, want %+v", got, want)
	}
	if got, _ := s.Top(context.Background(), f, "ref", 1); len(got) != 1 {
		t.Errorf("limit 1 gave %d rows", len(got))
	}
	f.Page = ""
	f.Ref = "google.com"
	pages, err := s.Top(context.Background(), f, "page", 10)
	if err != nil || len(pages) != 1 || pages[0].Key != "/a" || pages[0].Views != 3 {
		t.Errorf("pages from google = %+v %v", pages, err)
	}
	if _, err := s.Top(context.Background(), f, "visitor", 10); err == nil {
		t.Error("an unknown dim is not an error")
	}
}

func TestTopUTMAndDims(t *testing.T) {
	s := clockStore(t)
	utm := func(e Event, src, med, camp string) Event {
		return with(e, func(e *Event) { e.UTMSource, e.UTMMedium, e.UTMCampaign = src, med, camp })
	}
	seed(t, s,
		utm(view("2026-11-01T10:00:00Z", "/a", "v1"), "nl", "email", "fall"),
		utm(view("2026-11-01T10:00:00Z", "/a", "v2"), "", "", "fall"),
		view("2026-11-01T11:00:00Z", "/a", "v3"),
		utm(view("2026-11-03T10:00:00Z", "/a", "v4"), "nl", "email", "fall"),
		with(view("2026-11-03T10:00:00Z", "/a", "v5"), func(e *Event) { e.Device, e.Country = "mobile", "EG" }),
	)
	if err := s.Rollup(context.Background(), testNow); err != nil {
		t.Fatal(err)
	}
	f := rangeOf(day(2026, 10, 28), day(2026, 11, 4))
	rows, err := s.Top(context.Background(), f, "utm", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{{Key: "nl/email/fall", Visitors: 2, Views: 2}, {Key: "//fall", Visitors: 1, Views: 1}}
	if len(rows) != 2 || rows[0] != want[0] || rows[1] != want[1] {
		t.Errorf("utm rows = %+v, want %+v", rows, want)
	}
	for dim, w := range map[string]Row{"browser": {"Chrome", 5, 5}, "os": {"macOS", 5, 5}, "device": {"desktop", 4, 4}, "country": {"AE", 4, 4}} {
		rows, err := s.Top(context.Background(), f, dim, 10)
		if err != nil || len(rows) == 0 || rows[0] != w {
			t.Errorf("top %s = %+v %v", dim, rows, err)
		}
	}
	f.Device, f.Country = "mobile", "EG"
	if sum := summary(t, s, f); sum.Views != 1 || sum.Visitors != 1 {
		t.Errorf("device+country filter = %+v", sum)
	}
}

func TestFormsConversion(t *testing.T) {
	s := clockStore(t)
	var evs []Event
	for i := range 10 {
		evs = append(evs, view("2026-11-01T10:00:00Z", "/contact-us", string(rune('a'+i))))
	}
	evs = append(evs,
		submit("2026-11-01T10:05:00Z", "contact", "/contact-us"),
		submit("2026-11-01T10:06:00Z", "contact", "/contact-us"),
		view("2026-11-01T10:00:00Z", "/other", "z"),
	)
	seed(t, s, evs...)
	f := rangeOf(day(2026, 10, 28), day(2026, 11, 4))
	check := func(when string) {
		t.Helper()
		rows, err := s.Forms(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		want := FormRow{Form: "contact", Page: "/contact-us", Views: 10, Submits: 2, Rate: 0.2}
		if len(rows) != 1 || rows[0] != want {
			t.Errorf("%s: forms = %+v, want %+v", when, rows, want)
		}
	}
	check("before rollup (events)")
	if err := s.Rollup(context.Background(), testNow); err != nil {
		t.Fatal(err)
	}
	check("after rollup (daily)")
	if sum := summary(t, s, f); sum.Submits != 2 {
		t.Errorf("summary submits = %d, want 2", sum.Submits)
	}
}

func TestNowFiveMinutes(t *testing.T) {
	s := clockStore(t)
	seed(t, s,
		view("2026-11-03T14:54:59Z", "/a", "old"),
		view("2026-11-03T14:55:00Z", "/a", "v1"),
		view("2026-11-03T14:58:00Z", "/a", "v1"),
		view("2026-11-03T14:59:59Z", "/b", "v2"),
		view("2026-11-03T14:59:00Z", "/b", ""),
		submit("2026-11-03T14:59:00Z", "contact", "/b"),
	)
	n, err := s.Now(context.Background(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("now = %d, want 2", n)
	}
}

func TestTodayReadsEventsPastReadsDaily(t *testing.T) {
	s := clockStore(t)
	ctx := context.Background()
	seed(t, s,
		view("2026-10-30T10:00:00Z", "/a", "v1"),
		view("2026-11-03T09:00:00Z", "/a", "v2"),
		view("2026-11-03T10:00:00Z", "/b", "v3"),
	)
	if err := s.Rollup(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	exec(t, s, `DELETE FROM daily WHERE day = '2026-11-03'`)
	today := rangeOf(day(2026, 11, 3), day(2026, 11, 4))
	if got := summary(t, s, today); got.Views != 2 || got.Visitors != 2 {
		t.Errorf("today without daily rows = %+v, want 2 views 2 visitors", got)
	}
	if rows, err := s.Top(ctx, today, "page", 10); err != nil || len(rows) != 2 {
		t.Errorf("today top pages = %+v %v", rows, err)
	}

	// A settled past day's views come from daily: a tampered row shows.
	exec(t, s, `UPDATE daily SET views = 7 WHERE day = '2026-10-30' AND dim IN ('total', 'page')`)
	week := rangeOf(day(2026, 10, 28), day(2026, 11, 4))
	if got := summary(t, s, week); got.Views != 9 || got.Visitors != 3 {
		t.Errorf("week = %+v, want 7 from daily + 2 from events, 3 visitors", got)
	}
	if rows, _ := s.Top(ctx, week, "page", 10); len(rows) == 0 || rows[0] != (Row{Key: "/a", Visitors: 2, Views: 8}) {
		t.Errorf("week top pages = %+v", rows)
	}

	// With a filter, the whole range reads events.
	week.Page = "/a"
	if got := summary(t, s, week); got.Views != 2 {
		t.Errorf("filtered week = %+v, want 2 views from events", got)
	}

	// A past day missing from daily (not rolled up yet) falls back to events.
	exec(t, s, `DELETE FROM daily`)
	week.Page = ""
	if got := summary(t, s, week); got.Views != 3 || got.Visitors != 3 {
		t.Errorf("week without daily = %+v, want 3 views 3 visitors", got)
	}
}

// TestPartialDaysReadEvents: a range that starts or ends inside a day
// reads that day from events, so the bounds are honoured exactly.
func TestPartialDaysReadEvents(t *testing.T) {
	s := clockStore(t)
	seed(t, s,
		view("2026-10-30T08:00:00Z", "/a", "v1"),
		view("2026-10-30T20:00:00Z", "/a", "v2"),
		view("2026-10-31T12:00:00Z", "/a", "v3"),
		view("2026-11-01T08:00:00Z", "/a", "v4"),
	)
	if err := s.Rollup(context.Background(), testNow); err != nil {
		t.Fatal(err)
	}
	f := rangeOf(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC))
	if got := summary(t, s, f); got.Views != 2 || got.Visitors != 2 {
		t.Errorf("partial range = %+v, want 2 views", got)
	}
}
