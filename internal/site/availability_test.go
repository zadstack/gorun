package site_test

import (
	"context"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/site"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAvailabilityPasswordGateAndReset(t *testing.T) {
	e := boot(t)
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	e.setting(site.SettingAvailability, site.Availability{Mode: "password", PasswordHash: hash, Revision: 1})
	for _, path := range []string{"/", "/media/private.jpg", "/sitemap.xml"} {
		r, b := e.get(path)
		if r.StatusCode != 401 || !strings.Contains(b, "password") || r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("gate %s %d", path, r.StatusCode)
		}
	}
	_, b := e.get("/")
	csrf := csrfFrom(t, b)
	r, _ := e.do("POST", "/_site/unlock", url.Values{"password": {"correct horse battery"}})
	if r.StatusCode != 403 {
		t.Fatal("missing csrf", r.StatusCode)
	}
	r, _ = e.do("POST", "/_site/unlock", url.Values{"_csrf": {csrf}, "password": {"wrong password"}})
	if r.StatusCode != 401 {
		t.Fatal("wrong password", r.StatusCode)
	}
	r, _ = e.do("POST", "/_site/unlock", url.Values{"_csrf": {csrf}, "password": {"correct horse battery"}})
	if r.StatusCode != 303 {
		t.Fatal("unlock", r.StatusCode)
	}
	r, _ = e.get("/")
	if r.StatusCode != 200 {
		t.Fatal("cookie", r.StatusCode)
	}
	e.setting(site.SettingAvailability, site.Availability{Mode: "password", PasswordHash: hash, Revision: 2})
	r, _ = e.get("/")
	if r.StatusCode != 401 {
		t.Fatal("old cookie survived reset")
	}
	r, _ = e.get("/admin/login")
	if r.StatusCode == 503 || r.StatusCode == 401 {
		t.Fatal("admin blocked", r.StatusCode)
	}
}
func TestAvailabilityConstructionAndPolicyFailClosed(t *testing.T) {
	e := boot(t)
	e.setting(site.SettingAvailability, site.Availability{Mode: "construction", Message: "<script>bad()</script>", Revision: 1})
	r, b := e.get("/")
	if r.StatusCode != 503 || strings.Contains(b, "<script>bad") || !strings.Contains(b, "&lt;script&gt;") {
		t.Fatal("unsafe message", r.StatusCode)
	}
	r, _ = e.get("/media/private.jpg")
	if r.StatusCode != 503 {
		t.Fatal("public media leak", r.StatusCode)
	}
	e.setting(site.SettingAvailability, site.Availability{Mode: "public"})
	t.Setenv("GORUN_SITE_POLICY", "/invalid/tenant-writable.json")
	r, _ = e.get("/")
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("bad platform policy failed open", r.StatusCode)
	}
	r, _ = e.get("/admin/login")
	if r.StatusCode == 503 || r.StatusCode == 401 {
		t.Fatal("policy blocks recovery")
	}
}

func TestAvailabilityAdminSaveKeepClearAndCSRF(t *testing.T) {
	e := boot(t)
	e.loginAdmin()
	r, b := e.get("/admin/settings/availability")
	if r.StatusCode != 200 {
		t.Fatal("page", r.StatusCode)
	}
	csrf := csrfFrom(t, b)
	form := url.Values{"_csrf": {csrf}, "revision": {"0"}, "mode": {"password"}, "password": {"fixture secret password"}, "message": {"A visitor note"}}
	r, _ = e.do("POST", "/admin/settings/availability", form)
	if r.StatusCode != 303 {
		t.Fatal("save", r.StatusCode)
	}
	_, b = e.get("/admin/settings/availability")
	if strings.Contains(b, "fixture secret password") || strings.Contains(b, "$argon2") {
		t.Fatal("secret reflected")
	}
	var stored site.Availability
	if err := e.app.Deps.Settings.Get(context.Background(), site.SettingAvailability, &stored); err != nil {
		t.Fatal(err)
	}
	if !auth.VerifyPassword(stored.PasswordHash, "fixture secret password") {
		t.Fatal("password not hashed")
	}
	original := stored.PasswordHash
	form.Set("password", "")
	form.Set("revision", "1")
	r, _ = e.do("POST", "/admin/settings/availability", form)
	if r.StatusCode != 303 {
		t.Fatal("blank keep", r.StatusCode)
	}
	stored = site.Availability{}
	e.app.Deps.Settings.Get(context.Background(), site.SettingAvailability, &stored)
	if stored.PasswordHash != original {
		t.Fatal("blank replaced hash")
	}
	form.Set("revision", "2")
	form.Set("mode", "public")
	form.Set("clear_password", "1")
	r, _ = e.do("POST", "/admin/settings/availability", form)
	if r.StatusCode != 303 {
		t.Fatal("clear", r.StatusCode)
	}
	stored = site.Availability{}
	e.app.Deps.Settings.Get(context.Background(), site.SettingAvailability, &stored)
	if stored.PasswordHash != "" {
		t.Fatal("not cleared")
	}
	r, _ = e.do("POST", "/admin/settings/availability", form)
	if r.StatusCode != 409 {
		t.Fatal("stale form", r.StatusCode)
	}
	form.Del("_csrf")
	r, _ = e.do("POST", "/admin/settings/availability", form)
	if r.StatusCode != 403 {
		t.Fatal("csrf", r.StatusCode)
	}
}

func TestAvailabilityEditorMediaAndPublicPageIsolation(t *testing.T) {
	e := boot(t)
	e.loginAdmin()
	e.setting(site.SettingAvailability, site.Availability{Mode: "construction"})
	r, _ := e.get("/media/private.jpg")
	if r.StatusCode == 503 || r.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("editor media", r.StatusCode, r.Header)
	}
	r, _ = e.get("/")
	if r.StatusCode != 503 {
		t.Fatal("admin bypassed public restriction")
	}
}

func TestAvailabilityConstructionHeading(t *testing.T) {
	e := boot(t)
	e.setting(site.SettingAvailability, site.Availability{Mode: "construction"})
	for _, lang := range []string{"en", "ar"} {
		e.setting("site.locale", lang)
		r, b := e.get("/")
		want := "Under construction"
		if lang == "ar" {
			want = "الموقع تحت الإنشاء"
		}
		if r.StatusCode != 503 || !strings.Contains(b, want) {
			t.Fatal(lang, r.StatusCode)
		}
	}
}
