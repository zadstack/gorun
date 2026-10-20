package patternkit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateDirOK(t *testing.T) {
	dir := t.TempDir()
	slug := "editorial"
	root := filepath.Join(dir, slug)
	mustWrite(t, filepath.Join(root, ManifestFile), `{
  "kind":"pattern","slug":"editorial","version":"1.0.0",
  "name":{"en":"Editorial","ar":"تحريري"},
  "description":{"en":"Magazine home","ar":"رئيسية مجلة"}
}`)
	mustWrite(t, filepath.Join(root, "templates", "front-page.html"), `{{part "header" .}}ok{{part "footer" .}}`)
	mustWrite(t, filepath.Join(root, "lang", "en.json"), `{"pat.ok":"OK"}`)
	mustWrite(t, filepath.Join(root, "lang", "ar.json"), `{"pat.ok":"حسنا"}`)
	if issues := ValidateDir(root); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

func TestValidateDirRejectsIdentityTokens(t *testing.T) {
	dir := t.TempDir()
	slug := "bad"
	root := filepath.Join(dir, slug)
	mustWrite(t, filepath.Join(root, ManifestFile), `{
  "kind":"pattern","slug":"bad","version":"1.0.0",
  "name":{"en":"Bad"},"description":{"en":"x"}
}`)
	mustWrite(t, filepath.Join(root, "templates", "front-page.html"), `x`)
	mustWrite(t, filepath.Join(root, "lang", "en.json"), `{}`)
	mustWrite(t, filepath.Join(root, "lang", "ar.json"), `{}`)
	mustWrite(t, filepath.Join(root, "assets", "bad.css"), `:where(html[data-theme="bad"]){--accent:#f00}`)
	issues := ValidateDir(root)
	found := false
	for _, i := range issues {
		if i.Code == "identity_tokens" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want identity_tokens, got %v", issues)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
