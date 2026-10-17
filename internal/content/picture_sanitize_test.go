package content

import (
	"html"
	"strings"
	"testing"
)

func TestSanitizePicturePreservesSafeImageSources(t *testing.T) {
	out := Sanitize(`<picture><source type="image/webp" srcset="/media/a.webp 1x, https://cdn.test/a.webp 2x" onerror="evil()"><img src="/media/a.png" onerror="evil()"></picture><script>evil()</script>`)
	for _, want := range []string{`<picture>`, `<source`, `srcset="/media/a.webp 1x, https://cdn.test/a.webp 2x"`, `type="image/webp"`} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q: %s", want, out)
		}
	}
	if strings.Contains(out, "evil") || strings.Contains(out, "onerror") {
		t.Fatalf("unsafe HTML: %s", out)
	}
}

func TestSanitizeSrcsetRejectsUnsafeSchemes(t *testing.T) {
	for _, val := range []string{"javascript:alert(1) 1x", "data:text/html;base64,AAAA 1x", "https://good.test/a.png 1x, javascript:alert(1) 2x"} {
		out := Sanitize(`<picture><source srcset="` + val + `"><img src="/media/safe.png" srcset="` + val + `"></picture>`)
		if strings.Contains(out, "srcset=") {
			t.Errorf("unsafe srcset retained: %s", out)
		}
	}
}

func TestSanitizeSrcsetDoesNotRewritePlainText(t *testing.T) {
	in := `<p>Example srcset="javascript:example" is text.</p>`
	if got := html.UnescapeString(Sanitize(in)); got != in {
		t.Fatalf("plain text changed: %s", got)
	}
}
