package wpmigrate

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Re-review 1: the wizard's "Requests per second" accepts blank (default)
// or a finite 1–20; anything else is a translated field error and creates
// nothing.
func TestWizardRateField(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	b := e.browser()
	connect := func(rate string) (*http.Response, string) {
		return b.post("/admin/migrations/connect", url.Values{"url": {e.wp.URL()}, "slug_policy": {"suffix"}, "rate_per_second": {rate}})
	}
	storedRate := func(loc string) float64 {
		t.Helper()
		m := migID.FindStringSubmatch(loc)
		if m == nil {
			t.Fatalf("redirect = %q", loc)
		}
		row, err := e.svc.getMigration(context.Background(), m[1])
		if err != nil {
			t.Fatal(err)
		}
		return row.Options.RatePerSecond
	}
	for _, bad := range []string{"NaN", "Inf", "50", "0", "0.5", "abc"} {
		before := e.count(`SELECT count(*) FROM wp_sources`)
		resp, page := connect(bad)
		if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, `<div id="mig-rate-err"><p class="field-error`) || !strings.Contains(page, `aria-invalid="true"`) {
			t.Fatalf("%q: %d\n%s", bad, resp.StatusCode, page)
		}
		if e.count(`SELECT count(*) FROM wp_sources`) != before {
			t.Fatalf("%q: a source was created", bad)
		}
	}
	resp, _ := connect("")
	if resp.StatusCode != http.StatusSeeOther || storedRate(resp.Header.Get("Location")) != 0 {
		t.Fatalf("blank must store unset (setting/default): %d", resp.StatusCode)
	}
	resp, _ = connect("7")
	if resp.StatusCode != http.StatusSeeOther || storedRate(resp.Header.Get("Location")) != 7 {
		t.Fatalf("7 must be stored: %d", resp.StatusCode)
	}
	_, page := b.do("GET", "/admin/migrations/new", nil, "")
	if !strings.Contains(page, `name="rate_per_second" type="number" min="1" max="20" step="1"`) {
		t.Fatal("rate input must be min=1 max=20 step=1")
	}
}

// normalized() clamps to [1,20]; NaN, ±Inf, 0 and negatives mean unset
// (the setting, else 4/s).
func TestNormalizeRate(t *testing.T) {
	for in, want := range map[float64]float64{0: 0, -3: 0, math.NaN(): 0, math.Inf(1): 0, math.Inf(-1): 0, 0.4: 1, 7: 7, 50: 20} {
		if got := (Options{RatePerSecond: in}).normalized().RatePerSecond; got != want {
			t.Errorf("normalized(%v) = %v, want %v", in, got, want)
		}
	}
}

// A Discover (or any limiter lookup) never retunes a source's running
// bucket; only starting a run does.
func TestLimiterRetunedOnlyByStart(t *testing.T) {
	e := newEnv(t)
	s := e.svc
	l := s.limiter("src-x", 2)
	if l.every.Seconds() != 0.5 {
		t.Fatalf("seed rate: %v", l.every)
	}
	s.limiter("src-x", 9) // e.g. client() during Discover with the setting's rate
	if l.every.Seconds() != 0.5 {
		t.Fatalf("lookup retuned the bucket: %v", l.every)
	}
	s.tuneLimiter("src-x", 4)
	if l.every.Seconds() != 0.25 {
		t.Fatalf("tune: %v", l.every)
	}
}
