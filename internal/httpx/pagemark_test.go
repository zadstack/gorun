package httpx

import (
	"context"
	"testing"
)

// Two middlewares that each install a mark (analytics, then chat) both see
// the theme mark the page once: the inner one reuses the outer mark.
func TestWithPageMarkSharesTheOuterMark(t *testing.T) {
	ctx, outer := WithPageMark(context.Background())
	ctx, inner := WithPageMark(ctx)
	MarkThemePage(ctx)
	if !outer.Themed() || !inner.Themed() {
		t.Fatalf("outer %v inner %v, want both marked", outer.Themed(), inner.Themed())
	}
	if _, fresh := WithPageMark(context.Background()); fresh.Themed() {
		t.Fatal("a fresh mark starts marked")
	}
}
