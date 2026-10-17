package admin

import (
	"testing"
	"time"
)

func TestMetricoolRejectsUnsafeOrUnsupportedInput(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	good := metricoolInput{Text: "Reviewed", Network: "linkedin", DateTime: "2026-10-13T12:00:00", Timezone: "Asia/Dubai"}
	if !validMetricoolInput(good, now) {
		t.Fatal("valid draft rejected")
	}
	cases := []metricoolInput{good, good, good, good, good}
	cases[0].Text = " "
	cases[1].Network = "youtube"
	cases[2].DateTime = "2026-10-11T12:00:00"
	cases[3].Timezone = "../../etc/passwd"
	cases[4].DateTime = "2030-01-01T00:00:00"
	for _, in := range cases {
		if validMetricoolInput(in, now) {
			t.Fatal("invalid draft accepted")
		}
	}
}
