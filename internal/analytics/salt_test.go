package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"
)

func TestVisitorHashStableWithinDayChangesAcrossDays(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	morning := time.Date(2026, 11, 3, 0, 0, 1, 0, time.UTC)
	evening := time.Date(2026, 11, 3, 23, 59, 59, 0, time.UTC)
	next := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)

	a, err := s.VisitorHash(ctx, morning, "site-1", "198.51.100.1", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 16 {
		t.Fatalf("hash %q has length %d, want 16", a, len(a))
	}
	b, err := s.VisitorHash(ctx, evening, "site-1", "198.51.100.1", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("same visitor, same day: %q != %q", a, b)
	}
	if other, _ := s.VisitorHash(ctx, evening, "site-1", "198.51.100.2", "Mozilla/5.0"); other == a {
		t.Fatal("different IP gave the same hash")
	}
	if other, _ := s.VisitorHash(ctx, evening, "site-1", "198.51.100.1", "curl/8"); other == a {
		t.Fatal("different UA gave the same hash")
	}
	if other, _ := s.VisitorHash(ctx, evening, "site-2", "198.51.100.1", "Mozilla/5.0"); other == a {
		t.Fatal("different site gave the same hash")
	}
	c, err := s.VisitorHash(ctx, next, "site-1", "198.51.100.1", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Fatal("hash did not change across days")
	}

	// The formula is hex(sha256(salt || siteID || ip || ua))[:16].
	var salt []byte
	if err := s.DB().QueryRow(`SELECT salt FROM salts WHERE day = '2026-11-04'`).Scan(&salt); err != nil {
		t.Fatal(err)
	}
	if len(salt) != 32 {
		t.Fatalf("salt is %d bytes, want 32", len(salt))
	}
	sum := sha256.Sum256(append(append([]byte{}, salt...), "site-1198.51.100.1Mozilla/5.0"...))
	if want := hex.EncodeToString(sum[:])[:16]; c != want {
		t.Fatalf("hash = %q, want %q", c, want)
	}

	// The salt survives a restart: same day, same hash.
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	d, err := s2.VisitorHash(ctx, next, "site-1", "198.51.100.1", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if d != c {
		t.Fatalf("hash changed across a restart: %q != %q", d, c)
	}
}

func TestSaltRotationDeletesOldSalts(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	for _, d := range []time.Time{
		time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
	} {
		if _, err := s.VisitorHash(ctx, d, "site", "ip", "ua"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.DB().Query(`SELECT day FROM salts ORDER BY day`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var days []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		days = append(days, d)
	}
	if len(days) != 1 || days[0] != "2026-11-03" {
		t.Fatalf("salts = %v, want only 2026-11-03", days)
	}
}
