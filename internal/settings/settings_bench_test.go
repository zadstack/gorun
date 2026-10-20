package settings

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/db"
)

func BenchmarkGetCachedVsCold(b *testing.B) {
	d, err := db.Open(filepath.Join(b.TempDir(), "t.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d, "settings", Migrations()); err != nil {
		b.Fatal(err)
	}
	s := New(d)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		if err := s.Set(ctx, "site.key"+string(rune('a'+i%26)), "value"); err != nil {
			b.Fatal(err)
		}
	}
	// Warm common public keys.
	keys := []string{"site.title", "site.tagline", "site.locale", "site.theme", "seo.title_template"}
	for _, k := range keys {
		_ = s.Set(ctx, k, "x")
		var v string
		_ = s.Get(ctx, k, &v)
	}

	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			var v string
			for pb.Next() {
				for _, k := range keys {
					if err := s.Get(ctx, k, &v); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	})

	b.Run("no-cache", func(b *testing.B) {
		uncached := &Store{db: d, log: s.log} // nil cache
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			var v string
			for pb.Next() {
				for _, k := range keys {
					if err := uncached.Get(ctx, k, &v); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	})
}
