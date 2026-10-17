package httpx

import (
	"embed"
	"os"
	"testing"
)

//go:embed testdata
var embeddedTestdata embed.FS

// An embedded FS cannot change while the process runs, so dev mode gains
// nothing by re-parsing it on every request; a directory can change on disk.
func TestReloadable(t *testing.T) {
	if reloadable(embeddedTestdata) {
		t.Error("embed.FS must not be reloadable")
	}
	if !reloadable(os.DirFS("testdata")) {
		t.Error("os.DirFS must be reloadable")
	}
}

func TestDevReparsesOnlyReloadableSources(t *testing.T) {
	tpl := newTestTemplates(t, true)
	first, _ := tpl.set()
	second, _ := tpl.set()
	if first == second {
		t.Fatal("dev mode should re-parse an on-disk FS")
	}

	tpl.fsys = embeddedTestdata
	tpl.cached, _ = tpl.parse()
	first, _ = tpl.set()
	second, _ = tpl.set()
	if first != second {
		t.Fatal("dev mode re-parsed sources that cannot change")
	}

	tpl.extensions = append(tpl.extensions, os.DirFS("testdata"))
	first, _ = tpl.set()
	second, _ = tpl.set()
	if first == second {
		t.Fatal("an on-disk extension should keep dev re-parsing")
	}
}
