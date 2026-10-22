package kernel

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Dependency boundaries (docs/BOUNDARIES.md). importorder_test.go enforces
// BootOrder; this file enforces the layer policy on top of it and keeps the
// import graph in the document in sync with the code:
//
//   - core modules (settings … webhooks) never import a feature module;
//   - a feature module imports another feature module only through a
//     declared edge (featureEdges), so an undeclared coupling fails here
//     before it reaches a reviewer;
//   - themes/ and patterns/ carry templates and assets, no Go code besides
//     the embed in themes/bundled.go;
//   - the public contract packages under pkg/ (themekit, patternkit, edge)
//     import nothing from internal/, so theme and pattern authors depend on
//     a stable surface.
//
// Run with GORUN_UPDATE_BOUNDARIES=1 to rewrite the generated section of
// docs/BOUNDARIES.md after a dependency change.

const (
	boundariesDoc   = "../../docs/BOUNDARIES.md"
	boundariesBegin = "<!-- BEGIN GENERATED IMPORT GRAPH -->"
	boundariesEnd   = "<!-- END GENERATED IMPORT GRAPH -->"
	modulePrefix    = "github.com/zadstack/gorun/"
)

// coreModules are the domain modules every install boots. Everything in
// BootOrder after "webhooks" is a feature module.
var coreModules = []string{"settings", "auth", "content", "taxonomy", "media", "menus", "redirects", "seo", "theme", "pattern", "builder", "site", "api", "admin", "webhooks"}

// featureEdges are the declared compile-time dependencies between feature
// modules. Anything else between two feature modules must go through a
// kernel service, a hook or an adapter published in Deps.Services.
var featureEdges = map[string][]string{
	"saas":      {"commerce"}, // subscription hook names and event type
	"wpmigrate": {"commerce"}, // product importer adapter (adapter_commerce.go)
}

func isCoreModule(m string) bool { return slices.Contains(coreModules, m) }

func isFeatureModule(m string) bool {
	pos := bootPos(m)
	return pos > bootPos("webhooks") && pos >= 0
}

// pkgImports maps every non-test package directory under root (relative,
// slash-separated, e.g. "internal/theme" or "pkg/themekit") to the sorted
// set of repository packages it imports (same relative form).
func pkgImports(t *testing.T, roots ...string) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join("../..", root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel("../..", filepath.Dir(path))
			pkg := filepath.ToSlash(rel)
			if out[pkg] == nil {
				out[pkg] = map[string]bool{}
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				if strings.HasPrefix(p, modulePrefix) {
					out[pkg][strings.TrimPrefix(p, modulePrefix)] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	result := make(map[string][]string, len(out))
	for pkg, set := range out {
		list := make([]string, 0, len(set))
		for p := range set {
			list = append(list, p)
		}
		sort.Strings(list)
		result[pkg] = list
	}
	return result
}

// moduleOf collapses a package path to its module name ("internal/theme/fake"
// → "theme"); non-internal paths map to themselves.
func moduleOf(pkg string) string {
	if rest, ok := strings.CutPrefix(pkg, "internal/"); ok {
		return strings.Split(rest, "/")[0]
	}
	return pkg
}

func layerOf(module string) string {
	switch {
	case infra[module]:
		return "infra"
	case isCoreModule(module):
		return "core"
	case isFeatureModule(module):
		return "feature"
	case module == "pkg/cli" || strings.HasPrefix(module, "cmd/") || strings.HasPrefix(module, "tools/"):
		return "binary" // composition root: imports every module by design
	case strings.HasPrefix(module, "pkg/"):
		return "contract"
	default:
		return "infra" // unlisted internal helpers (observe, update, metrics, backup, mailer, brand, starter)
	}
}

func TestFeatureModulesImportOnlyDeclaredFeatureEdges(t *testing.T) {
	graph := pkgImports(t, "internal")
	for pkg, imports := range graph {
		from := moduleOf(pkg)
		for _, imp := range imports {
			to := moduleOf(imp)
			if from == to || !isFeatureModule(to) {
				continue
			}
			switch {
			case isCoreModule(from) || infra[from]:
				t.Errorf("%s (%s layer) imports feature module %s", pkg, layerOf(from), to)
			case isFeatureModule(from) && !slices.Contains(featureEdges[from], to):
				t.Errorf("%s imports feature module %s without a declared edge in featureEdges (use a service, hook or adapter)", pkg, to)
			}
		}
	}
	for from, tos := range featureEdges {
		for _, to := range tos {
			used := false
			for pkg, imports := range graph {
				if moduleOf(pkg) == from && slices.ContainsFunc(imports, func(p string) bool { return moduleOf(p) == to }) {
					used = true
				}
			}
			if !used {
				t.Errorf("featureEdges declares %s -> %s but no package uses it; remove the stale edge", from, to)
			}
		}
	}
}

func TestContractPackagesAndThemesStayStandalone(t *testing.T) {
	graph := pkgImports(t, "pkg", "themes", "patterns")
	for pkg, imports := range graph {
		if strings.HasPrefix(pkg, "pkg/cli") {
			continue // the composition root imports everything by design
		}
		for _, imp := range imports {
			if strings.HasPrefix(pkg, "pkg/") && strings.HasPrefix(imp, "internal/") {
				t.Errorf("contract package %s imports %s; pkg/ contracts must not depend on internal modules", pkg, imp)
			}
			if strings.HasPrefix(pkg, "themes") || strings.HasPrefix(pkg, "patterns") {
				t.Errorf("%s imports %s; themes and patterns are data, not Go code", pkg, imp)
			}
		}
	}
	for pkg := range graph {
		if (strings.HasPrefix(pkg, "themes") && pkg != "themes") || strings.HasPrefix(pkg, "patterns") {
			t.Errorf("Go package under %s; themes/ holds only themes/bundled.go (the embed) and patterns/ no Go at all", pkg)
		}
	}
}

// renderImportGraph writes the module-level import table documented in
// docs/BOUNDARIES.md: one row per internal module (sub-packages collapsed)
// and per pkg/ contract package, with layer and the modules it imports.
func renderImportGraph(graph map[string][]string) string {
	byModule := map[string]map[string]bool{}
	for pkg, imports := range graph {
		m := moduleOf(pkg)
		if strings.HasPrefix(pkg, "pkg/") {
			m = pkg
		}
		if byModule[m] == nil {
			byModule[m] = map[string]bool{}
		}
		for _, imp := range imports {
			to := moduleOf(imp)
			if strings.HasPrefix(imp, "pkg/") {
				to = imp
			}
			if to != m {
				byModule[m][to] = true
			}
		}
	}
	names := make([]string, 0, len(byModule))
	for m := range byModule {
		names = append(names, m)
	}
	order := func(m string) (int, int) {
		switch layerOf(m) {
		case "infra":
			return 0, bootPos(m)
		case "core":
			return 1, bootPos(m)
		case "feature":
			return 2, bootPos(m)
		case "contract":
			return 3, 0
		default:
			return 4, 0
		}
	}
	sort.Slice(names, func(i, j int) bool {
		li, pi := order(names[i])
		lj, pj := order(names[j])
		if li != lj {
			return li < lj
		}
		if pi != pj {
			return pi < pj
		}
		return names[i] < names[j]
	})
	var b bytes.Buffer
	b.WriteString("| Package | Layer | Imports (repository packages, non-test files) |\n|---|---|---|\n")
	for _, m := range names {
		deps := make([]string, 0, len(byModule[m]))
		for d := range byModule[m] {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		path := m
		if !strings.Contains(m, "/") {
			path = "internal/" + m
		}
		dep := "—"
		if len(deps) > 0 {
			dep = "`" + strings.Join(deps, "` `") + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", path, layerOf(m), dep)
	}
	return b.String()
}

func TestBoundariesDocumentMatchesImportGraph(t *testing.T) {
	graph := pkgImports(t, "internal", "pkg", "cmd")
	want := boundariesBegin + "\n" + renderImportGraph(graph) + boundariesEnd
	raw, err := os.ReadFile(boundariesDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	start, end := strings.Index(doc, boundariesBegin), strings.Index(doc, boundariesEnd)
	if start < 0 || end < start {
		t.Fatalf("%s lacks the %s / %s markers", boundariesDoc, boundariesBegin, boundariesEnd)
	}
	got := doc[start : end+len(boundariesEnd)]
	if got == want {
		return
	}
	if os.Getenv("GORUN_UPDATE_BOUNDARIES") != "" {
		if err := os.WriteFile(boundariesDoc, []byte(doc[:start]+want+doc[end+len(boundariesEnd):]), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s updated", boundariesDoc)
		return
	}
	t.Fatalf("the import graph in %s is stale; run GORUN_UPDATE_BOUNDARIES=1 go test ./internal/kernel -run TestBoundariesDocumentMatchesImportGraph\n--- want\n%s\n--- got\n%s", boundariesDoc, want, got)
}
