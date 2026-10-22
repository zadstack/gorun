package theme

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Theme layout on disk (spec §4.3):
//
//	<themes>/<slug>/theme.json
//	<themes>/<slug>/templates/*.html    hierarchy files, named "single.html"
//	<themes>/<slug>/parts/*.html        named "parts.header.html"
//	<themes>/<slug>/components/*.html   named "components.card.html"
//	<themes>/<slug>/assets/**           served at /themes/<slug>/assets/
//	<themes>/<slug>/lang/<locale>.json  optional flat {"key":"text"} strings for t
//
// Template names are slash-free so they fit one URL path segment
// (/api/v1/templates/{name}); the directory is encoded as a prefix.

const (
	themeFile    = "theme.json"
	maxThemeLink = 5 // child → parent chain length limit
)

var (
	// ErrNotFound is returned for unknown themes and templates.
	ErrNotFound = errors.New("theme: not found")
	// ErrInvalidName is returned for template names outside the naming scheme.
	ErrInvalidName = errors.New("theme: invalid template name")
	// ErrInvalidTheme is returned by Activate for a broken theme.
	ErrInvalidTheme = errors.New("theme: invalid theme")
	// ErrInvalidTemplate wraps a template parse error on save.
	ErrInvalidTemplate = errors.New("theme: invalid template")

	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	nameRe = regexp.MustCompile(`^(?:(parts|components)\.)?([\p{L}\p{N}][\p{L}\p{N}_-]{0,127})\.html$`)
)

// templateDirs maps a name prefix to its directory inside a theme.
var templateDirs = []struct{ prefix, dir string }{{"", "templates"}, {"parts.", "parts"}, {"components.", "components"}}

// ValidName reports whether name follows the template naming scheme.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// relPath maps a template name to its path inside a theme or override dir
// ("parts.header.html" → "parts/header.html").
func relPath(name string) (string, error) {
	m := nameRe.FindStringSubmatch(name)
	if m == nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	dir := "templates"
	if m[1] != "" {
		dir = m[1]
	}
	return filepath.Join(dir, m[2]+".html"), nil
}

// loadInfo reads <root>/<slug>/theme.json.
func loadInfo(root, slug string) (Info, error) {
	if !slugRe.MatchString(slug) {
		return Info{}, fmt.Errorf("%w: theme %q", ErrNotFound, slug)
	}
	dir := filepath.Join(root, slug)
	raw, err := os.ReadFile(filepath.Join(dir, themeFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Info{}, fmt.Errorf("%w: theme %q", ErrNotFound, slug)
	}
	if err != nil {
		return Info{}, fmt.Errorf("theme: read %s: %w", slug, err)
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return Info{}, fmt.Errorf("%w: %s/theme.json: %v", ErrInvalidTheme, slug, err)
	}
	for variant, path := range info.Logos {
		if !ValidAssetPath(path) || (variant != "primary" && variant != "dark" && variant != "light" && variant != "icon") {
			return Info{}, fmt.Errorf("%w: %s/theme.json: logos.%s must name a file under assets/", ErrInvalidTheme, slug, variant)
		}
	}
	if strings.TrimSpace(info.Name) == "" {
		return Info{}, fmt.Errorf("%w: %s/theme.json: name is required", ErrInvalidTheme, slug)
	}
	info.Slug = slug
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	info.Dir = dir
	return info, nil
}

// findInfo reads slug from the first of roots that has it: the core
// directory comes first, so an extra directory never shadows a core theme.
func findInfo(roots []string, slug string) (Info, error) {
	info, err := findExactInfo(roots, slug)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return info, err
	}
	if replacement := canonicalSlug(slug); replacement != slug {
		return findExactInfo(roots, replacement)
	}
	return info, err
}

// An installed legacy theme is authoritative, including when it lives in an
// extra root. Only a missing slug may fall back to its catalog replacement.
func findExactInfo(roots []string, slug string) (Info, error) {
	err := fmt.Errorf("%w: theme %q", ErrNotFound, slug)
	for _, root := range roots {
		info, e := loadInfo(root, slug)
		if e == nil || !errors.Is(e, ErrNotFound) {
			return info, e
		}
	}
	return Info{}, err
}

// loadChain returns the theme and its ancestors, child first; each may live
// in any of roots.
func loadChain(roots []string, slug string) ([]Info, error) {
	var chain []Info
	seen := map[string]bool{}
	for cur := slug; cur != ""; {
		if seen[cur] {
			return nil, fmt.Errorf("%w: parent cycle at %q", ErrInvalidTheme, cur)
		}
		if len(chain) == maxThemeLink {
			return nil, fmt.Errorf("%w: parent chain longer than %d", ErrInvalidTheme, maxThemeLink)
		}
		seen[cur] = true
		info, err := findInfo(roots, cur)
		if err != nil {
			if len(chain) > 0 && errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("%w: parent theme %q of %q is not installed", ErrInvalidTheme, cur, chain[len(chain)-1].Slug)
			}
			return nil, err
		}
		chain = append(chain, info)
		cur = info.Parent
	}
	return chain, nil
}

// listThemes returns every directory under roots that holds a theme.json,
// sorted by slug; the first root with a slug wins. Broken theme.json files
// and missing roots are skipped.
func listThemes(roots []string) ([]Info, error) {
	var out []Info
	seen := map[string]bool{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("theme: list %s: %w", root, err)
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			if info, err := loadInfo(root, e.Name()); err == nil {
				seen[e.Name()] = true
				out = append(out, info)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// layer is one directory searched for templates; Origin becomes
// TemplateFile.Source ("draft", "override", "theme" or "parent").
type layer struct{ dir, origin string }

// source is the winning file for a template name.
type source struct{ name, path, origin string }

// collect walks the layers (highest priority first) and returns, per
// template name, the first file found.
func collect(layers []layer) map[string]source {
	out := map[string]source{}
	for _, l := range layers {
		for _, td := range templateDirs {
			entries, err := os.ReadDir(filepath.Join(l.dir, td.dir))
			if err != nil {
				continue
			}
			for _, e := range entries {
				name := td.prefix + e.Name()
				if e.IsDir() || !ValidName(name) {
					continue
				}
				if _, taken := out[name]; !taken {
					out[name] = source{name: name, path: filepath.Join(l.dir, td.dir, e.Name()), origin: l.origin}
				}
			}
		}
	}
	return out
}

// loadLang merges <dir>/lang/<locale>.json of every theme in the chain;
// the child wins over its parents.
func loadLang(chain []Info) map[string]map[string]string {
	out := map[string]map[string]string{}
	for i := len(chain) - 1; i >= 0; i-- {
		mergeLangDir(out, chain[i].Dir)
	}
	return out
}

// mergeLangDir overlays <dir>/lang/<locale>.json onto out (later keys win).
func mergeLangDir(out map[string]map[string]string, dir string) {
	if dir == "" {
		return
	}
	files, _ := filepath.Glob(filepath.Join(dir, "lang", "*.json"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var msgs map[string]string
		if json.Unmarshal(raw, &msgs) != nil {
			continue
		}
		loc := strings.TrimSuffix(filepath.Base(f), ".json")
		if out[loc] == nil {
			out[loc] = map[string]string{}
		}
		for k, v := range msgs {
			out[loc][k] = v
		}
	}
}

var assetPathRe = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*\.[a-z0-9]{1,5}$`)

// ValidAssetPath accepts a relative file path under a theme's assets/
// directory (no traversal, no absolute path, no query).
func ValidAssetPath(p string) bool { return assetPathRe.MatchString(p) }
