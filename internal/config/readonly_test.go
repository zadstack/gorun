package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyConfigNeverGeneratesSecret(t *testing.T) {
	t.Setenv("GORUN_SECRET_KEY", "")
	t.Setenv("GORUN_ENV", "dev")
	path := filepath.Join(t.TempDir(), "absent")
	if _, e := LoadReadOnly([]string{"--data", path}); e == nil {
		t.Fatal("missing secret accepted")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("read-only load wrote directory", e)
	}
	c, e := Load([]string{"--data", path})
	if e != nil {
		t.Fatal(e)
	}
	again, e := LoadReadOnly([]string{"--data", path})
	if e != nil || string(c.SecretKey) != string(again.SecretKey) {
		t.Fatal("existing dev secret", e)
	}
}
