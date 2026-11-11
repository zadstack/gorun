package content

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPreviewTokenExpiresAndRejectsMalformed(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	it := mustCreate(t, s, Input{Type: "post", Title: "Scheduled", Status: "draft"})
	_, token, err := s.CreatePreviewLink(ctx, it.ID, "1h")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ResolvePreview(ctx, token); err != nil || got.ID != it.ID {
		t.Fatalf("fresh token: %v", err)
	}
	now = now.Add(59 * time.Minute)
	if _, err := s.ResolvePreview(ctx, token); err != nil {
		t.Fatalf("token before expiry: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := s.ResolvePreview(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired token: %v", err)
	}
	if links, _ := s.PreviewLinks(ctx, it.ID); len(links) != 0 {
		t.Fatalf("expired link listed: %d", len(links))
	}
	for _, bad := range []string{"", "short", token[:42] + "!", strings.Repeat("A", 43), token + "x"} {
		if _, err := s.ResolvePreview(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	// A trashed item is never previewed, even with a live link.
	_, token, err = s.CreatePreviewLink(ctx, it.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, it.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePreview(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trashed item resolves: %v", err)
	}
	if _, _, err := s.CreatePreviewLink(ctx, it.ID, "24h"); err == nil {
		t.Fatal("link created for a trashed item")
	}
}

func TestPreviewLinksAreCapped(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	it := mustCreate(t, s, Input{Type: "post", Title: "Popular"})
	for i := 0; i < maxActivePreviewLinks; i++ {
		if _, _, err := s.CreatePreviewLink(ctx, it.ID, "1h"); err != nil {
			t.Fatal(err)
		}
	}
	var ve *ValidationError
	if _, _, err := s.CreatePreviewLink(ctx, it.ID, "1h"); !errors.As(err, &ve) {
		t.Fatalf("link %d: %v", maxActivePreviewLinks+1, err)
	}
}
