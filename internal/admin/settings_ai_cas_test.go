package admin_test

import (
	"context"
	"net/url"
	"regexp"
	"testing"
)

func aiFormToken(t *testing.T, s *site) string {
	t.Helper()
	_, body := s.do("GET", "/admin/settings/ai", nil)
	m := regexp.MustCompile(`name="edit_token" value="([^"]+)"`).FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatal("missing AI concurrency token")
	}
	return m[1]
}
func TestAISettingsRejectStaleAndPreserveProposal(t *testing.T) {
	s := boot(t)
	s.login("ai-cas@example.com", "admin", "en")
	token := aiFormToken(t, s)
	form := url.Values{"edit_token": {token}, "provider": {"openai"}, "model": {"first-model"}, "base_url": {"https://example.com/v1"}}
	if r, _ := s.post("/admin/settings/ai", form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	form.Set("model", "stale-proposal")
	if r, body := s.post("/admin/settings/ai", form); r.StatusCode != 409 {
		t.Fatal(r.StatusCode)
	} else {
		mustContain(t, body, `value="stale-proposal"`, `value="`+token+`"`)
	}
	if got := s.app.Deps.Settings.GetString(context.Background(), "ai.model", ""); got != "first-model" {
		t.Fatal("overwrote concurrent config")
	}
	form.Del("edit_token")
	if r, _ := s.post("/admin/settings/ai", form); r.StatusCode != 409 {
		t.Fatal("missing token accepted", r.StatusCode)
	}
}

func TestAISettingsFailureRollsBackAndActivationInvalidatesForm(t *testing.T) {
	s := boot(t)
	s.login("ai-atomic@example.com", "admin", "en")
	ctx := context.Background()
	configureAI(t, s, url.Values{"provider": {"openai"}, "base_url": {"https://example.com/v1"}, "model": {"original"}})
	token := aiFormToken(t, s)
	if _, err := s.app.Deps.DB.Exec(`CREATE TRIGGER reject_ai_provider BEFORE UPDATE ON settings WHEN NEW.key='ai.provider' BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	r, _ := s.post("/admin/settings/ai", url.Values{"edit_token": {token}, "provider": {"openai"}, "base_url": {"https://different.example/v1"}, "model": {"must-rollback"}})
	if r.StatusCode != 500 {
		t.Fatal(r.StatusCode)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "ai.model", ""); got != "original" {
		t.Fatal("partial write", got)
	}
	if _, err := s.app.Deps.DB.Exec(`DROP TRIGGER reject_ai_provider`); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"name": {"Replacement"}, "kind": {"ai"}, "provider": {"openai"}, "model": {"profile-model"}, "endpoint": {"https://example.com/v1"}}
	if r, _ := s.post("/admin/settings/connections", form); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	row := savedConnection(t, s)
	if r, _ := s.post("/admin/settings/connections/"+row["id"].(string)+"/use-ai", url.Values{"version": {row["version"].(string)}, "ai_edit_token": {token}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	if r, _ := s.post("/admin/settings/ai", url.Values{"edit_token": {token}, "provider": {""}}); r.StatusCode != 409 {
		t.Fatal("activation did not invalidate old form", r.StatusCode)
	}
}
