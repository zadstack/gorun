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
