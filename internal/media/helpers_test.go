package media

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/settings"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx, d, "audit", audit.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "settings", settings.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "media", migrations); err != nil {
		t.Fatal(err)
	}
	return d
}

// testService returns a media service on a temp DB and storage dir. Hooks
// have no queue attached, so Emit delivers synchronously.
func testService(t *testing.T, allowPrivate bool) (*service, *hooks.Registry) {
	t.Helper()
	d := newTestDB(t)
	h := hooks.New()
	s := &service{db: d, storage: NewLocalStorage(filepath.Join(t.TempDir(), "media")), settings: settings.New(d),
		hooks: h, audit: audit.New(d, discardLog()), log: discardLog(), client: httpx.NewSafeClient(allowPrivate),
		now: func() time.Time { return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC) }}
	return s, h
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y += 7 {
		for x := 0; x < w; x += 7 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func auditCount(t *testing.T, d *sql.DB, action string) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
