package theme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeReparentTheme(t *testing.T, root, slug, parent string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, slug)
	p := `null`
	if parent != "" {
		p = `"` + parent + `"`
	}
	files["theme.json"] = "{\n  \"name\": \"" + slug + "\",\n  \"version\": \"1.0.0\",\n  \"parent\": " + p + ",\n  \"supports\": [\"rtl\"]\n}\n"
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func reparentFixture(t *testing.T) string {
	root := t.TempDir()
	writeReparentTheme(t, root, "default", "", map[string]string{"templates/index.html": "i", "templates/single.html": "s", "parts/header.html": "h", "parts/footer.html": "f"})
	writeReparentTheme(t, root, "old", "default", map[string]string{"templates/single.html": "old single", "components/card.html": "c"})
	writeReparentTheme(t, root, "fresh", "default", map[string]string{"templates/archive.html": "a"})
	writeReparentTheme(t, root, "brand", "old", map[string]string{"templates/front-page.html": `{{part "header" .}}{{component "card" .}}{{part "footer" .}}`})
	return root
}

func TestReparentReportsWithoutChanging(t *testing.T) {
	root := reparentFixture(t)
	before, _ := os.ReadFile(filepath.Join(root, "brand", "theme.json"))
	rep, err := Reparent([]string{root}, "brand", "fresh", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Applied || rep.OldParent != "old" {
		t.Fatalf("report: %+v", rep)
	}
	changed := map[string]InheritedSource{}
	for _, c := range rep.Changed {
		changed[c.Name] = c
	}
	if c := changed["templates/single.html"]; c.Before != "old" || c.After != "default" {
		t.Errorf("single.html source change not reported: %+v", rep.Changed)
	}
	if c := changed["components/card.html"]; c.Before != "old" || c.After != "" {
		t.Errorf("card.html must be reported as lost: %+v", rep.Changed)
	}
	if len(rep.Missing) != 1 || rep.Missing[0] != "components/card.html" {
		t.Errorf("missing references: %v", rep.Missing)
	}
	after, _ := os.ReadFile(filepath.Join(root, "brand", "theme.json"))
	if string(before) != string(after) {
		t.Error("a report must not touch theme.json")
	}
}

func TestReparentApplyRefusesUnresolvedReferences(t *testing.T) {
	root := reparentFixture(t)
	if _, err := Reparent([]string{root}, "brand", "fresh", true, time.Now()); !errors.Is(err, ErrInvalidTheme) {
		t.Fatalf("apply with missing references must fail, got %v", err)
	}
}

func TestReparentApplyBacksUpAndRewritesParentOnly(t *testing.T) {
	root := reparentFixture(t)
	writeReparentTheme(t, root, "brand2", "old", map[string]string{"templates/front-page.html": `{{part "header" .}}`})
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	rep, err := Reparent([]string{root}, "brand2", "fresh", true, now)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || !strings.HasSuffix(rep.BackupPath, "theme.json.bak-20261016T120000Z") {
		t.Fatalf("report: %+v", rep)
	}
	got, _ := os.ReadFile(filepath.Join(root, "brand2", "theme.json"))
	if !strings.Contains(string(got), `"parent": "fresh",`) || !strings.Contains(string(got), `"supports": ["rtl"]`) {
		t.Fatalf("theme.json must keep its layout with the new parent:\n%s", got)
	}
	old, _ := os.ReadFile(rep.BackupPath)
	if !strings.Contains(string(old), `"parent": "old"`) {
		t.Fatalf("backup must hold the previous file:\n%s", old)
	}
	info, err := findInfo([]string{root}, "brand2")
	if err != nil || info.Parent != "fresh" {
		t.Fatalf("reload: %+v %v", info, err)
	}
}

func TestReparentRejectsCycles(t *testing.T) {
	root := reparentFixture(t)
	if _, err := Reparent([]string{root}, "old", "brand", false, time.Now()); !errors.Is(err, ErrInvalidTheme) {
		t.Fatalf("parenting a theme under its own child must fail, got %v", err)
	}
	if _, err := Reparent([]string{root}, "brand", "brand", false, time.Now()); !errors.Is(err, ErrInvalidTheme) {
		t.Fatalf("self parent must fail, got %v", err)
	}
}

// setParent rewrites only the top-level "parent": a nested "parent" key
// (here in a setting's default, placed first) and the layout stay as they are.
func TestSetParentRewritesOnlyTheTopLevelKey(t *testing.T) {
	raw := "{\n  \"name\": \"brand\",\n  \"settings\": [{\"key\": \"parent\", \"default\": {\"parent\": \"keep-me\"}, \"label\": {\"en\": \"\\\"parent\\\": \\\"x\\\"\"}}],\n  \"parent\" :  \"old\",\n  \"supports\": [\"rtl\"]\n}\n"
	got, ok := setParent([]byte(raw), "fresh")
	if !ok {
		t.Fatal("setParent failed")
	}
	want := strings.Replace(raw, `"parent" :  "old"`, `"parent" :  "fresh"`, 1)
	if string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if got, ok := setParent([]byte(`{"name":"x","parent":null}`), `a"b`); !ok || string(got) != `{"name":"x","parent":"a\"b"}` {
		t.Fatalf("null parent / escaping: %q %v", got, ok)
	}
	for _, bad := range []string{
		`{"name":"x","settings":[{"parent":"nested only"}]}`, // no top-level parent
		`{"name":"x","parent":{"slug":"y"}}`,                 // not a string
		`{"name":"x","parent":"y"`,                           // invalid JSON
		`["parent","y"]`,                                     // not an object
	} {
		if _, ok := setParent([]byte(bad), "z"); ok {
			t.Errorf("setParent(%s) must fail", bad)
		}
	}
}

// Apply checks that the theme directory is writable before it writes the
// backup, so a read-only theme leaves no stray backup behind.
func TestReparentApplyRefusesReadOnlyThemeDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := reparentFixture(t)
	writeReparentTheme(t, root, "brand3", "old", map[string]string{"templates/front-page.html": `{{part "header" .}}`})
	dir := filepath.Join(root, "brand3")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	rep, err := Reparent([]string{root}, "brand3", "fresh", true, time.Now())
	if err == nil || rep.Applied {
		t.Fatalf("apply on a read-only theme dir must fail, got %+v %v", rep, err)
	}
	if !strings.Contains(err.Error(), "not writable") {
		t.Errorf("error must say the directory is not writable: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "theme.json.bak-*"))
	if len(matches) != 0 {
		t.Fatalf("no backup may be written: %v", matches)
	}
}
