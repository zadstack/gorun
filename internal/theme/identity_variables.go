package theme

import (
	"context"
	"maps"
	"strings"

	"github.com/zadstack/gorun/pkg/themekit"
)

// The Variables panel on Appearance → Identity: the identity tokens as the
// light and the dark scheme would render them, the role names that follow
// each token, and the contrast of the pairs a reader meets, checked in both
// schemes at once so a choice that reads on the light page is not a surprise
// on the dark one. identity-studio.js recomputes the same pairs as the
// colours are typed; the server values are the page's starting point and the
// save check (validateSnapshot) has the final say.

// identityVariables is the panel's data.
type identityVariables struct {
	Light, Dark identityScheme
	Active      string // "light" or "dark": the scheme the snapshot selects
	Pairs       []identityPairView
	Roles       []identityRole
}

// identityPairView is one table row: a pair in both schemes (nil when a
// scheme cannot resolve it).
type identityPairView struct {
	Key         string
	Light, Dark *contrastRow
}

// identityPairKeys are the table rows, in display order.
var identityPairKeys = []string{"text_bg", "muted_bg", "link_bg", "on_accent"}

// identityScheme is one scheme's effective tokens and contrast pairs. A
// colour the identity leaves to the theme is "" when the admin cannot
// resolve it, and the pairs that need it are left out.
type identityScheme struct {
	Background, Text, Accent, Link, OnAccent, Muted string
	Pairs                                           []contrastRow
}

// identityRole is one identity token and the shadcn role names aliased to it.
type identityRole struct {
	Token string
	Roles string
}

// identityRoleSources are the identity-driven tokens the panel lists, in
// display order.
var identityRoleSources = []string{"--bg", "--ink", "--accent", "--on-accent", "--link", "--muted"}

func (a *admin) identityVariablesFor(ctx context.Context, snap IdentitySnapshot) identityVariables {
	accent := snap.Get(idAccent)
	if !hexColor.MatchString(accent) {
		accent = ""
	}
	out := identityVariables{Light: a.identitySchemeFor(ctx, snap, "light", accent), Dark: a.identitySchemeFor(ctx, snap, "dark", accent), Active: "light"}
	if snap.Get(idScheme) == "dark" {
		out.Active = "dark"
	}
	for _, key := range identityPairKeys {
		out.Pairs = append(out.Pairs, identityPairView{Key: key, Light: findPair(out.Light.Pairs, key), Dark: findPair(out.Dark.Pairs, key)})
	}
	for _, src := range identityRoleSources {
		var names []string
		for _, rt := range themekit.RoleTokens {
			if rt.Source == src {
				names = append(names, rt.Name)
			}
		}
		out.Roles = append(out.Roles, identityRole{Token: src, Roles: strings.Join(names, ", ")})
	}
	return out
}

// identitySchemeFor resolves snap as scheme would render it: the explicit
// colours win, then the dark palette (dark) or what renders beneath the
// identity (light), the way renderedTextBackground and buildIdentity do.
func (a *admin) identitySchemeFor(ctx context.Context, snap IdentitySnapshot, scheme, accent string) identityScheme {
	forced := snap
	forced.Values = maps.Clone(snap.Values)
	if forced.Values == nil {
		forced.Values = map[string]string{}
	}
	forced.Values[idScheme] = scheme
	text, back := a.s.renderedTextBackground(ctx, forced)
	sc := identityScheme{Background: back, Text: text, Accent: accent}
	if scheme == "dark" {
		sc.Muted = darkPaletteValue("--muted")
	}
	if accent != "" {
		// buildIdentity derives link ink against the identity's own
		// background, else the light fallback; the pair below measures it
		// against the background that actually renders.
		bg := identityBackground(forced.Get)
		if bg == "" {
			bg = identityLightFallback
		}
		sc.Link = readableInk(accent, bg)
		sc.OnAccent = identityOnAccent(accent)
	}
	if text != "" && back != "" {
		sc.Pairs = append(sc.Pairs, row("text_bg", text, back, "--ink", "--bg"))
	}
	if sc.Muted != "" && back != "" {
		sc.Pairs = append(sc.Pairs, row("muted_bg", sc.Muted, back, "--muted", "--bg"))
	}
	if sc.Link != "" && back != "" {
		sc.Pairs = append(sc.Pairs, row("link_bg", sc.Link, back, "--link", "--bg"))
	}
	if accent != "" {
		sc.Pairs = append(sc.Pairs, row("on_accent", sc.OnAccent, accent, "--on-accent", "--accent"))
	}
	return sc
}

// darkPaletteValue returns the dark scheme's value for a token ("" when the
// palette does not set it).
func darkPaletteValue(name string) string {
	for _, v := range darkPalette {
		if v[0] == name {
			return v[1]
		}
	}
	return ""
}

func findPair(rows []contrastRow, key string) *contrastRow {
	for i := range rows {
		if rows[i].Key == key {
			return &rows[i]
		}
	}
	return nil
}
