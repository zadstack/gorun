package admin_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/web"
)

func loadCatalog(t *testing.T, locale string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("catalog", "admin."+locale+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

var (
	tmplKeyRe   = regexp.MustCompile(`\bt "admin\.([a-z_.]+)"`)
	goKeyRe     = regexp.MustCompile(`"admin\.((?:nav|common|locale|dashboard|users|settings|mail|health|jobs|keys|audit|search|api|ai)\.[a-z_.]+)"`)
	dynamicKeys = map[string][]string{ // printf "admin.<prefix>%s" expansions
		"audit.kind_":   {"user", "api_key", "system", "extension"},
		"jobs.status_":  {"queued", "retrying", "running", "dead", "done"},
		"locale.":       {"ar", "en"},
		"mail.tls_":     {"starttls", "tls", "none"},
		"users.status_": {"active", "disabled"},
	}
)

// TestCatalogCoversAllKeys checks ar/en parity and that every admin key
// used by the admin templates and Go code exists.
func TestCatalogCoversAllKeys(t *testing.T) {
	en, ar := loadCatalog(t, "en"), loadCatalog(t, "ar")
	for k := range en {
		if _, ok := ar[k]; !ok {
			t.Errorf("ar missing %s", k)
		}
	}
	for k := range ar {
		if _, ok := en[k]; !ok {
			t.Errorf("en missing %s", k)
		}
	}
	used := map[string]bool{}
	owned := []string{"admin/templates/pages/dashboard.html", "admin/templates/pages/users", "admin/templates/pages/settings/general.html",
		"admin/templates/pages/settings/mail.html", "admin/templates/pages/settings/ai.html", "admin/templates/pages/system", "admin/templates/pages/agents", "admin/templates/partials/dashboard_widget.html"}
	for _, root := range owned {
		_ = fs.WalkDir(web.FS, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, _ := fs.ReadFile(web.FS, p)
			for _, m := range tmplKeyRe.FindAllStringSubmatch(string(b), -1) {
				used[m[1]] = true
			}
			return nil
		})
	}
	gofiles, _ := filepath.Glob("*.go")
	for _, f := range gofiles {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range goKeyRe.FindAllStringSubmatch(string(b), -1) {
			used[m[1]] = true
		}
	}
	for prefix, vals := range dynamicKeys {
		for _, v := range vals {
			used[prefix+v] = true
		}
	}
	if len(used) < 100 {
		t.Fatalf("suspiciously few keys found: %d", len(used))
	}
	var missing []string
	for k := range used {
		if _, ok := en[k]; !ok {
			missing = append(missing, k)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("catalog missing keys: %v", missing)
	}
}
