package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func mailToken(t *testing.T, s *site) string {
	t.Helper()
	_, page := s.do("GET", "/admin/settings/mail", nil)
	m := regexp.MustCompile(`name="edit_token" value="([^"]+)"`).FindStringSubmatch(page)
	if len(m) != 2 {
		t.Fatal("mail edit token missing")
	}
	return m[1]
}
func TestResendMailSettingsSecretAndConflict(t *testing.T) {
	s := boot(t)
	s.login("owner@example.invalid", "admin", "en")
	ctx := context.Background()
	token := mailToken(t, s)
	form := url.Values{"edit_token": {token}, "provider": {"resend"}, "resend_api_key": {"re_test_secret_123"}, "from": {"info@example.invalid"}, "host": {"smtp.example.invalid"}, "port": {"587"}, "tls": {"starttls"}, "password": {"smtp_secret"}}
	resp, body := s.post("/admin/settings/mail", form)
	if resp.StatusCode != 303 {
		t.Fatalf("save %d %s", resp.StatusCode, body)
	}
	key := s.app.Deps.Settings.GetString(ctx, "mail.resend_api_key", "")
	if !strings.HasPrefix(key, "enc:v1:") || strings.Contains(key, "re_test_secret") {
		t.Fatal("not encrypted")
	}
	_, page := s.do("GET", "/admin/settings/mail", nil)
	if strings.Contains(page, "re_test_secret_123") || strings.Contains(page, "smtp_secret") {
		t.Fatal("secret disclosed")
	}
	form.Set("resend_api_key", "")
	form.Set("password", "")
	resp, _ = s.post("/admin/settings/mail", form)
	if resp.StatusCode != 409 {
		t.Fatal("stale form accepted", resp.StatusCode)
	}
	form.Set("edit_token", mailToken(t, s))
	form.Set("provider", "smtp")
	resp, _ = s.post("/admin/settings/mail", form)
	if resp.StatusCode != 303 || s.app.Deps.Settings.GetString(ctx, "mail.resend_api_key", "") != key {
		t.Fatal("switch lost key")
	}
	form.Set("edit_token", mailToken(t, s))
	form.Set("clear_resend_api_key", "on")
	resp, _ = s.post("/admin/settings/mail", form)
	if resp.StatusCode != 303 || s.app.Deps.Settings.GetString(ctx, "mail.resend_api_key", "") != "" {
		t.Fatal("clear failed")
	}
	var count int
	if err := s.app.Deps.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE meta_json LIKE '%re_test_secret_123%'`).Scan(&count); err == nil && count != 0 {
		t.Fatal("secret audit")
	}
}
func TestResendMailSettingsValidationAndAuthorization(t *testing.T) {
	s := boot(t)
	s.login("owner@example.invalid", "admin", "en")
	form := url.Values{"edit_token": {mailToken(t, s)}, "provider": {"resend"}, "resend_api_key": {"secret\nheader"}, "tls": {"starttls"}}
	resp, body := s.post("/admin/settings/mail", form)
	if resp.StatusCode != 422 || strings.Contains(body, "secret\nheader") {
		t.Fatal("validation/redaction", resp.StatusCode)
	}
	form.Del("_csrf")
	resp, _ = s.do("POST", "/admin/settings/mail", form)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatal("CSRF missing", resp.StatusCode)
	}
}

func postMailSettings(t *testing.T, s *site, form url.Values) (*http.Response, string) {
	t.Helper()
	form.Set("edit_token", mailToken(t, s))
	return s.post("/admin/settings/mail", form)
}

func TestResendSettingsAPI(t *testing.T) {
	s := boot(t)
	u := s.createUser("api-owner@example.invalid", "admin", "en")
	code, out := apiCall(t, s, u, "PATCH", "/api/v1/settings", `{"mail.provider":"resend","mail.resend_api_key":"re_api_private","mail.from":"info@example.invalid"}`)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	data := out["data"].(map[string]any)
	if data["mail.provider"] != "resend" || data["mail.resend_api_key_set"] != true {
		t.Fatal("wrong selected provider")
	}
	if _, ok := data["mail.resend_api_key"]; ok {
		t.Fatal("secret returned")
	}
	code, _ = apiCall(t, s, u, "PATCH", "/api/v1/settings", `{"mail.host":"smtp.example.invalid"}`)
	if code != 200 || s.app.Deps.Settings.GetString(context.Background(), "mail.provider", "") != "resend" {
		t.Fatal("unrelated patch reset provider")
	}
	code, out = apiCall(t, s, u, "PATCH", "/api/v1/settings", `{"mail.resend_api_key":null}`)
	if code != 200 || out["data"].(map[string]any)["mail.resend_api_key_set"] != false {
		t.Fatal("API clear failed")
	}
	ed := s.createUser("api-editor@example.invalid", "editor", "en")
	if code, _ := apiCall(t, s, ed, "PATCH", "/api/v1/settings", `{"mail.provider":"smtp"}`); code != 403 {
		t.Fatal("editor changed provider")
	}
}

type mailRoundTrip func(*http.Request) (*http.Response, error)

func (f mailRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResendExplicitTestActionOnly(t *testing.T) {
	s := boot(t)
	s.login("test-owner@example.invalid", "admin", "en")
	old := http.DefaultTransport
	var calls atomic.Int32
	http.DefaultTransport = mailRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.resend.com" {
			return old.RoundTrip(r)
		}
		calls.Add(1)
		var payload struct {
			To []string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.To) != 1 || payload.To[0] != "test-owner@example.invalid" {
			t.Error("test recipient mismatch")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"synthetic-message"}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	form := url.Values{"edit_token": {mailToken(t, s)}, "provider": {"resend"}, "resend_api_key": {"re_fake_only"}, "from": {"info@example.invalid"}, "tls": {"starttls"}}
	if resp, _ := s.post("/admin/settings/mail", form); resp.StatusCode != 303 {
		t.Fatal("save", resp.StatusCode)
	}
	if calls.Load() != 0 {
		t.Fatal("save sent mail")
	}
	resp, body := s.post("/admin/settings/mail/test", nil, "HX-Request", "true")
	if resp.StatusCode != 200 || calls.Load() != 1 || !strings.Contains(body, "accepted") {
		t.Fatal("explicit test failed", resp.StatusCode, calls.Load())
	}
}
