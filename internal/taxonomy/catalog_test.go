package taxonomy

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var literalKey = regexp.MustCompile(`"taxonomy\.([a-z0-9_]+(?:\.[a-z0-9_]+)+)"`)

// TestCatalogCoversKeys fails when a template or Go file uses a taxonomy.*
// key missing from either catalog.
func TestCatalogCoversKeys(t *testing.T) {
	cats := map[string]map[string]string{}
	for _, loc := range []string{"en", "ar"} {
		raw, err := catalogs.ReadFile("catalog/taxonomy." + loc + ".json")
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		cats[loc] = m
	}
	keys := map[string]bool{"nav": true}
	for _, root := range []string{".", "../../web/admin/templates/pages/taxonomy"} {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(p, "_test.go") || !(strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".html")) {
				return nil
			}
			b, _ := os.ReadFile(p)
			for _, m := range literalKey.FindAllStringSubmatch(string(b), -1) {
				keys[m[1]] = true
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
}
