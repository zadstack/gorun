package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func generalForm(baseURL string) url.Values {
	return url.Values{"title": {"ZAD STACK"}, "tagline": {"t"}, "locale": {"ar"}, "timezone": {"Africa/Cairo"},
		"base_url": {baseURL}, "theme": {"default"}, "front_page": {""}}
}

func TestSettingsSaveValidatesBaseURL(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	if resp, _ := s.do("GET", "/admin/settings/general", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("form: %d", resp.StatusCode)
	}
	for _, bad := range []string{"ftp://example.com", "example.com", "https://example.com/blog", "https://user:pw@example.com", "https://example.com?x=1", "https://"} {
		resp, page := s.post("/admin/settings/general", generalForm(bad))
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%q: status %d", bad, resp.StatusCode)
		}
		mustContain(t, page, "field-error", "Enter an absolute http or https URL without a path")
		if got := s.app.Deps.Settings.GetString(ctx, "site.base_url", ""); got != "" {
			t.Fatalf("%q must not be stored, got %q", bad, got)
		}
	}
	bad := generalForm("https://example.com")
	bad.Set("timezone", "Mars/Olympus")
	if resp, _ := s.post("/admin/settings/general", bad); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad timezone: %d", resp.StatusCode)
	}

	resp, _ := s.post("/admin/settings/general", generalForm("https://Example.com/"))
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("valid save: %d", resp.StatusCode)
	}
	for k, want := range map[string]string{"site.base_url": "https://Example.com", "site.title": "ZAD STACK", "site.timezone": "Africa/Cairo", "site.locale": "ar"} {
		if got := s.app.Deps.Settings.GetString(ctx, k, ""); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
	var meta string
	s.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'settings.update'`).Scan(&meta)
	if !strings.Contains(meta, "site.base_url") || strings.Contains(meta, "ZAD STACK") {
		t.Fatalf("audit meta must list keys, not values: %s", meta)
	}
}

func TestSettingsRequirePermission(t *testing.T) {
	s := boot(t)
	s.login("ed@example.com", "editor", "en")
	if resp, _ := s.do("GET", "/admin/settings/general", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor settings: %d", resp.StatusCode)
	}
	if resp, _ := s.post("/admin/settings/mail", url.Values{"host": {"x"}}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor mail save: %d", resp.StatusCode)
	}
}

func TestMailSettingsEncryptPasswordAndTestSend(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	ctx := context.Background()
	form := url.Values{"host": {"smtp.example.com"}, "port": {"2525"}, "username": {"u"}, "password": {"pa55word"}, "from": {"noreply@example.com"}, "from_name": {"ZAD"}, "tls": {"starttls"}}
	if resp, _ := postMailSettings(t, s, form); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("mail save: %d", resp.StatusCode)
	}
	enc := s.app.Deps.Settings.GetString(ctx, "mail.password", "")
	if enc == "" || strings.Contains(enc, "pa55word") {
		t.Fatalf("password must be stored encrypted: %q", enc)
	}
	_, page := s.do("GET", "/admin/settings/mail", nil)
	mustNotContain(t, page, "pa55word", enc)
	// Blank password keeps the stored one.
	form.Set("password", "")
	postMailSettings(t, s, form)
	if got := s.app.Deps.Settings.GetString(ctx, "mail.password", ""); got != enc {
		t.Fatal("blank password must keep the stored value")
	}
	form.Set("port", "99999")
	if resp, _ := postMailSettings(t, s, form); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad port: %d", resp.StatusCode)
	}
	// Clear host so the test email is logged instead of sent.
	form.Set("port", "587")
	form.Set("host", "")
	postMailSettings(t, s, form)
	resp, frag := s.post("/admin/settings/mail/test", nil, "HX-Request", "true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("test send: %d", resp.StatusCode)
	}
	mustContain(t, frag, `data-mail-test="ok"`, "admin@example.com")
	mustNotContain(t, frag, "<html")
	if s.auditCount("mail.test_sent") != 1 {
		t.Fatal("test mail not audited")
	}
}
