package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/mailer"
)

type metricoolRoundTripper func(*http.Request) (*http.Response, error)

func (f metricoolRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func metricoolFake(t *testing.T, reply string) *int {
	t.Helper()
	calls := new(int)
	old := http.DefaultTransport
	http.DefaultTransport = metricoolRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "app.metricool.com" {
			return old.RoundTrip(r)
		}
		*calls++
		if r.Method == "GET" {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`[{"id":23}]`))}, nil
		}
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["draft"] != true || payload["autoPublish"] != false {
			t.Error("unsafe provider payload")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(reply))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	return calls
}
func metricoolConfigValue(t *testing.T, s *site) map[string]any {
	t.Helper()
	var c map[string]any
	if err := s.app.Deps.Settings.Get(context.Background(), "metricool.config", &c); err != nil {
		t.Fatal(err)
	}
	return c
}
func configureMetricool(t *testing.T, s *site) {
	t.Helper()
	if r, _ := s.post("/admin/settings/metricool", url.Values{"user_id": {"12"}, "blog_id": {"23"}, "token": {"private-metricool-token"}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
}
func TestMetricoolSettingsWriteOnlyCASAndCSRF(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	configureMetricool(t, s)
	c := metricoolConfigValue(t, s)
	secret := c["secret"].(string)
	raw, err := mailer.DecryptSecret(s.app.Deps.Config.SecretKey, secret)
	if err != nil || raw != "private-metricool-token" || secret == raw {
		t.Fatal("not encrypted")
	}
	if r, b := s.do("GET", "/admin/settings/metricool", nil); r.StatusCode != 200 || strings.Contains(b, raw) || strings.Contains(b, secret) {
		t.Fatal("settings must render without secrets")
	}
	form := url.Values{"version": {c["version"].(string)}, "user_id": {"12"}, "blog_id": {"23"}}
	if r, _ := s.do("POST", "/admin/settings/metricool", form); r.StatusCode != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if r, _ := s.post("/admin/settings/metricool", form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	if metricoolConfigValue(t, s)["secret"] != secret {
		t.Fatal("empty token did not preserve secret")
	}
	if r, _ := s.post("/admin/settings/metricool", form); r.StatusCode != 409 {
		t.Fatal("stale save accepted")
	}
	c = metricoolConfigValue(t, s)
	form.Set("version", c["version"].(string))
	form.Set("clear_secret", "on")
	s.post("/admin/settings/metricool", form)
	if metricoolConfigValue(t, s)["secret"] != "" {
		t.Fatal("clear failed")
	}
	s.login("reader@example.com", "author", "en")
	if r, _ := s.do("GET", "/admin/settings/metricool", nil); r.StatusCode != 403 {
		t.Fatal("author settings access")
	}
}
func TestMetricoolDraftIdempotencyAndUncertainNeverRetry(t *testing.T) {
	for _, reply := range []string{`{"data":{"id":91,"draft":true,"autoPublish":false}}`, `{"data":{"id":91,"draft":false}}`} {
		t.Run(reply, func(t *testing.T) {
			calls := metricoolFake(t, reply)
			s := boot(t, content.New())
			s.login("admin@example.com", "admin", "en")
			configureMetricool(t, s)
			c := metricoolConfigValue(t, s)
			if r, _ := s.post("/admin/settings/metricool/test", url.Values{"version": {c["version"].(string)}}); r.StatusCode != 303 {
				t.Fatal(r.StatusCode)
			}
			c = metricoolConfigValue(t, s)
			_, body := s.do("GET", "/admin/metricool", nil)
			nonce := regexp.MustCompile(`name="nonce" value="([^"]+)"`).FindStringSubmatch(body)
			if nonce == nil {
				t.Fatal("missing composer nonce")
			}
			form := url.Values{"nonce": {nonce[1]}, "version": {c["version"].(string)}, "text": {"Reviewed text"}, "network": {"linkedin"}, "date_time": {time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02T15:04:05")}, "timezone": {"UTC"}, "reviewed": {"on"}}
			expected := 200
			if strings.Contains(reply, `"draft":false`) {
				expected = 502
			}
			for i := 0; i < 2; i++ {
				r, b := s.post("/admin/metricool/drafts", form)
				if r.StatusCode != expected {
					t.Fatalf("status %d %s", r.StatusCode, b)
				}
				mustNotContain(t, b, "private-metricool-token")
			}
			if *calls != 2 {
				t.Fatalf("duplicate outbound call: %d", *calls)
			}
			form.Set("text", "Changed")
			if r, _ := s.post("/admin/metricool/drafts", form); r.StatusCode != 409 {
				t.Fatal("nonce rebound")
			}
			if *calls != 2 {
				t.Fatal("conflict sent")
			}
		})
	}
}
func TestMetricoolDisabledPermissionsAndExplicitReview(t *testing.T) {
	calls := metricoolFake(t, `{"data":{"id":91,"draft":true}}`)
	s := boot(t, content.New())
	s.login("admin@example.com", "admin", "en")
	if r, b := s.do("GET", "/admin/metricool", nil); r.StatusCode != 200 || strings.Contains(b, `name="text"`) {
		t.Fatal("unconfigured composer enabled")
	}
	if r, _ := s.post("/admin/metricool/drafts", url.Values{}); r.StatusCode != 409 {
		t.Fatal("unconfigured send")
	}
	configureMetricool(t, s)
	c := metricoolConfigValue(t, s)
	s.post("/admin/settings/metricool/test", url.Values{"version": {c["version"].(string)}})
	c = metricoolConfigValue(t, s)
	_, body := s.do("GET", "/admin/metricool", nil)
	nonce := regexp.MustCompile(`name="nonce" value="([^"]+)"`).FindStringSubmatch(body)
	form := url.Values{"nonce": {nonce[1]}, "version": {c["version"].(string)}, "text": {"Review"}, "network": {"linkedin"}, "date_time": {time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04:05")}, "timezone": {"UTC"}}
	if r, _ := s.post("/admin/metricool/drafts", form); r.StatusCode != 422 {
		t.Fatal("missing explicit review accepted")
	}
	form.Set("reviewed", "on")
	form.Del("_csrf")
	if r, _ := s.do("POST", "/admin/metricool/drafts", form); r.StatusCode != 403 {
		t.Fatal("missing csrf send")
	}
	s.login("contributor@example.com", "contributor", "en")
	if r, _ := s.do("GET", "/admin/metricool", nil); r.StatusCode != 403 {
		t.Fatal("contributor composer access")
	}
	if r, _ := s.post("/admin/metricool/drafts", form); r.StatusCode != 403 {
		t.Fatal("contributor send")
	}
	if *calls != 1 {
		t.Fatal("unauthorized provider call")
	}
}
func TestMetricoolNonceOwnerRateLimitAndConfigValidation(t *testing.T) {
	calls := metricoolFake(t, `{"data":{"id":91,"draft":true}}`)
	s := boot(t, content.New())
	u := s.login("admin@example.com", "admin", "en")
	if r, b := s.post("/admin/settings/metricool", url.Values{"user_id": {"0"}, "blog_id": {"23"}, "token": {"secret-must-not-echo"}}); r.StatusCode != 422 || strings.Contains(b, "secret-must-not-echo") {
		t.Fatal("invalid ID/secret echo")
	}
	configureMetricool(t, s)
	c := metricoolConfigValue(t, s)
	if r, _ := s.post("/admin/settings/metricool/test", url.Values{"version": {"stale"}}); r.StatusCode != 409 {
		t.Fatal("stale test accepted")
	}
	s.post("/admin/settings/metricool/test", url.Values{"version": {c["version"].(string)}})
	c = metricoolConfigValue(t, s)
	_, body := s.do("GET", "/admin/metricool", nil)
	nonce := regexp.MustCompile(`name="nonce" value="([^"]+)"`).FindStringSubmatch(body)[1]
	form := url.Values{"nonce": {nonce}, "version": {c["version"].(string)}, "text": {"Reviewed"}, "network": {"linkedin"}, "date_time": {time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04:05")}, "timezone": {"UTC"}, "reviewed": {"on"}}
	if r, _ := s.post("/admin/metricool/drafts", form); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	s.login("other@example.com", "admin", "en")
	if r, _ := s.post("/admin/metricool/drafts", form); r.StatusCode != 409 {
		t.Fatal("nonce stolen by other actor")
	}
	if *calls != 2 {
		t.Fatal("owner mismatch sent request")
	}
	s.signIn("admin@example.com")
	for i := 0; i < 20; i++ {
		if err := s.app.Deps.Settings.Set(context.Background(), "metricool.draft.rate-"+string(rune('a'+i)), map[string]string{"actor": u.ID, "created_at": time.Now().UTC().Format(time.RFC3339)}); err != nil {
			t.Fatal(err)
		}
	}
	_, body = s.do("GET", "/admin/metricool", nil)
	form.Set("nonce", regexp.MustCompile(`name="nonce" value="([^"]+)"`).FindStringSubmatch(body)[1])
	if r, _ := s.post("/admin/metricool/drafts", form); r.StatusCode != 422 {
		t.Fatal("rate limit missing")
	}
	if *calls != 2 {
		t.Fatal("rate limited request sent")
	}
}
