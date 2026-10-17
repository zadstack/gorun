package db

import "time"

// Now returns the current time as an RFC3339 UTC string at second precision.
// Every stored timestamp uses this format so lexical comparison in SQL
// (e.g. run_at <= ?) matches chronological order.
func Now() string {
	return FormatTime(time.Now())
}

// FormatTime formats t like Now.
func FormatTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// ParseTime parses a timestamp produced by Now or FormatTime.
func ParseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}
