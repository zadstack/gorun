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

func TestNativeComparisonFocusAttributesAreBounded(t *testing.T) {
	input := `<div class="gp-s-compare-wrap" tabindex="0" role="region" aria-label="مقارنة الخيارات"><table class="gp-s-compare"><thead><tr><th scope="col">Capability</th></tr></thead><tbody><tr><th scope="row">Feature</th></tr></tbody></table></div>`
	cleaned := Sanitize(input)
	for _, attribute := range []string{`tabindex="0"`, `role="region"`, `aria-label="مقارنة الخيارات"`, `scope="col"`, `scope="row"`} {
		if !strings.Contains(cleaned, attribute) {
			t.Errorf("native keyboard semantics lost %s in %s", attribute, cleaned)
		}
	}
	for _, value := range []string{"-1", "1", "999", "00", "invalid"} {
		cleaned := Sanitize(`<div tabindex="` + value + `" role="button">Text</div>`)
		if strings.Contains(cleaned, "tabindex=") || strings.Contains(cleaned, `role="button"`) {
			t.Fatalf("unbounded focus/role retained: %s", cleaned)
		}
	}
	longLabel := strings.Repeat("x", 301)
	if strings.Contains(Sanitize(`<div aria-label="`+longLabel+`">Text</div>`), "aria-label=") {
		t.Fatal("oversized accessible label retained")
	}
	if strings.Contains(Sanitize(`<span tabindex="0">Text</span>`), "tabindex=") {
		t.Fatal("focus attribute allowed outside bounded div surface")
	}
}
