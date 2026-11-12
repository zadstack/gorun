package web

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Interface text belongs in the module catalogs (<module>.en.json and
// <module>.ar.json) and reaches templates through {{t "key"}} and scripts
// through data-* attributes. These patterns pick display text by interface
// language inline instead, so they bypass the catalogs and their ar/en parity
// checks.
var (
	templateLocalePick = regexp.MustCompile(`eq \(?locale\)? "(?:ar|en)"|\$ar\s*:?=`)
	scriptLocalePick   = regexp.MustCompile(`lang\s*===?\s*['"](?:ar|en)['"]`)
)

// localeValueUses are the reviewed spans where the interface locale selects a
// locale value (a language switch target, a request parameter), not text.
var localeValueUses = map[string][]string{
	"admin/templates/partials/topbar.html": {
		`value="{{if eq locale "ar"}}en{{else}}ar{{end}}"`,
	},
	"admin/static/canvas-ai-suggestions.js": {
		`document.documentElement.lang === 'ar' ? 'ar' : 'en'`,
	},
}

// TestNoInlineLocaleTextPickers fails when an admin template, a module
// template or an admin script chooses display text by interface locale
// instead of using the catalogs.
func TestNoInlineLocaleTextPickers(t *testing.T) {
	scanned := 0
	check := func(name string, src []byte, re *regexp.Regexp) {
		scanned++
		text := string(src)
		for _, allowed := range localeValueUses[name] {
			text = strings.ReplaceAll(text, allowed, "")
		}
		for _, loc := range re.FindAllStringIndex(text, -1) {
			line := strings.Count(text[:loc[0]], "\n") + 1
			t.Errorf("%s:%d: %q picks display text by locale; add the strings to the module catalogs and use {{t \"key\"}} (or data-* attributes for scripts)", name, line, text[loc[0]:loc[1]])
		}
	}
	walk := func(fsys fs.FS, root, prefix, ext string, re *regexp.Regexp) {
		err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ext) || strings.HasSuffix(p, "_test.cjs") {
				return err
			}
			src, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			check(prefix+p, src, re)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	walk(FS, "admin/templates", "", ".html", templateLocalePick)
	walk(FS, "admin/static", "", ".js", scriptLocalePick)

	// Module-owned template directories under internal/ (testdata excluded).
	repo := os.DirFS("..")
	moduleDirs, err := fs.Glob(repo, "internal/*")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range moduleDirs {
		_ = fs.WalkDir(repo, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return fs.SkipDir
			}
			if d.IsDir() && d.Name() == "templates" {
				walk(repo, p, "", ".html", templateLocalePick)
				return fs.SkipDir
			}
			return nil
		})
	}
	if scanned < 200 {
		t.Fatalf("scanned only %d files; the template or script roots moved", scanned)
	}
}

// TestLocalePickerPatterns pins what the guard catches and what it allows.
func TestLocalePickerPatterns(t *testing.T) {
	for _, s := range []string{`{{if eq locale "ar"}}x{{end}}`, `{{if eq (locale) "ar"}}`, `{{$ar := eq locale "ar"}}`, `{{if eq locale "en"}}`} {
		if !templateLocalePick.MatchString(s) {
			t.Errorf("template guard misses %s", s)
		}
	}
	for _, s := range []string{`{{if eq .Locale "ar"}}العربية{{end}}`, `{{t "starters.next"}}`, `<option value="ar">`} {
		if templateLocalePick.MatchString(s) {
			t.Errorf("template guard flags %s", s)
		}
	}
	for _, s := range []string{`document.documentElement.lang==='ar'?'a':'b'`, `lang === "ar"`} {
		if !scriptLocalePick.MatchString(s) {
			t.Errorf("script guard misses %s", s)
		}
	}
	if scriptLocalePick.MatchString(`const L=document.querySelector('[data-x]').dataset`) {
		t.Error("script guard flags a dataset read")
	}
}
