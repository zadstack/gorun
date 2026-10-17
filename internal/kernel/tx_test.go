package kernel_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/settings"
)

// Infrastructure writes inside db.InTx must use the Tx variants: the pool's
// other connections would wait on the immediate lock (busy_timeout) and fail.
func TestTxVariantsWorkInsideInTx(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(ctx, d, "audit", audit.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "jobs", jobs.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d, "settings", settings.Migrations()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, q, s := audit.New(d, log), jobs.New(d, log), settings.New(d)

	start := time.Now()
	var jobID string
	err = db.InTx(ctx, d, func(tx *sql.Tx) error {
		w.RecordTx(ctx, tx, "content.publish", "content", "01X", nil)
		var err error
		if jobID, err = q.EnqueueTx(ctx, tx, "content.index", map[string]string{"id": "01X"}, jobs.Idempotency("idx-01X")); err != nil {
			return err
		}
		return s.SetTx(ctx, tx, "site.title", "GoRun")
	})
	if err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("InTx stalled for %s", el)
	}
	if _, total, _ := w.List(ctx, audit.ListQuery{}); total != 1 {
		t.Fatalf("want 1 audit row, got %d", total)
	}
	if js, _ := q.List(ctx, "queued", 10); len(js) != 1 || js[0].ID != jobID {
		t.Fatalf("job not committed: %+v", js)
	}
	if again, _ := q.Enqueue(ctx, "content.index", nil, jobs.Idempotency("idx-01X")); again != jobID {
		t.Fatal("idempotency key not honoured across Tx and non-Tx enqueue")
	}
	if got := s.GetString(ctx, "site.title", ""); got != "GoRun" {
		t.Fatalf("setting not committed: %q", got)
	}
}
