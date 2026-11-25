package themekit_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/pkg/themekit"
)

func TestValidateCatalogRootShipWith(t *testing.T) {
	root, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	results := themekit.ValidateCatalogRoot(root)
	if len(results) != len(themekit.CatalogSlugs) {
		t.Fatalf("got %d results, want %d", len(results), len(themekit.CatalogSlugs))
	}
	for _, res := range results {
		if !res.OK {
			t.Errorf("%s: %v", res.Slug, res.Issues)
		}
	}
}

func TestValidateDirReportsMissingLocale(t *testing.T) {
	dir := t.TempDir()
	slug := "child"
	themeDir := filepath.Join(dir, slug)
	if err := os.MkdirAll(filepath.Join(themeDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	parent := "default"
	raw, _ := json.Marshal(map[string]any{
		"name": "Child", "version": "1.0.0", "parent": parent, "supports": []string{"rtl", "identity"},
	})
	if err := os.WriteFile(filepath.Join(themeDir, "theme.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "templates", "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Point roots at real themes so parent resolves, but child still lacks lang.
	themes, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	res := themekit.ValidateDir(themeDir, []string{themes}, themekit.CatalogOptions())
	if res.OK {
		t.Fatal("expected failure without lang files")
	}
	var sawLocale bool
	for _, issue := range res.Issues {
		if issue.Code == "locale" {
			sawLocale = true
			break
		}
	}
	if !sawLocale {
		t.Fatalf("expected locale issue, got %v", res.Issues)
	}
}

// A head that skips {{sections .}} must link the core site stylesheet itself:
// a plain sections.css link alone leaves the shared store and account pages
// without their styling.
func TestHeadWithoutSectionsCallMustLinkTheCoreStylesheet(t *testing.T) {
	for _, tc := range []struct {
		name, head string
		ok         bool
	}{
		{"sections call", `<head>{{sections .}}{{builderIdentity}}{{identity .}}</head>`, true},
		{"plain sections link only", `<head><link rel="stylesheet" href="{{asset "sections.css"}}">{{builderIdentity}}{{identity .}}</head>`, false},
		{"plain links to both", `<head><link rel="stylesheet" href="/static/site/core.css"><link rel="stylesheet" href="{{asset "sections.css"}}">{{builderIdentity}}{{identity .}}</head>`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "solo")
			for name, body := range map[string]string{
				"theme.json":           `{"name":"Solo","version":"1.0.0","supports":["rtl","identity"]}`,
				"templates/index.html": "x",
				"parts/head.html":      tc.head,
			} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			res := themekit.ValidateDir(dir, []string{filepath.Dir(dir)}, themekit.CatalogOptions())
			var sectionsIssue bool
			for _, issue := range res.Issues {
				if issue.Code == "head" && strings.Contains(issue.Message, "sections") {
					sectionsIssue = true
				}
			}
			if sectionsIssue == tc.ok {
				t.Fatalf("sections head issue = %v, want %v: %v", sectionsIssue, !tc.ok, res.Issues)
			}
		})
	}
}

// A theme sets the identity tokens (--accent, --bg, …) and never the shadcn
// role names the shared tokens alias to them: a direct --primary would stop
// following --accent and the site identity.
func TestRoleTokenOverridesAreRejected(t *testing.T) {
	themes, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, css string
		ok        bool
	}{
		{"source token", `:where(html[data-theme="child"]){--accent:#0f766e;--radius:8px}`, true},
		{"reference", `:where(html[data-theme="child"]){--accent:#0f766e}.x{color:var(--primary);border-color:var(--border)}`, true},
		{"role declared", `:where(html[data-theme="child"]){--primary:#0f766e}`, false},
		{"role declared later", `:where(html[data-theme="child"]){--accent:#0f766e}
.btn{--border: red}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"name":"Child","version":"1.0","parent":"default"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "assets", "site.css"), []byte(tc.css), 0o644); err != nil {
				t.Fatal(err)
			}
			res := themekit.ValidateDir(dir, []string{themes}, themekit.Options{ForbidRoleTokenOverrides: true})
			var saw []string
			for _, issue := range res.Issues {
				if issue.Code == "tokens" {
					saw = append(saw, issue.Message)
				}
			}
			if tc.ok && len(saw) > 0 {
				t.Fatalf("unexpected tokens issues: %v", saw)
			}
			if !tc.ok && len(saw) != 1 {
				t.Fatalf("want one tokens issue, got %v (all: %v)", saw, res.Issues)
			}
			if !tc.ok && tc.name == "role declared" && !strings.Contains(saw[0], "--primary is an alias of --accent") {
				t.Fatalf("message should name the source token: %q", saw[0])
			}
		})
	}
}

// The shared tokens file declares every role name as var(<source>), so the
// table doctor reports from and the CSS themes load cannot drift apart.
func TestRoleTokensDeclaredInSharedTokens(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "themes", "_base", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, tok := range themekit.RoleTokens {
		want := tok.Name + ": "
		i := strings.Index(css, want)
		if i < 0 {
			t.Errorf("%s not declared in themes/_base/tokens.css", tok.Name)
			continue
		}
		decl := css[i : i+strings.Index(css[i:], ";")]
		if !strings.Contains(decl, "var("+tok.Source+")") {
			t.Errorf("%s should follow %s, got %q", tok.Name, tok.Source, decl)
		}
	}
}

// The shared tokens file declares every step of the type scale and its line
// heights, so a theme or the admin can write var(--fs-*) on any page.
func TestTypeScaleTokensDeclaredInSharedTokens(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "themes", "_base", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for _, tok := range themekit.TypeScaleTokens {
		if !strings.Contains(css, "\n  "+tok+": ") {
			t.Errorf("%s not declared in themes/_base/tokens.css", tok)
		}
	}
	for _, fluid := range []string{"--fs-lg", "--fs-xl", "--fs-2xl", "--fs-3xl", "--fs-4xl", "--fs-5xl", "--fs-display"} {
		i := strings.Index(css, "\n  "+fluid+": ")
		if i < 0 {
			continue
		}
		decl := css[i : i+strings.Index(css[i:], ";")]
		if !strings.Contains(decl, "clamp(") || !strings.Contains(decl, "vw") {
			t.Errorf("%s should be fluid (clamp with a vw term), got %q", fluid, strings.TrimSpace(decl))
		}
	}
	if !strings.Contains(css, "--fs-base: var(--body-size);") {
		t.Error("--fs-base should follow the theme's --body-size")
	}
}
