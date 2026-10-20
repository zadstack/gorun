package auth_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSetupRejectsEmptySiteTitle(t *testing.T) {
	s := boot(t)
	_, page := s.do("GET", "/admin/setup", nil)
	token := csrfFrom(t, page)
	resp, body := s.do("POST", "/admin/setup", url.Values{
		"_csrf": {token}, "name": {"Ahmed"}, "email": {"admin@example.com"},
		"password": {"correct horse battery"}, "title": {"   "}, "locale": {"en"},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity || s.userCount() != 0 {
		t.Fatalf("empty title accepted: %d users=%d", resp.StatusCode, s.userCount())
	}
	if !strings.Contains(body, "site name") && !strings.Contains(body, "اسمًا للموقع") {
		t.Fatalf("missing title error:\n%s", body)
	}
	if got := s.app.Deps.Settings.GetString(context.Background(), "site.title", "unset"); got != "unset" && got != "" {
		t.Fatalf("empty title wrote site.title=%q", got)
	}
}

func TestSetupStoresSiteIdentityAtomically(t *testing.T) {
	s := boot(t)
	_, page := s.do("GET", "/admin/setup", nil)
	resp, _ := s.do("POST", "/admin/setup", url.Values{
		"_csrf": {csrfFrom(t, page)}, "name": {"Ahmed"}, "email": {"owner@example.com"},
		"password": {"correct horse battery"}, "title": {"  Atlas Press  "}, "tagline": {"  Clear words  "}, "locale": {"en"},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/workspace" {
		t.Fatalf("setup: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	ctx := context.Background()
	if got := s.app.Deps.Settings.GetString(ctx, "site.title", ""); got != "Atlas Press" {
		t.Fatalf("title=%q", got)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "site.tagline", ""); got != "Clear words" {
		t.Fatalf("tagline=%q", got)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "site.locale", ""); got != "en" {
		t.Fatalf("locale=%q", got)
	}
	var userLocale string
	if err := s.app.Deps.DB.QueryRow(`SELECT locale FROM users WHERE email = ?`, "owner@example.com").Scan(&userLocale); err != nil || userLocale != "en" {
		t.Fatalf("user locale=%q err=%v", userLocale, err)
	}
}
