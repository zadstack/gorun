package admin

import (
	"context"
	"net/http"
)

// MetricoolConfigKey is the setting of Settings → Metricool (JSON
// MetricoolConfig), which the services layer reads and writes as the
// metricool service.
const MetricoolConfigKey = metricoolConfigKey

// MetricoolConfig is Settings → Metricool as stored: Secret is the token
// encrypted with the site key (mailer.EncryptSecret), Version changes on
// every save (the admin form refuses a stale one), and TestStatus is pass,
// fail or empty (untested). Drafts need a pass on the current Version.
type MetricoolConfig = metricoolConfig

// ValidMetricoolID reports a Metricool user or brand id as the admin form
// takes it: 1 to 10 digits, no leading zero.
func ValidMetricoolID(s string) bool { return metricoolID.MatchString(s) }

// ValidMetricoolToken reports a token the admin form would store.
func ValidMetricoolToken(s string) bool { return s != "" && validConnectionSecret(s) }

// ValidMetricoolTrackerHash reports a web tracker hash: 8 to 64 ASCII
// letters and digits.
func ValidMetricoolTrackerHash(s string) bool { return metricoolTrackerHash.MatchString(s) }

// CheckMetricool is the admin's read-only brand test: the token opens
// Metricool's brand list for cfg.UserID and cfg.BlogID is one of its
// brands, not deleted. transport nil is the default one. The error says
// nothing of the provider's answer.
func CheckMetricool(ctx context.Context, cfg MetricoolConfig, token string, transport http.RoundTripper) error {
	return metricoolClient{transport: transport}.test(ctx, cfg, token)
}
