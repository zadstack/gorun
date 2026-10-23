package admin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
)

func TestOnboardingReadOnlyPermissionsAndSecretBoundary(t *testing.T) {
	s := boot(t)
	s.login("setup-owner@example.com", "admin", "en")
	ctx := context.Background()
	for key, value := range map[string]string{"ai.provider": "test-provider", "ai.api_key": "private-ai-key-not-for-html", "ai.model": "local-model", "mail.host": "private-mail-host.example"} {
		if err := s.app.Deps.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.app.Deps.Settings.All(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/platform-setup", "/admin/settings/edge/instances"} {
		response, page := s.do("GET", path, nil)
		if response.StatusCode != 200 {
			t.Fatalf("%s: %d %s", path, response.StatusCode, page)
		}
		if strings.Contains(page, "private-ai-key-not-for-html") || strings.Contains(page, "private-mail-host.example") {
			t.Fatal("private configuration leaked")
		}
		if strings.Contains(page, "ZgotmplZ") || strings.Contains(page, "<no value>") {
			t.Fatal("invalid template output")
		}
	}
	after, err := s.app.Deps.Settings.All(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("GET changed settings")
	}
	for key, value := range before {
		if string(value) != string(after[key]) {
			t.Fatalf("GET changed %s", key)
		}
	}
	s.login("setup-editor@example.com", "editor", "en")
	for _, path := range []string{"/admin/platform-setup", "/admin/settings/edge/instances"} {
		response, _ := s.do("GET", path, nil)
		if response.StatusCode != 403 {
			t.Fatalf("editor %s: %d", path, response.StatusCode)
		}
	}
}

func TestOnboardingStatesDoNotClaimOperationTests(t *testing.T) {
	s := boot(t)
	s.login("setup-states@example.com", "admin", "en")
	if err := s.app.Deps.Settings.Set(context.Background(), admin.SettingWorkspace, admin.WorkspaceFull); err != nil {
		t.Fatal(err)
	}
	response, page := s.do("GET", "/admin/platform-setup", nil)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode, page)
	}
	for _, text := range []string{"configuration alone does not prove operation", "does not prove the file still exists", "local database only", "/admin/settings/integrations#integrations-cloudflare"} {
		if !strings.Contains(page, text) {
			t.Fatalf("missing honest state explanation %q", text)
		}
	}
	_, page = s.do("GET", "/admin/settings/edge/instances", nil)
	for _, text := range []string{"not operation-tested", "data-edge-plan", "data-edge-create hidden", "data-edge-plans"} {
		if !strings.Contains(page, text) {
			t.Fatalf("missing edge contract %q", text)
		}
	}
}

func TestOnboardingArabicTemplateAndCollapsedDiagnostics(t *testing.T) {
	s := boot(t)
	s.login("setup-arabic@example.com", "admin", "ar")
	for _, path := range []string{"/admin/platform-setup", "/admin/settings/edge/instances"} {
		response, page := s.do("GET", path, nil)
		if response.StatusCode != 200 || !strings.Contains(page, `lang="ar"`) || !strings.Contains(page, `dir="rtl"`) {
			t.Fatalf("Arabic route %s: status %d", path, response.StatusCode)
		}
		if strings.Contains(page, ">onboarding.") {
			t.Fatalf("untranslated onboarding keys in %s", path)
		}
	}
	_, page := s.do("GET", "/admin", nil)
	for _, widget := range []string{"recent_audit", "jobs_summary"} {
		if !strings.Contains(page, `<details data-widget="`+widget+`">`) {
			t.Fatalf("diagnostic %s should start collapsed", widget)
		}
	}
}
