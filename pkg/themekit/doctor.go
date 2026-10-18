package themekit

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// OptionsForSlug returns CatalogOptions for ship-with themes and a lighter
// activation-oriented check for custom themes (index.html must resolve).
func OptionsForSlug(slug string) Options {
	if slices.Contains(CatalogSlugs, slug) {
		return CatalogOptions()
	}
	return Options{
		RequireTemplates: []string{"index.html"},
	}
}

// DoctorTheme validates one theme directory using OptionsForSlug(slug).
func DoctorTheme(dir string, roots []string) Result {
	slug := filepath.Base(filepath.Clean(dir))
	return ValidateDir(dir, roots, OptionsForSlug(slug))
}

// DoctorRoots validates every theme.json directory under roots (first root
// wins per slug). Catalog slugs use CatalogOptions; others use OptionsForSlug.
func DoctorRoots(roots []string) []Result {
	seen := map[string]bool{}
	var slugs []string
	dirs := map[string]string{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			dir := filepath.Join(root, e.Name())
			if _, err := os.Stat(filepath.Join(dir, "theme.json")); err != nil {
				continue
			}
			seen[e.Name()] = true
			slugs = append(slugs, e.Name())
			dirs[e.Name()] = dir
		}
	}
	sort.Strings(slugs)
	out := make([]Result, 0, len(slugs))
	for _, slug := range slugs {
		out = append(out, DoctorTheme(dirs[slug], roots))
	}
	return out
}

// FormatResult writes a human-readable doctor line for one Result.
func FormatResult(res Result) string {
	var b strings.Builder
	status := "ok"
	if !res.OK {
		status = "FAIL"
	}
	fmt.Fprintf(&b, "%s %s\n", res.Slug, status)
	for _, issue := range res.Issues {
		fmt.Fprintf(&b, "  - %s\n", issue)
	}
	return b.String()
}

// AnyFailed reports whether any result is not OK.
func AnyFailed(results []Result) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}
