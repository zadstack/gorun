package theme

import (
	"strings"
	"testing"
)

// Section backgrounds are identity tokens: the stylesheet carries only
// variables (ink, density scale) and points .gp-s-bg at a pattern variable
// that sections.css draws, so Go never holds CSS drawings.
func TestIdentityPatternEmitsTokensNotDrawings(t *testing.T) {
	css := IdentityStylesheet(identityGetter(map[string]string{
		idPattern: "dots", idPatternDensity: "dense", idPatternColor: "#c2410c",
	}))
	mustContain(t, css,
		"--gp-pattern-ink:#c2410c;",
		"--gp-pattern-scale:1.4;",
		":where(:root[data-theme]) .gp-s-bg{--gp-pattern-image:var(--gp-pat-dots);--gp-pattern-size:var(--gp-pat-dots-size)}",
	)
	for _, bad := range []string{"gradient", "url(", "<svg"} {
		if strings.Contains(css, bad) {
			t.Errorf("identity stylesheet must not draw patterns (%q found):\n%s", bad, css)
		}
	}
	// Pattern alone, no colour or density: only the selector rule, no body rule.
	css = IdentityStylesheet(identityGetter(map[string]string{idPattern: "waves"}))
	mustContain(t, css, ".gp-s-bg{--gp-pattern-image:var(--gp-pat-waves);--gp-pattern-size:var(--gp-pat-waves-size)}")
	mustNotContain(t, css, "--gp-pattern-ink", "--gp-pattern-scale", "body{")
	// "none" is an explicit off switch: sections.css draws --gp-pat-none as nothing.
	css = IdentityStylesheet(identityGetter(map[string]string{idPattern: "none"}))
	mustContain(t, css, ".gp-s-bg{--gp-pattern-image:var(--gp-pat-none);--gp-pattern-size:var(--gp-pat-none-size)}")
}

func TestIdentityPatternRejectsUnknownValues(t *testing.T) {
	css := IdentityStylesheet(identityGetter(map[string]string{
		idPattern: "dots;}body{display:none", idPatternDensity: "huge", idPatternColor: "red",
	}))
	if css != "" {
		t.Fatalf("unknown pattern values must render nothing, got %q", css)
	}
	errs := validateIdentity(map[string]any{idPatternColor: "red", idPattern: "dots"})
	if errs[idPatternColor] != "theme.identity.error_color" {
		t.Errorf("pattern colour must be a hex colour: %v", errs)
	}
}

func TestIdentityPatternFieldsInSchemaAndDesignGroup(t *testing.T) {
	info := Info{Slug: "x", Supports: []string{"identity"}}
	schema := settingsSchema(info)
	for _, k := range []string{idPattern, idPatternDensity, idPatternColor} {
		var found bool
		for _, f := range schema {
			found = found || f.Key == k
		}
		if !found {
			t.Errorf("schema lacks %s", k)
		}
		if !isIdentityResetKey(k) {
			t.Errorf("%s must be resettable like the other identity keys", k)
		}
	}
	var pattern int
	for _, f := range schema {
		if f.Key == idPattern {
			pattern = len(f.Options)
		}
	}
	if pattern != 9 { // theme default + none + 7 drawings
		t.Errorf("pattern select has %d options, want 9", pattern)
	}
	groups := themeSettingsGroups([]settingView{{Key: idPattern}, {Key: "header_style"}})
	if len(groups) != 2 || groups[1].Key != "design" {
		t.Fatalf("pattern must land in the design tab: %+v", groups)
	}
}
