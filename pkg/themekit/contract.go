// Package themekit is the shared catalog theme contract: validation helpers
// for ship-with and custom themes. The theme doctor CLI and token helpers can
// grow here without pulling theme markup into internal/.
//
// Catalog themes under themes/ must pass ValidateDir with CatalogOptions.
// Child themes that inherit from default may omit files the parent already
// provides; ValidateDir walks the parent chain.
package themekit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// CatalogSlugs are the five themes GoRun ships in themes/.
var CatalogSlugs = []string{"default", "business", "magazine", "portfolio", "docs"}

// RequiredTemplates must resolve for a catalog theme (own file or inherited).
var RequiredTemplates = []string{
	"index.html",
	"page.html",
	"single.html",
	"archive.html",
	"taxonomy.html",
	"search.html",
	"404.html",
	"500.html",
	"page-builder.html",
	"page-contact.html",
	"form-result.html",
}

// RequiredLocales are lang/<locale>.json files every catalog theme must own
// (parent merge is not enough for the ship-with quality bar).
var RequiredLocales = []string{"en", "ar"}

// RequiredSupports must appear in theme.json "supports".
var RequiredSupports = []string{"rtl", "identity"}

// Options configure ValidateDir. CatalogOptions is the ship-with bar.
type Options struct {
	// RequireLocales requires lang/<locale>.json on the theme itself.
	RequireLocales []string
	// RequireSupports lists theme.json supports values.
	RequireSupports []string
	// RequireTemplates lists templates that must resolve via inheritance.
	RequireTemplates []string
	// RequireScreenshot requires screenshot.png at the theme root.
	RequireScreenshot bool
	// RequireHome requires front-page.html or home.html (resolved).
	RequireHome bool
	// ForbidScriptHosts fails when theme HTML/CSS references these substrings
	// (public CSP: no third-party script CDNs). Empty skips the check.
	ForbidScriptHosts []string
	// RequireTokenSelector requires the theme's primary stylesheet to declare
	// tokens under :where(html[data-theme="<slug>"]).
	RequireTokenSelector bool
}

// CatalogOptions is the shared ship-with contract.
func CatalogOptions() Options {
	return Options{
		RequireLocales:       append([]string(nil), RequiredLocales...),
		RequireSupports:      append([]string(nil), RequiredSupports...),
		RequireTemplates:     append([]string(nil), RequiredTemplates...),
		RequireScreenshot:    true,
		RequireHome:          true,
		ForbidScriptHosts:    []string{"cdn.tailwindcss.com", "cdn.jsdelivr.net", "unpkg.com", "cdnjs.cloudflare.com"},
		RequireTokenSelector: true,
	}
}

// Issue is one contract failure.
type Issue struct {
	Path    string `json:"path,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (i Issue) String() string {
	if i.Path == "" {
		return fmt.Sprintf("%s: %s", i.Code, i.Message)
	}
	return fmt.Sprintf("%s: %s (%s)", i.Code, i.Message, i.Path)
}

// Result is the outcome of ValidateDir.
type Result struct {
	Slug   string  `json:"slug"`
	OK     bool    `json:"ok"`
	Issues []Issue `json:"issues,omitempty"`
}

type manifest struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Parent   *string  `json:"parent"`
	Supports []string `json:"supports"`
}

// ValidateDir checks one theme directory against opts. roots are searched for
// parents (same order as GORUN_THEMES). slug is the directory basename when
// empty.
func ValidateDir(dir string, roots []string, opts Options) Result {
	dir = filepath.Clean(dir)
	slug := filepath.Base(dir)
	res := Result{Slug: slug}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		res.Issues = append(res.Issues, Issue{Code: "missing_dir", Message: "theme directory not found", Path: dir})
		return res
	}
	raw, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		res.Issues = append(res.Issues, Issue{Code: "theme_json", Message: "theme.json is required", Path: "theme.json"})
		return finish(res)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		res.Issues = append(res.Issues, Issue{Code: "theme_json", Message: "theme.json is not valid JSON: " + err.Error(), Path: "theme.json"})
		return finish(res)
	}
	if strings.TrimSpace(m.Name) == "" {
		res.Issues = append(res.Issues, Issue{Code: "theme_json", Message: "name is required", Path: "theme.json"})
	}
	if opts.RequireScreenshot {
		if _, err := os.Stat(filepath.Join(dir, "screenshot.png")); err != nil {
			res.Issues = append(res.Issues, Issue{Code: "screenshot", Message: "screenshot.png is required", Path: "screenshot.png"})
		}
	}
	for _, loc := range opts.RequireLocales {
		rel := filepath.Join("lang", loc+".json")
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			res.Issues = append(res.Issues, Issue{Code: "locale", Message: "lang/" + loc + ".json is required on the theme itself", Path: rel})
		} else if err := checkLangJSON(filepath.Join(dir, rel)); err != nil {
			res.Issues = append(res.Issues, Issue{Code: "locale", Message: err.Error(), Path: rel})
		}
	}
	for _, want := range opts.RequireSupports {
		if !slices.Contains(m.Supports, want) {
			res.Issues = append(res.Issues, Issue{Code: "supports", Message: "supports must include " + want, Path: "theme.json"})
		}
	}
	chain, err := resolveChain(dir, m, roots)
	if err != nil {
		res.Issues = append(res.Issues, Issue{Code: "parent", Message: err.Error(), Path: "theme.json"})
		return finish(res)
	}
	for _, name := range opts.RequireTemplates {
		if !resolves(chain, "templates", name) {
			res.Issues = append(res.Issues, Issue{Code: "template", Message: "required template missing (own or inherited)", Path: "templates/" + name})
		}
	}
	if opts.RequireHome {
		if !resolves(chain, "templates", "front-page.html") && !resolves(chain, "templates", "home.html") {
			res.Issues = append(res.Issues, Issue{Code: "template", Message: "front-page.html or home.html is required", Path: "templates/"})
		}
	}
	if opts.RequireTokenSelector {
		if issue := checkTokenSelector(dir, slug); issue != nil {
			res.Issues = append(res.Issues, *issue)
		}
	}
	if len(opts.ForbidScriptHosts) > 0 {
		res.Issues = append(res.Issues, scanForbiddenHosts(dir, opts.ForbidScriptHosts)...)
	}
	// {{sections .}} links the core site stylesheet (forms, tables, the shared
	// store and account templates) on every page and sections.css only on
	// pages whose content uses it. A theme whose own templates use gp-s-* may
	// keep a plain sections.css link instead, but then links the core
	// stylesheet itself, or the shared functional pages lose their styling.
	if !headMatches(chain, sectionsCall) && !(headProvides(chain, "sections.css") && headProvides(chain, CoreStylesheetPath)) {
		res.Issues = append(res.Issues, Issue{Code: "head", Message: "head must call {{sections .}}, or load both sections.css and " + CoreStylesheetPath + " (own or inherited)", Path: "parts/head.html"})
	}
	if !headProvides(chain, "builderIdentity") && !headProvides(chain, "builderStyle") {
		res.Issues = append(res.Issues, Issue{Code: "head", Message: "head must call builderIdentity or builderStyle (own or inherited)", Path: "parts/head.html"})
	}
	if !headProvides(chain, "identity") {
		res.Issues = append(res.Issues, Issue{Code: "head", Message: "head must call identity (own or inherited)", Path: "parts/head.html"})
	}
	return finish(res)
}

// ValidateCatalogRoot validates every CatalogSlug under root.
func ValidateCatalogRoot(root string) []Result {
	opts := CatalogOptions()
	roots := []string{root}
	out := make([]Result, 0, len(CatalogSlugs))
	for _, slug := range CatalogSlugs {
		out = append(out, ValidateDir(filepath.Join(root, slug), roots, opts))
	}
	return out
}

func finish(res Result) Result {
	res.OK = len(res.Issues) == 0
	return res
}

func checkLangJSON(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("invalid JSON object of string values: %w", err)
	}
	if len(m) == 0 {
		return fmt.Errorf("must contain at least one key")
	}
	return nil
}

func resolveChain(dir string, m manifest, roots []string) ([]string, error) {
	chain := []string{dir}
	seen := map[string]bool{filepath.Base(dir): true}
	parent := ""
	if m.Parent != nil {
		parent = strings.TrimSpace(*m.Parent)
	}
	for parent != "" {
		if seen[parent] {
			return nil, fmt.Errorf("parent cycle at %q", parent)
		}
		seen[parent] = true
		next, err := findThemeDir(parent, roots)
		if err != nil {
			return nil, err
		}
		chain = append(chain, next)
		raw, err := os.ReadFile(filepath.Join(next, "theme.json"))
		if err != nil {
			return nil, fmt.Errorf("parent %q: %w", parent, err)
		}
		var pm manifest
		if err := json.Unmarshal(raw, &pm); err != nil {
			return nil, fmt.Errorf("parent %q theme.json: %w", parent, err)
		}
		if pm.Parent == nil {
			parent = ""
		} else {
			parent = strings.TrimSpace(*pm.Parent)
		}
	}
	return chain, nil
}

func findThemeDir(slug string, roots []string) (string, error) {
	for _, root := range roots {
		p := filepath.Join(root, slug)
		if st, err := os.Stat(filepath.Join(p, "theme.json")); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("parent theme %q not found", slug)
}

func resolves(chain []string, kind, name string) bool {
	rel := filepath.Join(kind, name)
	for _, dir := range chain {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return true
		}
	}
	return false
}

// CoreStylesheetPath is the core site stylesheet GoRun serves from the binary
// (theme.CoreCSSPath); {{sections .}} links it with a content hash.
const CoreStylesheetPath = "/static/site/core.css"

// sectionsCall matches the {{sections .}} head func, with any spacing.
var sectionsCall = regexp.MustCompile(`\{\{-?\s*sections\b`)

func headMatches(chain []string, re *regexp.Regexp) bool {
	for _, dir := range chain {
		for _, name := range []string{"parts/head.html", "parts/head-extra.html"} {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil && re.Match(raw) {
				return true
			}
		}
	}
	return false
}

func headProvides(chain []string, needle string) bool {
	for _, dir := range chain {
		for _, name := range []string{"parts/head.html", "parts/head-extra.html"} {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			if strings.Contains(string(raw), needle) {
				return true
			}
		}
	}
	return false
}

func checkTokenSelector(dir, slug string) *Issue {
	candidates := []string{
		filepath.Join("assets", "site.css"),
		filepath.Join("assets", "theme.css"),
		filepath.Join("assets", "style.css"),
	}
	want := `:where(html[data-theme="` + slug + `"])`
	var sawCSS bool
	for _, rel := range candidates {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		sawCSS = true
		if strings.Contains(string(raw), want) {
			return nil
		}
		// default also scopes some rules as html[data-theme="default"] without :where;
		// accept that form for the reference theme only.
		if slug == "default" && strings.Contains(string(raw), `html[data-theme="default"]`) {
			return nil
		}
	}
	if !sawCSS {
		// Child that only overrides markup may inherit tokens from parent.
		return nil
	}
	return &Issue{Code: "tokens", Message: "primary stylesheet must declare tokens under " + want, Path: "assets/"}
}

func scanForbiddenHosts(dir string, hosts []string) []Issue {
	var issues []Issue
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".html" && ext != ".css" && ext != ".js" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		body := string(raw)
		rel, _ := filepath.Rel(dir, path)
		for _, host := range hosts {
			if strings.Contains(body, host) {
				issues = append(issues, Issue{Code: "forbidden_host", Message: "must not reference " + host, Path: rel})
			}
		}
		return nil
	})
	return issues
}
