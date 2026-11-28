package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/zadstack/gorun/internal/settings"
)

// The Metricool web tracker: the chat module prints it on the same pages
// as the widget, from the tracker_hash of Settings → Metricool (the
// metricool.config setting, read by name: the admin and services layers
// write it). The hash is public: it is in every page.
const (
	SettingMetricool = "metricool.config"
	// MetricoolTrackerOrigin is the only host the tracker loads from and
	// reports to; it is added to script-src and connect-src.
	MetricoolTrackerOrigin = "https://tracker.metricool.com"
	metricoolTrackerScript = MetricoolTrackerOrigin + "/resources/be.js"
)

var trackerHashRe = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)

// ValidTrackerHash reports a tracker hash of 8 to 64 ASCII letters and
// digits (the admin and services layers check the same rule).
func ValidTrackerHash(h string) bool { return trackerHashRe.MatchString(h) }

// LoadTracker reads the Metricool tracker hash; ok is false when none is
// set or it is not valid (a value written by hand is checked again here).
func LoadTracker(ctx context.Context, s *settings.Store) (hash string, ok bool) {
	var c struct {
		TrackerHash string `json:"tracker_hash"`
	}
	if err := s.Get(ctx, SettingMetricool, &c); err != nil {
		return "", false
	}
	return c.TrackerHash, ValidTrackerHash(c.TrackerHash)
}

// trackerTag is the nonced inline script that loads Metricool's be.js and
// starts it with hash, JSON-encoded (never concatenated raw).
func trackerTag(hash, nonce string) []byte {
	h, _ := json.Marshal(hash)
	src, _ := json.Marshal(metricoolTrackerScript)
	var b bytes.Buffer
	b.WriteString(`<script`)
	if nonce != "" {
		b.WriteString(` nonce="` + nonce + `"`)
	}
	b.WriteString(`>(function(d){var s=d.createElement("script");s.src=`)
	b.Write(src)
	b.WriteString(`;s.async=true;s.onload=function(){window.beTracker&&window.beTracker.t({hash:`)
	b.Write(h)
	b.WriteString(`})};d.body.appendChild(s)})(document);</script>`)
	return b.Bytes()
}

// WidenCSPForTracker adds MetricoolTrackerOrigin to script-src and
// connect-src (adding the directive with 'self' when the policy has none).
// Other directives are kept as they are.
func WidenCSPForTracker(csp string) string {
	return widen(csp, map[string][]string{
		"script-src":  {MetricoolTrackerOrigin},
		"connect-src": {MetricoolTrackerOrigin},
	})
}

// widen adds the sources of add to their directives, each once; a
// directive the policy lacks is appended as 'self' plus its sources.
func widen(csp string, add map[string][]string) string {
	var parts []string
	seen := map[string]bool{}
	for _, d := range strings.Split(csp, ";") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		name := strings.ToLower(strings.Fields(d)[0])
		if srcs, ok := add[name]; ok && !seen[name] {
			for _, s := range srcs {
				if !hasSource(d, s) {
					d += " " + s
				}
			}
		}
		seen[name] = true
		parts = append(parts, d)
	}
	for _, name := range []string{"script-src", "connect-src", "frame-src"} {
		if srcs, ok := add[name]; ok && !seen[name] {
			parts = append(parts, name+" 'self' "+strings.Join(srcs, " "))
		}
	}
	return strings.Join(parts, "; ")
}
