package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddonConfigurationPrecedenceAndValidation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_DATA", dir)
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_ADDONS", `{"example":"disabled"}`)
	if err := os.WriteFile(filepath.Join(dir, "gorun.toml"), []byte("[addons]\nexample = 'enabled'\nother = 'auto'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addons["example"] != "disabled" || cfg.Addons["other"] != "auto" {
		t.Fatalf("incorrect addon config: %v", cfg.Addons)
	}
	for _, raw := range []string{`{"example":"yes"}`, `["example"]`, `{"":"auto"}`} {
		t.Setenv("GORUN_ADDONS", raw)
		if _, err := Load(nil); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
