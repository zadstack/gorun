package wpmigrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCatalogCoversTemplates checks every static wpmigrate.* key used by the
// templates and the code exists in both catalogs, and that both catalogs
// have the same keys.
func TestCatalogCoversTemplates(t *testing.T) {
	load := func(loc string) map[string]string {
		b, err := os.ReadFile("catalog/wpmigrate." + loc + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	en, ar := load("en"), load("ar")
	for k := range en {
		if _, ok := ar[k]; !ok {
			t.Errorf("ar lacks %s", k)
		}
	}
	for k := range ar {
		if _, ok := en[k]; !ok {
			t.Errorf("en lacks %s", k)
		}
	}
	re := regexp.MustCompile(`"wpmigrate\.([a-z_.]+[a-z_])"`)
	files, _ := filepath.Glob("../../web/admin/templates/pages/migrations/*.html")
	gofiles, _ := filepath.Glob("*.go")
	for _, f := range append(files, gofiles...) {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			key := m[1]
			switch {
			case strings.HasPrefix(key, "product_importer"), strings.HasPrefix(key, "source_create"), key == "source_update", key == "options_reset", key == "create", key == "discover",
				key == "start", key == "pause", key == "resume", key == "retry", key == "finish", key == "finished", key == "map",
				key == "preview", key == "validate", key == "settings", key == "watchdog", key == "rate_per_second":
				continue // audit actions / service names, not UI copy
			}
			if _, ok := en[key]; !ok {
				t.Errorf("%s uses wpmigrate.%s, missing from catalogs", filepath.Base(f), key)
			}
		}
	}
	for _, dyn := range []string{"status.draft", "status.validated", "kind.rewrite", "step.validate", "runstep.validate",
		"target.commerce.product", "policy.suffix", "conflict.duplicate_in_source", "action.needs_commerce_module", "auth.wxr", "validate.scope.stored_legacy_report", "validate.limitation.legacy_report_checks_not_recorded", "check.kind.report", "check.reason.legacy_report_checks_not_recorded"} {
		if en[dyn] == "" {
			t.Errorf("dynamic key %s missing", dyn)
		}
	}
}
