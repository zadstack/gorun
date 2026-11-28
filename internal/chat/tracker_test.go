package chat

import (
	"strings"
	"testing"
)

const testTrackerHash = "a1b2c3d4e5f60718"

func (e *env) configureTracker(hash string) {
	e.set(SettingMetricool, map[string]string{"user_id": "12", "blog_id": "34", "tracker_hash": hash})
}

func TestMetricoolTrackerOnThemePagesWithItsCSP(t *testing.T) {
	e := boot(t)
	e.configureTracker(testTrackerHash)

	resp, body := e.get("/")
	if n := strings.Count(body, metricoolTrackerScript); n != 1 {
		t.Fatalf("tracker %d times on a theme page, want 1:\n%s", n, body)
	}
	if !strings.Contains(body, `beTracker.t({hash:"`+testTrackerHash+`"})`) {
		t.Fatalf("tracker not started with the hash:\n%s", body)
	}
	if i, j := strings.Index(body, metricoolTrackerScript), strings.LastIndex(strings.ToLower(body), "</body>"); j < 0 || i > j {
		t.Fatalf("tracker at %d, </body> at %d", i, j)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	script := directive(csp, "script-src")
	k := strings.Index(script, "'nonce-")
	if k < 0 {
		t.Fatalf("no nonce in %q", script)
	}
	nonce := strings.TrimSuffix(strings.Fields(script[k:])[0][len("'nonce-"):], "'")
	if !strings.Contains(body, `<script nonce="`+nonce+`">(function(d){var s=d.createElement("script");s.src="`+metricoolTrackerScript) {
		t.Fatalf("tracker is not nonced with %q:\n%s", nonce, body)
	}
	for _, name := range []string{"script-src", "connect-src"} {
		if d := directive(csp, name); !hasSource(d, MetricoolTrackerOrigin) || !hasSource(d, "'self'") {
			t.Errorf("%s %q lacks %s", name, d, MetricoolTrackerOrigin)
		}
	}
	if directive(csp, "frame-src") != "" {
		t.Errorf("the tracker alone added frame-src: %q", csp)
	}
	// Admin pages never get it, nor their CSP.
	if resp, admin := e.get("/admin/x"); strings.Contains(admin, metricoolTrackerScript) || strings.Contains(resp.Header.Get("Content-Security-Policy"), "metricool") {
		t.Error("tracker on an admin path")
	}
}

func TestMetricoolTrackerBesideTheChatWidget(t *testing.T) {
	e := boot(t)
	e.configure()
	e.configureTracker(testTrackerHash)
	resp, body := e.get("/")
	if !strings.Contains(body, sdkPath) || !strings.Contains(body, metricoolTrackerScript) {
		t.Fatalf("want both the widget and the tracker:\n%s", body)
	}
	if d := directive(resp.Header.Get("Content-Security-Policy"), "script-src"); !hasSource(d, testBase) || !hasSource(d, MetricoolTrackerOrigin) {
		t.Errorf("script-src %q lacks one of the hosts", d)
	}
}

func TestMetricoolTrackerNeedsAValidHash(t *testing.T) {
	e := boot(t)
	for _, hash := range []string{"", "short", `x"});alert(1);//`, strings.Repeat("a", 65)} {
		e.configureTracker(hash)
		resp, body := e.get("/")
		if strings.Contains(body, metricoolTrackerScript) || strings.Contains(resp.Header.Get("Content-Security-Policy"), "metricool") {
			t.Errorf("hash %q: tracker printed", hash)
		}
	}
}
