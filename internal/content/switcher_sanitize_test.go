package content

import (
	"strings"
	"testing"
)

// A builder feature switcher keeps its exclusive-group name through the
// sanitizer; any other name on details, or on another element, is dropped.
func TestSanitizeKeepsOnlyBuilderSwitcherNames(t *testing.T) {
	out := Sanitize(`<details class="gp-s-switch" name="gp-switch-01ABC" open><summary>A</summary></details>` +
		`<details name="evil"><summary>B</summary></details><div name="gp-switch-x">C</div>`)
	if !strings.Contains(out, `name="gp-switch-01ABC"`) || !strings.Contains(out, "open") {
		t.Fatalf("lost the switcher group: %s", out)
	}
	if strings.Contains(out, `name="evil"`) || strings.Contains(out, `<div name=`) {
		t.Fatalf("kept an unexpected name: %s", out)
	}
}

// A builder directory slot keeps its type class, underscores included, so the
// theme can fill it at render time.
func TestSanitizeKeepsDirectorySlot(t *testing.T) {
	want := `<div class="gp-s-directory gp-s-dir-case_study"></div>`
	if out := Sanitize(want); out != want {
		t.Fatalf("directory slot changed: %s", out)
	}
}
