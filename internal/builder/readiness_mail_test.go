package builder

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

func TestReadinessSelectedMailProviderAndExplicitRecipients(t *testing.T) {
	cases := []struct {
		name                      string
		settings                  map[string]any
		configuration, recipients string
	}{
		{"unconfigured", nil, "warn", "warn"},
		{"SMTP default provider", map[string]any{"mail.host": "private.smtp.invalid", "contact.email": "owner@private.invalid"}, "pass", "pass"},
		{"SMTP explicit provider", map[string]any{"mail.provider": "smtp", "mail.host": "private.smtp.invalid"}, "pass", "warn"},
		{"Resend with form recipients", map[string]any{"mail.provider": "resend", "mail.resend_api_key": "not-decrypted-secret", "mail.from": "sender@private.invalid", "forms.recipients": "one@private.invalid;two@private.invalid\none@private.invalid", "contact.email": "invalid"}, "pass", "pass"},
		{"Resend missing key ignores stale SMTP", map[string]any{"mail.provider": "resend", "mail.host": "private.smtp.invalid", "mail.from": "sender@private.invalid"}, "warn", "warn"},
		{"Resend missing sender", map[string]any{"mail.provider": "resend", "mail.resend_api_key": "not-decrypted-secret"}, "warn", "warn"},
		{"Resend ignores invalid unused SMTP", map[string]any{"mail.provider": "resend", "mail.host": 42, "mail.resend_api_key": "not-decrypted-secret", "mail.from": "sender@private.invalid"}, "pass", "warn"},
		{"unsupported provider", map[string]any{"mail.provider": "unknown", "mail.host": "private.smtp.invalid"}, "warn", "warn"},
		{"unreadable provider", map[string]any{"mail.provider": 42}, "unknown", "warn"},
		{"unreadable selected secret", map[string]any{"mail.provider": "resend", "mail.resend_api_key": 42, "mail.from": "sender@private.invalid"}, "unknown", "warn"},
		{"invalid form recipient does not claim fallback passes", map[string]any{"forms.recipients": "not-an-address", "contact.email": "owner@private.invalid"}, "warn", "warn"},
		{"unreadable recipients", map[string]any{"forms.recipients": 42, "contact.email": "owner@private.invalid"}, "warn", "unknown"},
		{"blank form list falls back to contact", map[string]any{"forms.recipients": "  ", "contact.email": "owner@private.invalid"}, "warn", "pass"},
		{"contact email is not a recipient list", map[string]any{"contact.email": "one@private.invalid,two@private.invalid"}, "warn", "warn"},
		{"display name rejected by plain address contract", map[string]any{"contact.email": "Owner <owner@private.invalid>"}, "warn", "warn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, ctx := starterTest(t)
			for key, setting := range tc.settings {
				if err := h.d.Settings.Set(ctx, key, setting); err != nil {
					t.Fatal(err)
				}
			}
			report := buildReadiness(i18n.WithLocale(ctx, "en"), h.d)
			for id, want := range map[string]string{"smtp_config": tc.configuration, "contact_email": tc.recipients, "smtp_delivery": "unknown", "mail_inbox": "unknown"} {
				check := readinessByID(t, report, id)
				if check.Status != want {
					t.Errorf("%s = %s, want %s (%s)", id, check.Status, want, check.Evidence)
				}
			}
			rendered := fmt.Sprint(report)
			for _, private := range []string{"private.smtp.invalid", "@private.invalid", "not-decrypted-secret", "not-an-address"} {
				if strings.Contains(rendered, private) {
					t.Errorf("report disclosed %q", private)
				}
			}
			if !strings.Contains(readinessByID(t, report, "smtp_delivery").Evidence, "Acceptance does not prove inbox receipt") {
				t.Fatal("provider acceptance conflated with inbox receipt")
			}
			if recipientList, ok := tc.settings["forms.recipients"].(string); ok && strings.TrimSpace(recipientList) != "" {
				if check := readinessByID(t, report, "contact_email"); check.Fix != "/admin/system/form-submissions" {
					t.Fatalf("form recipients remediation: %+v", check)
				}
			}
		})
	}
}
