package admin

import (
	"context"
	"testing"
)

func TestCorruptSavedInventoryShowsUnavailableInsteadOfEmptySuccess(t *testing.T) {
	h, _, calls := integrationsPerformanceFixture(t, 1, false)
	if _, err := h.d.DB.Exec(`UPDATE observations SET result_json='[]' WHERE kind=?`, kindCFInventory); err != nil {
		t.Fatal(err)
	}
	report, err := h.integrationsReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range report.Inventory {
		if view.Meta.Status != "failed" || view.Meta.ErrorKey == "" || view.Actionable || view.Complete {
			t.Errorf("corrupt %s snapshot looked available: %+v", view.Kind, view)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("read failure triggered a provider request")
	}
}

func TestInventoryReadDoesNotMixNewObservationsWithEarlierConfiguration(t *testing.T) {
	h, _, _ := integrationsPerformanceFixture(t, 1, false)
	ctx := context.Background()
	original, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(ctx, settingCFAccount, "ffffffffffffffffffffffffffffffff"); err != nil {
		t.Fatal(err)
	}
	current, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.DB.Exec(`UPDATE observations SET success_revision=? WHERE kind=?`, current.revision(true), kindCFInventory); err != nil {
		t.Fatal(err)
	}
	for _, view := range h.inventoryViews(ctx, original, nil) {
		if view.Actionable || view.Meta.RevisionMatch == nil || *view.Meta.RevisionMatch || view.Meta.Status != "stale" {
			t.Errorf("mixed-account evidence was treated as current: %+v", view)
		}
	}
}
