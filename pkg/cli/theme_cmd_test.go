package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeCLI(t *testing.T) {
	dir := t.TempDir()
	themes, _ := filepath.Abs("../../themes")
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_THEMES", themes)
	data := []string{"--data", dir}
	run := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		err := themeCmd(append(args, data...), strings.NewReader(stdin), &out)
		return out.String(), err
	}
	info := func() themeInfo {
		t.Helper()
		out, err := run("", "info")
		if err != nil {
			t.Fatal(err)
		}
		var i themeInfo
		if err := json.Unmarshal([]byte(out), &i); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return i
	}
	source := func(i themeInfo, name string) string {
		for _, f := range i.Templates {
			if f.Name == name {
				return f.Source
			}
		}
		return ""
	}
	i := info()
	if i.Active != "default" || len(i.Themes) < 2 || source(i, "index.html") != "theme" {
		t.Fatalf("info %+v", i)
	}
	if _, err := run("", "activate", "nope"); err == nil {
		t.Fatal("unknown theme activated")
	}
	if _, err := run("", "activate", "business"); err != nil {
		t.Fatal(err)
	}
	if info().Active != "business" {
		t.Fatal("not activated")
	}
	if _, err := run("{{ broken", "save", "parts.footer.html"); err == nil {
		t.Fatal("a broken template was saved")
	}
	if _, err := run("<footer>mine</footer>", "save", "parts.footer.html"); err != nil {
		t.Fatal(err)
	}
	if source(info(), "parts.footer.html") != "override" {
		t.Fatal("override not listed")
	}
	if out, _ := run("", "show", "parts.footer.html"); out != "<footer>mine</footer>" {
		t.Fatalf("show %q", out)
	}
	if _, err := run("<footer>draft</footer>", "save", "parts.header.html", "--draft"); err != nil {
		t.Fatal(err)
	}
	for _, f := range info().Templates {
		if f.Name == "parts.header.html" && (!f.Draft || f.Source != "theme") {
			t.Fatalf("draft %+v", f)
		}
	}
	if _, err := run("", "reset", "parts.footer.html"); err != nil {
		t.Fatal(err)
	}
	if source(info(), "parts.footer.html") != "theme" {
		t.Fatal("reset did not restore the theme's file")
	}
}
