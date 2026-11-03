package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// testNow is the fixed "now" of the rollup and query tests: Tuesday
// 2026-11-03 15:00 UTC.
var testNow = time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)

// clockStore is a test store whose clock is testNow.
func clockStore(t *testing.T) *Store {
	t.Helper()
	s, _ := openTestStore(t)
	s.now = func() time.Time { return testNow }
	return s
}

// view is a desktop Chrome page view at ts (RFC3339).
func view(ts, path, visitor string) Event {
	at, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		panic(err)
	}
	return Event{TS: at, Kind: KindView, Path: path, Visitor: visitor, RefHost: "direct",
		Browser: "Chrome", OS: "macOS", Device: "desktop", Country: "AE", Locale: "en"}
}

func submit(ts, form, path string) Event {
	at, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		panic(err)
	}
	return Event{TS: at, Kind: KindSubmit, Path: path, Form: form}
}

func with(e Event, f func(*Event)) Event { f(&e); return e }

func seed(t *testing.T, s *Store, evs ...Event) {
	t.Helper()
	if err := s.Insert(context.Background(), evs); err != nil {
		t.Fatal(err)
	}
}

type dailyRow struct {
	Day, Dim, Key            string
	Views, Visitors, Submits int
}

func dumpDaily(t *testing.T, s *Store) []dailyRow {
	t.Helper()
	var out []dailyRow
	err := s.With(context.Background(), func(d *sql.DB) error {
		rows, err := d.Query(`SELECT day, dim, key, views, visitors, submits FROM daily ORDER BY day, dim, key`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r dailyRow
			if err := rows.Scan(&r.Day, &r.Dim, &r.Key, &r.Views, &r.Visitors, &r.Submits); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func dailyOf(rows []dailyRow, day, dim, key string) (dailyRow, bool) {
	for _, r := range rows {
		if r.Day == day && r.Dim == dim && r.Key == key {
			return r, true
		}
	}
	return dailyRow{}, false
}

func exec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if err := s.With(context.Background(), func(d *sql.DB) error {
		_, err := d.Exec(q, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRollupIdempotent(t *testing.T) {
	s := clockStore(t)
	ctx := context.Background()
	seed(t, s,
		view("2026-11-02T09:00:00Z", "/a", "v1"),
		view("2026-11-02T10:00:00Z", "/a", "v1"),
		with(view("2026-11-02T11:00:00Z", "/b", "v2"), func(e *Event) {
			e.RefHost, e.UTMSource, e.UTMMedium, e.UTMCampaign = "news.example", "nl", "email", "fall"
		}),
		view("2026-11-02T12:00:00Z", "/b", ""), // no visitor: a view, not a visitor
		submit("2026-11-02T12:30:00Z", "contact", "/b"),
		view("2026-11-03T08:00:00Z", "/a", "v3"),
	)
	if err := s.Rollup(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	first := dumpDaily(t, s)
	if err := s.Rollup(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	if second := dumpDaily(t, s); !reflect.DeepEqual(first, second) {
		t.Fatalf("second rollup changed daily:\n%v\n%v", first, second)
	}

	want := []dailyRow{
		{"2026-11-02", "total", "", 4, 2, 1},
		{"2026-11-02", "page", "/a", 2, 1, 0},
		{"2026-11-02", "page", "/b", 2, 1, 1},
		{"2026-11-02", "ref", "direct", 3, 1, 0},
		{"2026-11-02", "ref", "news.example", 1, 1, 0},
		{"2026-11-02", "utm", "nl/email/fall", 1, 1, 0},
		{"2026-11-02", "browser", "Chrome", 4, 2, 0},
		{"2026-11-02", "os", "macOS", 4, 2, 0},
		{"2026-11-02", "device", "desktop", 4, 2, 0},
		{"2026-11-02", "country", "AE", 4, 2, 0},
		{"2026-11-02", "form", formKey("contact", "/b"), 0, 0, 1},
		{"2026-11-03", "total", "", 1, 1, 0},
	}
	for _, w := range want {
		got, ok := dailyOf(first, w.Day, w.Dim, w.Key)
		if !ok || got != w {
			t.Errorf("daily %s/%s/%q = %+v (found %v), want %+v", w.Day, w.Dim, w.Key, got, ok, w)
		}
	}
	if _, ok := dailyOf(first, "2026-11-02", "utm", ""); ok {
		t.Error("views without utm got a utm row")
	}

	// Replace semantics: a later event (and a stale row) for yesterday are
	// reflected exactly by the next run.
	exec(t, s, `UPDATE daily SET views = 999 WHERE day = '2026-11-02' AND dim = 'page' AND key = '/a'`)
	exec(t, s, `INSERT INTO daily VALUES ('2026-11-02', 'page', '/gone', 5, 5, 0)`)
	seed(t, s, view("2026-11-02T23:00:00Z", "/a", "v1"))
	if err := s.Rollup(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	after := dumpDaily(t, s)
	if r, _ := dailyOf(after, "2026-11-02", "page", "/a"); r.Views != 3 || r.Visitors != 1 {
		t.Errorf("after rerun /a = %+v, want 3 views 1 visitor", r)
	}
	if _, ok := dailyOf(after, "2026-11-02", "page", "/gone"); ok {
		t.Error("stale row survived the rerun")
	}
}

func TestRollupBackfillsWhenEmpty(t *testing.T) {
	s := clockStore(t)
	ctx := context.Background()
	seed(t, s,
		view("2026-10-20T09:00:00Z", "/old", "v1"),
		view("2026-10-25T09:00:00Z", "/old", "v2"),
		view("2026-11-03T09:00:00Z", "/new", "v3"),
	)
	if err := s.Rollup(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	rows := dumpDaily(t, s)
	for _, day := range []string{"2026-10-20", "2026-10-25", "2026-11-03"} {
		if r, ok := dailyOf(rows, day, "total", ""); !ok || r.Views != 1 {
			t.Errorf("total %s = %+v (found %v), want 1 view", day, r, ok)
		}
	}
	// Every day from the first event to today has a total row, even an empty
	// one, so reads can tell "no traffic" from "not rolled up".
	for d := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC); !d.After(testNow); d = d.AddDate(0, 0, 1) {
		if _, ok := dailyOf(rows, dayKey(d), "total", ""); !ok {
			t.Errorf("no total row for %s", dayKey(d))
		}
	}

	// A daily left with a gap (the server was down for days) is filled from
	// its newest day onward, not only today and yesterday.
	exec(t, s, `DELETE FROM daily WHERE day >= '2026-10-24'`)
	if err := s.Rollup(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	if r, ok := dailyOf(dumpDaily(t, s), "2026-10-25", "total", ""); !ok || r.Views != 1 {
		t.Errorf("gap not filled: %+v (found %v)", r, ok)
	}

	// Empty events: nothing to do, and no error.
	empty := clockStore(t)
	if err := empty.Rollup(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	if n := len(dumpDaily(t, empty)); n != 0 {
		t.Errorf("empty store got %d daily rows", n)
	}
}

// TestModuleRollupCron runs the registered cron body.
func TestModuleRollupCron(t *testing.T) {
	e := bootCollect(t, nil, nil)
	now := time.Now().UTC()
	seed(t, e.mod.store, view(now.Add(-time.Minute).Format(time.RFC3339), "/x", "v1"))
	if err := e.mod.rollup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r, ok := dailyOf(dumpDaily(t, e.mod.store), dayKey(now.Add(-time.Minute)), "total", ""); !ok || r.Views != 1 {
		t.Fatalf("cron rollup total = %+v (found %v)", r, ok)
	}
}

func TestFormKey(t *testing.T) {
	for _, c := range [][2]string{{"contact", "/contact-us"}, {"quote", ""}, {"a", "/b/c"}} {
		f, p := splitFormKey(formKey(c[0], c[1]))
		if f != c[0] || p != c[1] {
			t.Errorf("round trip %q -> %q %q", c, f, p)
		}
	}
	if k := formKey("con\ttact", "/x"); k != formKey("contact", "/x") {
		t.Errorf("tab not stripped from the form: %q", fmt.Sprint(k))
	}
}
