package gutenberg

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "testdata", "gutenberg", name)
	if *update {
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v (run with -update)", name, err)
	}
	if strings.TrimSpace(string(want)) != strings.TrimSpace(got) {
		t.Fatalf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func readInput(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "gutenberg", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestGutenbergConvertCoreBlocksGolden covers every core block of spec §4.7.
func TestGutenbergConvertCoreBlocksGolden(t *testing.T) {
	raw := readInput(t, "core_blocks.html")
	res := ConvertRaw(raw, Options{})
	golden(t, "core_blocks.golden.html", res.HTML)
	if len(res.Unknown) != 0 {
		t.Fatalf("core blocks reported unknown: %v", res.Unknown)
	}
	for _, want := range []string{"core/paragraph", "core/heading", "core/list", "core/quote", "core/image", "core/gallery",
		"core/columns", "core/column", "core/group", "core/buttons", "core/button", "core/separator", "core/spacer",
		"core/table", "core/code", "core/preformatted", "core/details", "core/embed", "core/html", "core/cover", "core/media-text"} {
		if !strings.Contains(raw, "<!-- wp:"+strings.TrimPrefix(want, "core/")+" ") && !strings.Contains(raw, "<!-- wp:"+strings.TrimPrefix(want, "core/")+" -->") {
			t.Errorf("fixture lacks %s", want)
		}
	}
	for _, bad := range []string{"wp-block-", "has-", "is-provider", "style=", "<!-- wp:", "wp-image-"} {
		if strings.Contains(res.HTML, bad) {
			t.Errorf("converted HTML still contains %q", bad)
		}
	}
	if res.Shortcodes["ep_plans"] != 1 {
		t.Errorf("shortcodes = %v", res.Shortcodes)
	}
	keep := ConvertRaw(raw, Options{KeepClasses: true})
	if !strings.Contains(keep.HTML, `class="wp-block-quote is-style-large"`) || !strings.Contains(keep.HTML, `style="flex-basis:56%"`) {
		t.Errorf("KeepClasses dropped classes/styles")
	}
}

func TestGutenbergUnknownBlockKeepsInnerAndLogs(t *testing.T) {
	raw := `<!-- wp:acme/slider {"speed":3} -->
<div class="acme-slider wp-block-acme-slider"><!-- wp:paragraph -->
<p class="has-small-font-size">شريحة</p>
<!-- /wp:paragraph --></div>
<!-- /wp:acme/slider -->
<!-- wp:latest-posts /-->`
	res := ConvertRaw(raw, Options{})
	if res.Unknown["acme/slider"] != 1 || res.Unknown["core/latest-posts"] != 1 {
		t.Fatalf("unknown = %v", res.Unknown)
	}
	if want := "<div class=\"acme-slider\"><p>شريحة</p></div>"; !strings.Contains(res.HTML, want) {
		t.Fatalf("inner HTML not kept:\n%s", res.HTML)
	}
	if got := res.UnknownNames(); len(got) != 2 || got[0] != "acme/slider" {
		t.Fatalf("names = %v", got)
	}
}

func TestCleanRenderedExamplePressHome(t *testing.T) {
	raw := readInput(t, "rendered_home.html")
	got := CleanHTML(raw, Options{})
	golden(t, "rendered_home.golden.html", got)
	if strings.Contains(got, "wp-block-") || strings.Contains(got, "style=") || !strings.Contains(got, "ep-k-hero") {
		t.Fatal("rendered cleanup wrong (theme classes must stay, WP classes go)")
	}
}
