package content

import (
	"strings"
	"testing"
)

func TestBuilderMarkupSurvivesSanitizingWithoutActiveHTML(t *testing.T) {
	input := `<section class="gp-section gp-hero gp-variant-split" onclick="alert(1)"><div class="gp-container"><h2>Title</h2><a class="gp-button" href="javascript:alert(1)">Click</a></div></section><script>alert(1)</script><div class="arbitrary css-class">Unsafe classes</div>`
	cleaned := Sanitize(input)
	for _, want := range []string{`<section class="gp-section gp-hero gp-variant-split">`, `class="gp-container"`, `class="gp-button"`} {
		if !strings.Contains(cleaned, want) {
			t.Errorf("missing %s in %s", want, cleaned)
		}
	}
	for _, bad := range []string{"onclick", "javascript:", "<script>", "arbitrary", "css-class"} {
		if strings.Contains(cleaned, bad) {
			t.Errorf("unsafe %s in %s", bad, cleaned)
		}
	}
}
