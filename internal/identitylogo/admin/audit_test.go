package identitylogoadmin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/identitylogo"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestAuditAdapterPersistsApprovalContractMetadata(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx, database, "audit", audit.Migrations()); err != nil {
		t.Fatal(err)
	}
	writer := audit.New(database, slog.New(slog.NewTextHandler(io.Discard, nil)))
	hooks := bridgeHooks(&kernel.Deps{Audit: writer})
	hooks.Record(ctx, identitylogo.AuditEvent{Action: "generation", Outcome: "approval_rejected", Owner: "actual-caller", Theme: "default", ApprovalHash: "approval-digest", PayloadHash: "payload-digest", DestinationFingerprint: "destination-digest", CapabilityFingerprint: "capability-digest", QuoteHash: "quote-digest", QuoteVersion: "quote-v2", DraftRevisionHash: "revision-digest"})
	entries, total, err := writer.List(ctx, audit.ListQuery{})
	if err != nil || total != 1 {
		t.Fatalf("audit rows=%d err=%v", total, err)
	}
	var meta map[string]any
	if err := json.Unmarshal(entries[0].Meta, &meta); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"owner": "actual-caller", "approval_hash": "approval-digest", "payload_hash": "payload-digest", "destination_fingerprint": "destination-digest", "capability_fingerprint": "capability-digest", "quote_hash": "quote-digest", "quote_version": "quote-v2", "draft_revision_hash": "revision-digest"} {
		if meta[key] != want {
			t.Errorf("stored %s=%v want %s", key, meta[key], want)
		}
	}
	for _, forbidden := range []string{"prompt", "payload", "token", "revision", "prepared", "secret"} {
		if _, ok := meta[forbidden]; ok {
			t.Errorf("raw contract field persisted: %s", forbidden)
		}
	}
}
