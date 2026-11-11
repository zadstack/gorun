package httpx

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// One date/time output for the admin. Every timestamp is shown in UTC as
// "2006-01-02 15:04", left to right, with the full value in the title. The
// admin JavaScript helper (static/datetime.js) produces the same markup.
const (
	dateTimeShort = "2006-01-02 15:04"
	dateTimeFull  = "2006-01-02 15:04:05 UTC"
)

// timestampLayouts are the stored timestamp shapes the admin renders:
// RFC 3339 (with or without fractional seconds) and SQLite's
// CURRENT_TIMESTAMP form.
var timestampLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04"}

// parseTimestamp parses a stored timestamp; ok is false for empty or
// unrecognised values.
func parseTimestamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range timestampLayouts {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

// formatDate is the plain-text short form ("—" when empty, the raw value
// when it cannot be parsed).
func formatDate(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	ts, ok := parseTimestamp(s)
	if !ok {
		return s
	}
	return ts.Format(dateTimeShort)
}

// formatDateTime renders a <time> element: the short UTC form as text, the
// machine value in datetime and the full UTC value in title. An optional
// class is added to the element. Empty values render "—"; unparseable values
// render escaped as plain text.
func formatDateTime(s string, class ...string) template.HTML {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	ts, ok := parseTimestamp(s)
	if !ok {
		return template.HTML(template.HTMLEscapeString(s))
	}
	cls := ""
	if len(class) > 0 && class[0] != "" {
		cls = ` class="` + template.HTMLEscapeString(class[0]) + `"`
	}
	return template.HTML(fmt.Sprintf(`<time%s datetime="%s" dir="ltr" title="%s">%s</time>`,
		cls, ts.Format(time.RFC3339), ts.Format(dateTimeFull), ts.Format(dateTimeShort)))
}

// initials returns up to two uppercase initials for an avatar ("Campaign
// assistant" → "CA", "أمين" → "أ"). It falls back to "?" for names without
// letters.
func initials(name string) string {
	var out []rune
	for _, word := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		for _, r := range word {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

var dataAttrName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// dataAttr returns a bare data-* attribute (for example the script and test
// hook "data-key-create-open") that a page can pass into a partial. Names
// outside [a-z0-9-] produce no attribute.
func dataAttr(name string) template.HTMLAttr {
	if !dataAttrName.MatchString(name) {
		return ""
	}
	return template.HTMLAttr("data-" + name)
}

// dict builds a map from key/value pairs so a page can pass named arguments
// to a partial: {{template "partials/page-header" (dict "Title" "…")}}.
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
		}
		m[k] = kv[i+1]
	}
	return m, nil
}
