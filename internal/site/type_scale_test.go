package site_test

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/web"
)

// The admin declares a compact instance of the shared type scale (the step
// names of themes/_base/tokens.css) and its Tailwind build maps text-* onto
// it by value, so a template's text-sm and a stylesheet's var(--fs-base) are
// one size. Only the two title steps are fluid.
func TestAdminStylesheetsShareTheTypeScale(t *testing.T) {
	admin, err := web.FS.ReadFile("admin/static/admin.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(admin)
	for _, tok := range []string{"--fs-xs", "--fs-sm", "--fs-md", "--fs-base", "--fs-lg", "--fs-xl", "--fs-2xl", "--fs-3xl", "--leading-tight", "--leading-snug", "--leading-normal", "--leading-relaxed"} {
		if !strings.Contains(css, "\n  "+tok+": ") {
			t.Errorf("admin.css does not declare %s", tok)
		}
	}
	for _, fluid := range []string{"--fs-2xl: clamp(", "--fs-3xl: clamp("} {
		if !strings.Contains(css, fluid) {
			t.Errorf("admin.css should make %s fluid", fluid)
		}
	}
	if !strings.Contains(css, `[dir="rtl"] { --leading-tight:`) {
		t.Error("admin.css should raise the line heights for Arabic")
	}
	if strings.Contains(css, "font-size: 1.375rem") || strings.Contains(css, "font-size: .8125rem") {
		t.Error("admin.css still sizes a component in rem instead of a step")
	}
	tw, err := web.FS.ReadFile("admin/static/admin-tailwind.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--text-xs: var(--fs-sm);", "--text-sm: var(--fs-base);", "--text-base: var(--fs-lg);", "--text-lg: var(--fs-xl);", "--text-xl: var(--fs-2xl);", "--text-2xl: var(--fs-3xl);", "--text-sm--line-height: var(--leading-normal);"} {
		if !strings.Contains(string(tw), want) {
			t.Errorf("admin-tailwind.css lacks %q", want)
		}
	}
	if strings.Contains(string(tw), "--text-sm: 0.875rem;") {
		t.Error("admin-tailwind.css still declares Tailwind's fixed text-sm")
	}
}
