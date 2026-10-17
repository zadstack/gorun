package kernel

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const internalPrefix = "github.com/zadstack/gorun/internal/"

// infra is the kernel infrastructure: packages Boot constructs or that sit
// under every module. Infra packages (and any other unlisted package) may
// import each other freely (the compiler rejects real cycles) and are
// importable by every module, but must never import a domain module.
// Domain modules (listed in BootOrder, not infra) may only import earlier
// domain modules.
var infra = map[string]bool{
	"kernel": true, "config": true, "db": true, "hooks": true, "httpx": true,
	"settings": true, "i18n": true, "audit": true, "jobs": true, "diagnostics": true,
}

func bootPos(m string) int {
	for i, n := range BootOrder {
		if n == m {
			return i
		}
	}
	return -1
}

func isFoundation(m string) bool { return infra[m] || bootPos(m) == -1 }

// checkImport reports whether internal package `from` may import `to`
// (both given as the first path segment under internal/, so
// internal/settings/settingsmod counts as "settings").
func checkImport(from, to string) error {
	switch {
	case from == to, isFoundation(to):
		return nil
	case isFoundation(from):
		return fmt.Errorf("infra package %s must not import domain module %s", from, to)
	case bootPos(to) > bootPos(from):
		return fmt.Errorf("module %s must not import later module %s (BootOrder)", from, to)
	}
	return nil
}

// fileImportAllowed applies checkImport to one file's import. Test files
// (_test.go) are exempt: tests may import any internal package, e.g. to boot
// a later module end to end.
func fileImportAllowed(filename, from, to string) error {
	if strings.HasSuffix(filename, "_test.go") {
		return nil
	}
	return checkImport(from, to)
}

func TestFileImportAllowedExemptsTests(t *testing.T) {
	cases := []struct {
		file, from, to string
		ok             bool
	}{
		{"content/admin_test.go", "content", "commerce", true}, // tests may import later modules
		{"kernel/boot_test.go", "kernel", "auth", true},        // and infra tests domain modules
		{"content/admin.go", "content", "commerce", false},     // the rule still holds for code
		{"kernel/boot.go", "kernel", "auth", false},
		{"commerce/cart.go", "commerce", "content", true},
		{"content/test.go", "content", "commerce", false}, // only the _test.go suffix exempts
	}
	for _, c := range cases {
		err := fileImportAllowed(c.file, c.from, c.to)
		if (err == nil) != c.ok {
			t.Errorf("%s (%s -> %s): ok=%v want %v (%v)", c.file, c.from, c.to, err == nil, c.ok, err)
		}
	}
}

func TestCheckImportRules(t *testing.T) {
	cases := []struct {
		from, to string
		ok       bool
	}{
		{"commerce", "content", true},
		{"content", "commerce", false},
		{"auth", "audit", true}, // infra is importable by all
		{"auth", "jobs", true},
		{"kernel", "settings", true},
		{"hooks", "jobs", true},
		{"jobs", "hooks", true}, // unlisted importee
		{"settings", "kernel", true},
		{"kernel", "auth", false},
		{"hooks", "content", false},
		{"audit", "auth", false},
		{"settings", "audit", true}, // infra imports infra freely
		{"db", "settings", true},
		{"audit", "httpx", true},
		{"httpx", "auth", false},
		{"config", "theme", false},
		{"jobs", "db", true},
		{"ext", "mcp", true},
	}
	for _, c := range cases {
		err := checkImport(c.from, c.to)
		if (err == nil) != c.ok {
			t.Errorf("%s -> %s: ok=%v want %v (%v)", c.from, c.to, err == nil, c.ok, err)
		}
	}
}

func TestImportOrder(t *testing.T) {
	root := ".." // internal/
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		from := strings.Split(filepath.ToSlash(rel), "/")[0]
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		files++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !strings.HasPrefix(p, internalPrefix) {
				continue
			}
			to := strings.Split(strings.TrimPrefix(p, internalPrefix), "/")[0]
			if err := fileImportAllowed(path, from, to); err != nil {
				t.Errorf("%s: %v", rel, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no Go files found under internal/")
	}
}
