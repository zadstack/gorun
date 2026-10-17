package content

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// literalKey finds "content.xxx" i18n keys in templates and Go sources.
var literalKey = regexp.MustCompile(`"content\.([a-z0-9_]+(?:\.[a-z0-9_]+)+)"`)

// dynamicKeys are built with printf in templates.
func dynamicKeys() []string {
	var out []string
	for _, st := range Statuses {
		out = append(out, "status."+string(st))
	}
	for _, ft := range FieldTypes {
		out = append(out, "field_type."+string(ft))
	}
	for _, s := range Supports {
		out = append(out, "supports."+s)
	}
	for _, part := range []string{"name", "singular", "plural"} {
		out = append(out, "types."+part+"_en", "types."+part+"_ar")
	}
	return append(out, "nav.type.post", "nav.type.page")
}

// TestCatalogCoversKeys fails when a template or Go file uses a content.*
// key missing from either catalog (t falls back to the key silently).
func TestCatalogCoversKeys(t *testing.T) {
	cats := map[string]map[string]string{}
	for _, loc := range []string{"en", "ar"} {
		raw, err := catalogs.ReadFile("catalog/content." + loc + ".json")
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		cats[loc] = m
	}
	keys := map[string]bool{}
	for _, k := range dynamicKeys() {
		keys[k] = true
	}
	scan := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range literalKey.FindAllStringSubmatch(string(b), -1) {
			if !strings.HasPrefix(m[1], "type.") { // nav ids like content.type.<slug>
				keys[m[1]] = true
			}
		}
	}
	for _, root := range []string{".", "../../web/admin/templates/pages/content", "../../web/admin/templates/partials"} {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".html") || (strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go"))) {
				scan(p)
			}
			return nil
		})
	}
	for k := range keys {
		for loc, m := range cats {
			if m[k] == "" {
				t.Errorf("catalog %s misses %q", loc, k)
			}
		}
	}
	for loc, m := range cats {
		if len(m) != len(cats["en"]) {
			t.Errorf("catalog %s has %d keys, en has %d", loc, len(m), len(cats["en"]))
		}
	}
}
