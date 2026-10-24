package media

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// A focal point is stored, read back, replaced and cleared; a point outside
// 0..1 is refused and leaves the stored one untouched.
func TestFocalPointRoundTrip(t *testing.T) {
	s, _ := testService(t, false)
	ctx := context.Background()
	m, err := s.Upload(ctx, "photo.jpg", bytes.NewReader(jpegBytes(t, 120, 80)), UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Focal != nil {
		t.Fatalf("new media has a focal point: %+v", m.Focal)
	}
	if _, err := s.Update(ctx, m.ID, Patch{Focal: &Focal{X: 0.25, Y: 0.75}}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, m.ID)
	if got.Focal == nil || *got.Focal != (Focal{X: 0.25, Y: 0.75}) {
		t.Fatalf("focal after set = %+v", got.Focal)
	}
	for _, bad := range []Focal{{X: -0.1, Y: 0.5}, {X: 0.5, Y: 1.2}} {
		if _, err := s.Update(ctx, m.ID, Patch{Focal: &bad}); !errors.Is(err, ErrFocal) {
			t.Fatalf("focal %+v: err = %v, want ErrFocal", bad, err)
		}
	}
	if got, _ := s.Get(ctx, m.ID); got.Focal == nil || got.Focal.X != 0.25 {
		t.Fatalf("a refused focal point changed the stored one: %+v", got.Focal)
	}
	alt := "kept"
	if _, err := s.Update(ctx, m.ID, Patch{Alt: &alt}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, m.ID); got.Focal == nil {
		t.Fatal("updating other fields cleared the focal point")
	}
	if _, err := s.Update(ctx, m.ID, Patch{ClearFocal: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, m.ID); got.Focal != nil {
		t.Fatalf("focal after clear = %+v", got.Focal)
	}
}
