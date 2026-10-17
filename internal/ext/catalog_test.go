package ext

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCatalogCoversKeys checks that every ext.* key used in Go code and in
// the extension admin templates exists in both catalogs.
func TestCatalogCoversKeys(t *testing.T) {
	cats := map[string]map[string]string{}
	for _, loc := range []string{"en", "ar"} {
		raw, err := fs.ReadFile(catalogs, "catalog/ext."+loc+".json")
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", loc, err)
		}
		cats[loc] = m
	}
	keyRe := regexp.MustCompile(`"ext\.([a-z_]+(?:\.[a-z_]+)+)"`)
	used := map[string]bool{}
	scan := func(path string) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range keyRe.FindAllStringSubmatch(string(raw), -1) {
			used[m[1]] = true
		}
	}
	goFiles, _ := filepath.Glob("*.go")
	for _, f := range goFiles {
		if !strings.HasSuffix(f, "_test.go") {
			scan(f)
		}
	}
	tpls, _ := filepath.Glob("../../web/admin/templates/pages/extensions/*.html")
	if len(tpls) == 0 {
		t.Fatal("no templates found")
	}
	for _, f := range tpls {
		scan(f)
	}
	for k := range used {
		if strings.HasPrefix(k, "deliver.") {
			continue // log event names (ext.deliver.*), not UI copy
		}
		for loc, m := range cats {
			if _, ok := m[k]; !ok {
				t.Errorf("catalog %s lacks key %q", loc, k)
			}
		}
	}
	for _, k := range []string{"kind.action", "kind.filter", "kind.event", "status.installed", "status.active", "status.disabled", "status.error"} {
		if cats["en"][k] == "" || cats["ar"][k] == "" {
			t.Errorf("missing %s", k)
		}
	}
}
