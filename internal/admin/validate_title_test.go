package admin_test

import (
	"errors"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
)

func TestValidateGeneralSettingRequiresSiteTitle(t *testing.T) {
	for _, empty := range []string{"", "  ", "\t"} {
		_, err := admin.ValidateGeneralSetting("site.title", empty)
		var se *admin.SettingError
		if !errors.As(err, &se) || se.Code != "admin.settings.error_title_required" {
			t.Fatalf("%q: %v", empty, err)
		}
	}
	got, err := admin.ValidateGeneralSetting("site.title", "  Harbor  ")
	if err != nil || got != "Harbor" {
		t.Fatalf("valid title: %q %v", got, err)
	}
	got, err = admin.ValidateGeneralSetting("site.tagline", "")
	if err != nil || got != "" {
		t.Fatalf("empty tagline must stay allowed: %q %v", got, err)
	}
}
