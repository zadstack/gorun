package pattern

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCatalogCoversKeys(t *testing.T) {
	cats := map[string]map[string]string{}
	for _, loc := range []string{"en", "ar"} {
		raw, err := fs.ReadFile(catalogs, "catalog/pattern."+loc+".json")
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", loc, err)
		}
		cats[loc] = m
	}
	// Match i18n lookups only (not filenames like pattern.json).
	keyRe := regexp.MustCompile(`(?:t\s+"pattern\.([^"]+)"|LabelKey:\s+"pattern\.([^"]+)"|SetFlash\([^,]+,\s*"[^"]+",\s*"pattern\.([^"]+)")`)
	used := map[string]bool{}
	scan := func(path string) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range keyRe.FindAllStringSubmatch(string(raw), -1) {
			for _, g := range m[1:] {
				if g != "" {
					used[g] = true
				}
			}
		}
	}
	goFiles, _ := filepath.Glob("*.go")
	for _, f := range goFiles {
		if !strings.HasSuffix(f, "_test.go") {
			scan(f)
		}
	}
	tpls, _ := filepath.Glob("../../web/admin/templates/pages/appearance/patterns.html")
	if len(tpls) == 0 {
		t.Fatal("no patterns admin template")
	}
	for _, f := range tpls {
		scan(f)
	}
	if len(used) == 0 {
		t.Fatal("no pattern.* keys discovered")
	}
	for k := range used {
		for loc, m := range cats {
			if _, ok := m[k]; !ok {
				t.Errorf("catalog %s lacks key %q", loc, k)
			}
		}
	}
}
