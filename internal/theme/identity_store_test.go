package theme

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
)

func identityEnv(t *testing.T) (*env, context.Context) {
	t.Helper()
	e := newEnv(t, "../../themes")
	return e, adminCtx()
}

func snap(theme string, values, site map[string]string) IdentitySnapshot {
	if values == nil {
		values = map[string]string{}
	}
	if site == nil {
		site = map[string]string{"site.title": "Site"}
	}
	return IdentitySnapshot{Theme: theme, Values: values, Site: site}
}

func TestIdentityApplyWritesOnlyIdentityKeys(t *testing.T) {
	e, ctx := identityEnv(t)
	e.setting("theme.default.header_style", "rail")
	e.setting("site.title", "Before")
	it := e.fake.Content.Add(content.Item{ID: "p1", Type: "post", Title: "Keep me", Slug: "keep", Status: "published", Locale: "en"})
	rev, err := e.svc.ApplyIdentity(ctx, snap("default", map[string]string{idAccent: "#c2410c", idCustomCSS: ".x{}"}, map[string]string{"site.title": "After", "site.tagline": "Tag"}))
	if err != nil {
		t.Fatal(err)
	}
	if rev.Kind != "apply" || rev.ID == "" {
		t.Fatalf("revision = %+v", rev)
	}
	get := func(k string) string { return e.app.Deps.Settings.GetString(ctx, k, "") }
	if get("theme.default.identity_accent") != "#c2410c" || get("theme.default.custom_css") != ".x{}" || get("site.title") != "After" || get("site.tagline") != "Tag" {
		t.Fatalf("live keys not written: %q %q %q", get("theme.default.identity_accent"), get("theme.default.custom_css"), get("site.title"))
	}
	if get("theme.default.header_style") != "rail" {
		t.Fatal("apply touched a non-identity theme setting")
	}
	if got, _ := e.fake.Content.Get(ctx, it.ID); got == nil || got.Title != "Keep me" {
		t.Fatal("apply touched content")
	}
	if _, ok := e.svc.IdentityDraft(ctx, "default"); ok {
		t.Fatal("draft must be cleared after apply")
	}
	hist, ok := e.svc.IdentityHistory(ctx, "default")
	if !ok || len(hist) != 2 || hist[0].Kind != "apply" || hist[1].Kind != "baseline" || hist[1].Snapshot.Site["site.title"] != "Before" {
		t.Fatalf("history = %+v (ok=%v)", hist, ok)
	}
	var n int
	_ = e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'theme.identity.apply' AND target_id = 'default'`).Scan(&n)
	if n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
}

func TestIdentityRevertRestoresPreviousWithoutContent(t *testing.T) {
	e, ctx := identityEnv(t)
	first, err := e.svc.ApplyIdentity(ctx, snap("default", map[string]string{idAccent: "#111111"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ApplyIdentity(ctx, snap("default", map[string]string{idAccent: "#222222", idRadius: "round"}, nil)); err != nil {
		t.Fatal(err)
	}
	rev, err := e.svc.RevertIdentity(ctx, "default", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Kind != "revert" {
		t.Fatalf("kind = %s", rev.Kind)
	}
	if got := e.app.Deps.Settings.GetString(ctx, "theme.default.identity_accent", ""); got != "#111111" {
		t.Fatalf("accent after revert = %q", got)
	}
	if got := e.app.Deps.Settings.GetString(ctx, "theme.default.identity_radius", "unset"); got != "" {
		t.Fatalf("radius must be cleared by revert, got %q", got)
	}
	hist, _ := e.svc.IdentityHistory(ctx, "default")
	if len(hist) != 4 || hist[0].Kind != "revert" {
		t.Fatalf("history = %d entries, first %s", len(hist), hist[0].Kind)
	}
	if _, err := e.svc.RevertIdentity(ctx, "default", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown revision err = %v", err)
	}
}

func TestIdentityDraftIsPerThemeAndNeverLive(t *testing.T) {
	e, ctx := identityEnv(t)
	e.setting("theme.default.identity_accent", "#333333")
	if err := e.svc.SaveIdentityDraft(ctx, snap("default", map[string]string{idAccent: "#c2410c"}, nil)); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.svc.IdentityDraft(ctx, "business"); ok {
		t.Fatal("draft leaked to another theme")
	}
	d, ok := e.svc.IdentityDraft(ctx, "default")
	if !ok || d.Snapshot.Get(idAccent) != "#c2410c" || d.Author != "u1" || d.SavedAt == "" {
		t.Fatalf("draft = %+v ok=%v", d, ok)
	}
	if live := e.svc.LiveIdentity(ctx, "default"); live.Get(idAccent) != "#333333" {
		t.Fatalf("live changed: %+v", live)
	}
	css := IdentityStylesheet(func(k string) string { return e.app.Deps.Settings.GetString(ctx, SettingKey("default", k), "") })
	if strings.Contains(css, "#c2410c") {
		t.Fatal("draft rendered live")
	}
	if err := e.svc.DiscardIdentityDraft(ctx, "default"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.svc.IdentityDraft(ctx, "default"); ok {
		t.Fatal("draft survived discard")
	}
}

func TestIdentityHistoryBoundedAndTolerant(t *testing.T) {
	e, ctx := identityEnv(t)
	for i := 0; i < 12; i++ {
		if _, err := e.svc.ApplyIdentity(ctx, snap("default", map[string]string{idAccent: fmt.Sprintf("#1111%02d", i)}, nil)); err != nil {
			t.Fatal(i, err)
		}
	}
	hist, ok := e.svc.IdentityHistory(ctx, "default")
	if !ok || len(hist) != identityHistoryKeep || hist[0].Snapshot.Get(idAccent) != "#111111" {
		t.Fatalf("history len=%d ok=%v first=%+v", len(hist), ok, hist[0].Snapshot)
	}
	e.setting(identityHistoryKey("default"), "{broken")
	if hist, ok := e.svc.IdentityHistory(ctx, "default"); ok || hist != nil {
		t.Fatalf("broken history must report invalid, got ok=%v %v", ok, hist)
	}
	if _, err := e.svc.ApplyIdentity(ctx, snap("default", map[string]string{idAccent: "#abcdef"}, nil)); err != nil {
		t.Fatal(err)
	}
	if hist, ok := e.svc.IdentityHistory(ctx, "default"); !ok || len(hist) != 2 || hist[1].Kind != "baseline" {
		t.Fatalf("history after broken = %+v ok=%v", hist, ok)
	}
}

func TestIdentityApplyRejectsInvalid(t *testing.T) {
	e, ctx := identityEnv(t)
	e.fake.Media.Add(media.Media{ID: "m1", Filename: "logo.png", Mime: "image/png"})
	for _, tc := range []struct {
		name, field string
		s           IdentitySnapshot
	}{
		{"color", idAccent, snap("default", map[string]string{idAccent: "red"}, nil)},
		{"title", "site.title", snap("default", nil, map[string]string{"site.title": ""})},
		{"logo", "site.logo", snap("default", nil, map[string]string{"site.title": "S", "site.logo": "missing"})},
		{"contrast", idText, snap("default", map[string]string{idText: "#ffffff", idBackground: "#ffffff"}, nil)},
	} {
		_, err := e.svc.ApplyIdentity(ctx, tc.s)
		var ve *IdentityValidationError
		if !errors.As(err, &ve) || ve.Fields[tc.field] == "" {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
	}
	if _, err := e.svc.ApplyIdentity(ctx, snap("default", nil, map[string]string{"site.title": "S", "site.logo": "m1"})); err != nil {
		t.Fatalf("existing logo rejected: %v", err)
	}
	if got := e.app.Deps.Settings.GetString(ctx, "site.title", ""); got != "S" {
		t.Fatalf("title = %q", got)
	}
	if hist, _ := e.svc.IdentityHistory(ctx, "default"); len(hist) != 2 {
		t.Fatalf("invalid applies must not create revisions: %d", len(hist))
	}
}

// Header-link values travel with the draft and are written in the apply
// transaction, after validation, only when the theme declares them.
func TestIdentityDraftKeepsHeaderLinks(t *testing.T) {
	e, ctx := identityEnv(t)
	s := snap("default", map[string]string{idAccent: "#c2410c"}, nil)
	s.Links = map[string]string{"cta_label": "Call us", "cta_url": "/contact"}
	if err := e.svc.SaveIdentityDraft(ctx, s); err != nil {
		t.Fatal(err)
	}
	d, ok := e.svc.IdentityDraft(ctx, "default")
	if !ok || d.Snapshot.Links["cta_label"] != "Call us" {
		t.Fatalf("draft lost links: %+v", d.Snapshot)
	}
	bad := s
	bad.Links = map[string]string{"cta_label": "Call us", "cta_url": "javascript:alert(1)"}
	_, err := e.svc.ApplyIdentity(ctx, bad)
	var ve *IdentityValidationError
	if !errors.As(err, &ve) || ve.Fields["cta_url"] == "" {
		t.Fatalf("bad url accepted: %v", err)
	}
	if got := e.app.Deps.Settings.GetString(ctx, "theme.default.cta_label", ""); got != "" {
		t.Fatalf("link written despite invalid apply: %q", got)
	}
	if got := e.app.Deps.Settings.GetString(ctx, "theme.default.identity_accent", ""); got != "" {
		t.Fatalf("identity written despite invalid apply: %q", got)
	}
	if _, err := e.svc.ApplyIdentity(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got := e.app.Deps.Settings.GetString(ctx, "theme.default.cta_url", ""); got != "/contact" {
		t.Fatalf("cta_url = %q", got)
	}
}

// A draft remembers the site values it started from; when those changed
// meanwhile (another theme applied a new title), the draft takes the live
// value instead of silently restoring the old one.
func TestIdentityDraftRebasesChangedSiteKeys(t *testing.T) {
	e, ctx := identityEnv(t)
	e.setting("site.title", "Before")
	if err := e.svc.SaveIdentityDraft(ctx, snap("default", nil, map[string]string{"site.title": "Before", "site.tagline": "Draft tag"})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ApplyIdentity(ctx, snap("business", nil, map[string]string{"site.title": "Bar"})); err != nil {
		t.Fatal(err)
	}
	d, ok := e.svc.IdentityDraft(ctx, "default")
	if !ok || d.Snapshot.Site["site.title"] != "Bar" || d.Snapshot.Site["site.tagline"] != "Draft tag" {
		t.Fatalf("draft not rebased: %+v ok=%v", d.Snapshot.Site, ok)
	}
	if len(d.Rebased) != 1 || d.Rebased[0] != "site.title" {
		t.Fatalf("rebased = %v", d.Rebased)
	}
}

// Text contrast is checked against the theme's declared background when
// the snapshot leaves it empty, as the page promises.
func TestIdentityApplyContrastUsesThemeDeclaredBackground(t *testing.T) {
	e, ctx := identityEnv(t)
	_, err := e.svc.ApplyIdentity(ctx, snap("default", map[string]string{idText: "#fafafa"}, nil))
	var ve *IdentityValidationError
	if !errors.As(err, &ve) || ve.Fields[idText] == "" {
		t.Fatalf("near-white text on the light default theme accepted: %v", err)
	}
}

func TestIdentityLengthsCountCharacters(t *testing.T) {
	e, ctx := identityEnv(t)
	title := strings.Repeat("ع", 150) // 300 bytes, 150 characters
	if _, err := e.svc.ApplyIdentity(ctx, snap("default", nil, map[string]string{"site.title": title})); err != nil {
		t.Fatalf("150-character Arabic title rejected: %v", err)
	}
	if _, err := e.svc.ApplyIdentity(ctx, snap("default", nil, map[string]string{"site.title": strings.Repeat("ع", 201)})); err == nil {
		t.Fatal("201-character title accepted")
	}
}
