package themekit_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zadstack/gorun/pkg/themekit"
)

func TestExtractTKeysAndMissing(t *testing.T) {
	dir := t.TempDir()
	mustMk(t, filepath.Join(dir, "templates", "index.html"), `{{t "hello"}} {{t "core.app.name"}} {{part "header" .}}`)
	mustMk(t, filepath.Join(dir, "parts", "header.html"), `{{t "menu"}}`)
	keys, err := themekit.ExtractTKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"core.app.name", "hello", "menu"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("got %v want %v", keys, want)
	}
	missing := themekit.MissingLangKeys(keys, map[string]string{"menu": "Menu"})
	if !reflect.DeepEqual(missing, []string{"hello"}) {
		t.Fatalf("missing %v", missing)
	}
}

func mustMk(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
