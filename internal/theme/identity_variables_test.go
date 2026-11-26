package theme

import (
	"strings"
	"testing"
)

// The Variables panel resolves both schemes from one snapshot: explicit
// colours win in both, the dark palette fills the rest of the dark scheme,
// link ink is mixed toward readability, and each pair carries a WCAG level.
func TestIdentityVariablesResolveBothSchemes(t *testing.T) {
	e := newEnv(t, "../../themes")
	a := &admin{s: e.svc}
	ctx := adminCtx()

	// Amber accent on a light page: links must darken to read, white does
	// not read on the accent, and the dark scheme shows its own pairs.
	snap := IdentitySnapshot{Theme: "default", Values: map[string]string{idAccent: "#f0b429", idBackground: "#ffffff", idText: "#111111"}}
	v := a.identityVariablesFor(ctx, snap)
	if v.Active != "light" {
		t.Fatalf("active = %q", v.Active)
	}
	if v.Light.Background != "#ffffff" || v.Light.Text != "#111111" || v.Light.Accent != "#f0b429" {
		t.Fatalf("light tokens: %+v", v.Light)
	}
	if v.Light.Link == "#f0b429" || contrast(v.Light.Link, "#ffffff") < minTextContrast {
		t.Fatalf("light link ink %q does not read on white", v.Light.Link)
	}
	if v.Light.OnAccent != "#111111" {
		t.Fatalf("on-accent for amber = %q, want near-black", v.Light.OnAccent)
	}
	// Explicit colours override the dark palette too.
	if v.Dark.Background != "#ffffff" || v.Dark.Text != "#111111" || v.Dark.Muted != darkPaletteValue("--muted") {
		t.Fatalf("dark tokens: %+v", v.Dark)
	}
	keys := func(rows []contrastRow) string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Key+"="+r.Level)
		}
		return strings.Join(out, " ")
	}
	if got := keys(v.Light.Pairs); got != "text_bg=aa link_bg=aa on_accent=aa" {
		t.Fatalf("light pairs: %s", got)
	}
	// Muted dark text on an explicit white page fails: the panel says so.
	if got := keys(v.Dark.Pairs); !strings.HasPrefix(got, "text_bg=aa muted_bg=fail") {
		t.Fatalf("dark pairs: %s", got)
	}
	if len(v.Pairs) != 4 || v.Pairs[0].Key != "text_bg" || v.Pairs[1].Light != nil || v.Pairs[1].Dark == nil {
		t.Fatalf("pair rows: %+v", v.Pairs)
	}

	// Dark scheme with no explicit colours: the palette's ink on its page.
	dark := IdentitySnapshot{Theme: "default", Values: map[string]string{idScheme: "dark", idAccent: "#167252"}}
	v = a.identityVariablesFor(ctx, dark)
	if v.Active != "dark" || v.Dark.Background != identityDarkBackground || v.Dark.Text != identityDarkInk {
		t.Fatalf("dark fallback: active=%q %+v", v.Active, v.Dark)
	}
	if contrast(v.Dark.Link, identityDarkBackground) < minTextContrast {
		t.Fatalf("dark link ink %q does not read on the dark page", v.Dark.Link)
	}
	var roles []string
	for _, r := range v.Roles {
		roles = append(roles, r.Token+":"+r.Roles)
	}
	joined := strings.Join(roles, "|")
	if !strings.Contains(joined, "--bg:--background") || !strings.Contains(joined, "--accent:--primary, --ring") {
		t.Fatalf("roles: %s", joined)
	}
}

// The page prints the panel with both scheme columns and the data the
// script recomputes from.
func TestIdentityPageShowsVariablesPanel(t *testing.T) {
	e := newEnv(t, "../../themes")
	c, csrf := identityClient(t, e)
	if resp, _ := c.post(identityPath, identityForm(csrf, "draft", "identity_accent", "#f0b429", "identity_background", "#ffffff", "identity_text", "#111111")); resp.StatusCode != 303 {
		t.Fatalf("draft: %d", resp.StatusCode)
	}
	_, page := c.get(identityPath)
	for _, want := range []string{
		`data-identity-variables`, `data-active="light"`, `data-dark-bg="#ffffff"`, `data-dark-muted="` + darkPaletteValue("--muted") + `"`,
		`data-var-cell="light:text_bg"`, `data-var-cell="dark:on_accent"`, `data-level="fail"`, `--primary, --ring`,
	} {
		mustContain(t, page, want)
	}
}
