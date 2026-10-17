package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoRunCanonicalConfiguration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_DATA", dir)
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_ADDR", "127.0.0.1:9123")
	t.Setenv("GORUN_THEME", "studio-business")
	if err := os.WriteFile(filepath.Join(dir, "gorun.toml"), []byte("theme = \"default\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != dir || cfg.Addr != "127.0.0.1:9123" || cfg.Theme != "studio-business" || cfg.DBPath() != filepath.Join(dir, "gorun.db") {
		t.Fatal("canonical GoRun config paths/environment not applied")
	}
	cfg, err = Load([]string{"--theme", "studio-editorial"})
	if err != nil || cfg.Theme != "studio-editorial" {
		t.Fatal("flags must override GoRun environment", err)
	}
}
