package gutenberg

import (
	"testing"
)

func TestGutenbergParseTree(t *testing.T) {
	raw := `<p>classic intro</p>
<!-- wp:group {"tagName":"section","layout":{"type":"constrained"}} -->
<section class="wp-block-group"><!-- wp:heading {"level":3} -->
<h3 class="wp-block-heading">عنوان</h3>
<!-- /wp:heading -->

<!-- wp:columns -->
<div class="wp-block-columns"><!-- wp:column {"width":"40%"} -->
<div class="wp-block-column"><!-- wp:paragraph -->
<p>one</p>
<!-- /wp:paragraph --></div>
<!-- /wp:column --></div>
<!-- /wp:columns -->

<!-- wp:separator /-->
<!-- wp:latest-posts {"postsToShow":3} /--></section>
<!-- /wp:group -->
<!-- wp:acme/widget {"label":"a \u002d\u002d> b"} -->
<div>x</div>
<!-- /wp:acme/widget -->`
	blocks := Parse(raw)
	if len(blocks) != 3 {
		t.Fatalf("top-level blocks = %d, want 3 (freeform, group, acme/widget): %+v", len(blocks), names(blocks))
	}
	if blocks[0].Name != "" || blocks[0].HTML() != "<p>classic intro</p>\n" {
		t.Fatalf("freeform block = %q %q", blocks[0].Name, blocks[0].HTML())
	}
	g := blocks[1]
	if g.Name != "core/group" || g.Attrs["tagName"] != "section" {
		t.Fatalf("group = %q %v", g.Name, g.Attrs)
	}
	if got := names(g.Inner); len(got) != 4 || got[0] != "core/heading" || got[1] != "core/columns" || got[2] != "core/separator" || got[3] != "core/latest-posts" {
		t.Fatalf("group children = %v", got)
	}
	if lvl, _ := g.Inner[0].Attrs["level"].(float64); lvl != 3 {
		t.Fatalf("heading level attr = %v", g.Inner[0].Attrs["level"])
	}
	col := g.Inner[1].Inner[0]
	if col.Name != "core/column" || col.Attrs["width"] != "40%" || len(col.Inner) != 1 || col.Inner[0].Name != "core/paragraph" {
		t.Fatalf("column = %+v", col)
	}
	if !g.Inner[2].Void || len(g.Inner[2].Parts) != 0 {
		t.Fatalf("separator should be void: %+v", g.Inner[2])
	}
	if got := blocks[2]; got.Name != "acme/widget" || got.Attrs["label"] != "a --> b" {
		t.Fatalf("namespaced block = %q %v", got.Name, got.Attrs)
	}
	// The group's own HTML keeps the wrapper with placeholders removed.
	if h := g.HTML(); h == "" || h[:8] != "\n<sectio" {
		t.Fatalf("group own html = %q", h)
	}
}

func TestGutenbergParseUnclosedAndStrayCloser(t *testing.T) {
	blocks := Parse("<!-- /wp:paragraph --><!-- wp:paragraph --><p>a</p>")
	if len(blocks) != 1 || blocks[0].Name != "core/paragraph" || blocks[0].HTML() != "<p>a</p>" {
		t.Fatalf("got %+v", names(blocks))
	}
	if HasBlocks("<p>plain</p>") || !HasBlocks("<!-- wp:paragraph --><p>a</p><!-- /wp:paragraph -->") {
		t.Fatal("HasBlocks wrong")
	}
}

func names(bs []*Block) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Name
	}
	return out
}
