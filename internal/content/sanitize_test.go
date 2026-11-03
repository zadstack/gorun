package content

import (
	"strings"
	"testing"
)

func TestSanitizeStripsScriptKeepsDetails(t *testing.T) {
	in := `<h2 class="ql-align-right" dir="rtl">عنوان</h2><script>alert(1)</script>` +
		`<details open><summary>More</summary><p data-x="1" onclick="x()" style="color:red">Body</p></details>` +
		`<figure><img src="/media/a.png" alt="a" loading="lazy"><figcaption>Cap</figcaption></figure>` +
		`<iframe src="https://www.youtube.com/embed/abc"></iframe><iframe src="https://evil.example/x"></iframe>` +
		`<table><tr><td>1</td></tr></table><pre><code class="language-go">x</code></pre><a href="javascript:alert(1)">bad</a>`
	out := Sanitize(in)
	frame := Sanitize(`<iframe src="https://player.vimeo.com/video/1" allow="camera; microphone; geolocation" sandbox="allow-top-navigation allow-scripts"></iframe>`)
	if strings.Contains(frame, "allow=") || strings.Contains(frame, "allow-top-navigation") || !strings.Contains(frame, `sandbox="`) || !strings.Contains(frame, "allow-scripts") {
		t.Errorf("iframe must be sandboxed without an allow list: %s", frame)
	}
	if bare := Sanitize(`<iframe src="https://www.youtube.com/embed/x"></iframe>`); !strings.Contains(bare, "sandbox=") {
		t.Errorf("sandbox must be added when missing: %s", bare)
	}
	for _, bad := range []string{"<script", "alert(1)", "onclick", "data-x", "style=", "evil.example", "javascript:"} {
		if strings.Contains(out, bad) {
			t.Errorf("kept %q in %s", bad, out)
		}
	}
	for _, good := range []string{"<details open", "<summary>More</summary>", "<figure>", "<figcaption>Cap</figcaption>",
		`class="ql-align-right"`, `dir="rtl"`, "عنوان", `src="https://www.youtube.com/embed/abc"`, "<table>", `class="language-go"`, `loading="lazy"`} {
		if !strings.Contains(out, good) {
			t.Errorf("lost %q in %s", good, out)
		}
	}
}

// A builder form section names its site form with data-form (a form slug);
// no other data-* attribute or value survives.
func TestSanitizeKeepsFormSectionSlug(t *testing.T) {
	out := Sanitize(`<section class="gp-section gp-form" data-form="booking"><div data-form="x">a</div></section><section data-form="Bad Slug!">b</section>`)
	if !strings.Contains(out, `data-form="booking"`) {
		t.Fatalf("form slug lost: %s", out)
	}
	if strings.Count(out, "data-form") != 1 {
		t.Fatalf("data-form kept outside a section or with an invalid slug: %s", out)
	}
}
