package themekit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PlatformLangPrefix marks keys that resolve from core/module catalogs
// (e.g. {{t "core.app.name"}}), not theme lang files.
const PlatformLangPrefix = "core."

var tKeyRe = regexp.MustCompile(`\{\{\s*t\s+("([^"\\]|\\.)*"|` + "`" + `([^` + "`" + `\\]|\\.)*` + "`" + `)`)

// ExtractTKeys returns unique {{t "…"}} keys referenced in HTML under dir
// (templates/, parts/, components/).
func ExtractTKeys(dir string) ([]string, error) {
	seen := map[string]bool{}
	for _, sub := range []string{"templates", "parts", "components"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, sub, e.Name()))
			if err != nil {
				return nil, err
			}
			for _, m := range tKeyRe.FindAllSubmatch(raw, -1) {
				if len(m) < 2 {
					continue
				}
				key, err := unquoteTLiteral(string(m[1]))
				if err != nil || key == "" {
					continue
				}
				seen[key] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// MergeLang merges lang/<locale>.json along chain (child first; child wins).
func MergeLang(chainDirs []string, locale string) (map[string]string, error) {
	out := map[string]string{}
	for i := len(chainDirs) - 1; i >= 0; i-- {
		path := filepath.Join(chainDirs[i], "lang", locale+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		for k, v := range m {
			out[k] = v
		}
	}
	return out, nil
}

// MissingLangKeys returns used keys absent from catalog, skipping platform keys.
func MissingLangKeys(used []string, catalog map[string]string) []string {
	var missing []string
	for _, k := range used {
		if strings.HasPrefix(k, PlatformLangPrefix) {
			continue
		}
		if _, ok := catalog[k]; !ok {
			missing = append(missing, k)
		}
	}
	return missing
}

// LangKeyWarnings scans the theme's own HTML for {{t}} keys missing from the
// merged en (then ar) catalogs along the parent chain. These are soft checks
// for `gorun theme doctor` and do not affect ValidateDir / catalog CI.
func LangKeyWarnings(dir string, roots []string) ([]Issue, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	chain, err := resolveChain(dir, m, roots)
	if err != nil {
		return nil, err
	}
	used, err := ExtractTKeys(dir)
	if err != nil {
		return nil, err
	}
	if len(used) == 0 {
		return nil, nil
	}
	catalog, err := MergeLang(chain, "en")
	if err != nil {
		return nil, err
	}
	if len(catalog) == 0 {
		catalog, err = MergeLang(chain, "ar")
		if err != nil {
			return nil, err
		}
	}
	var issues []Issue
	for _, k := range MissingLangKeys(used, catalog) {
		issues = append(issues, Issue{
			Code:    "lang_key",
			Message: "templates reference {{t \"" + k + "\"}} but key is absent from theme/parent lang",
			Path:    "lang/",
		})
	}
	return issues, nil
}

func unquoteTLiteral(lit string) (string, error) {
	if len(lit) < 2 {
		return "", errShort
	}
	q := lit[0]
	if (q != '"' && q != '`') || lit[len(lit)-1] != q {
		return "", errShort
	}
	inner := lit[1 : len(lit)-1]
	if q == '`' {
		return inner, nil
	}
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] != '\\' {
			b.WriteByte(inner[i])
			continue
		}
		i++
		if i >= len(inner) {
			return "", errShort
		}
		b.WriteByte(inner[i])
	}
	return b.String(), nil
}

var errShort = errString("short literal")

type errString string

func (e errString) Error() string { return string(e) }
