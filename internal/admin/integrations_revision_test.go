package admin

import (
	"context"
	"errors"
	"github.com/zadstack/gorun/internal/observe"
	"net/http"
	"testing"
)

// Keep historical snapshots inspectable without allowing resources from another
// account (or an expired read) to authorize a new identity check.
func TestCloudflareInventoryActionsRequireCurrentEvidence(t *testing.T) {
	for _, mutation := range []string{"account", "token", "expired", "pending"} {
		t.Run(mutation, func(t *testing.T) {
			newFakeCloudflare(t)
			h, app := integrationsHandlers(t)
			ctx := context.Background()
			storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
			h.queueCloudflareChecks(ctx)
			if _, err := h.observe.Refresh(ctx, kindCFInventory, "workers"); err != nil {
				t.Fatal(err)
			}
			runObserveJobs(t, app)
			if !h.workerDiscovered(ctx, "edge-worker") {
				t.Fatal("fresh worker was not actionable")
			}
			switch mutation {
			case "account":
				if err := h.d.Settings.Set(ctx, settingCFAccount, fakeCFOther); err != nil {
					t.Fatal(err)
				}
			case "token":
				storeCloudflare(t, h, fakeCFToken+"NEW", fakeCFAccount)
			case "expired":
				if _, err := h.d.DB.Exec(`UPDATE observations SET success_at = '2000-01-01T00:00:00Z' WHERE kind = ?`, kindCFInventory); err != nil {
					t.Fatal(err)
				}
			case "pending":
				if err := h.d.Settings.Set(ctx, settingCFAccount, fakeCFOther); err != nil {
					t.Fatal(err)
				}
				if _, err := h.observe.Refresh(ctx, kindCFInventory, "workers"); err != nil {
					t.Fatal(err)
				}
			}
			if h.workerDiscovered(ctx, "edge-worker") {
				t.Fatal("historical worker authorized identity check")
			}
			report, err := h.integrationsReport(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, iv := range report.Inventory {
				if iv.Kind != "workers" {
					continue
				}
				found = true
				if len(iv.Items) == 0 {
					t.Fatal("legacy snapshot disappeared")
				}
				if iv.Actionable {
					t.Fatal("historical snapshot marked actionable")
				}
				for _, item := range iv.Items {
					if item.Identity != nil || len(item.Bound) > 0 {
						t.Fatalf("historical identity promoted: %+v", item)
					}
				}
			}
			if !found {
				t.Fatal("workers inventory missing")
			}
		})
	}
}

func TestCloudflareExpiredIdentityIsNotPassed(t *testing.T) {
	newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	h.queueCloudflareChecks(ctx)
	if _, err := h.observe.Refresh(ctx, kindCFInventory, "workers"); err != nil {
		t.Fatal(err)
	}
	runObserveJobs(t, app)
	rev, err := h.cfEdgeRevision(ctx, "edge-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.observe.Record(ctx, kindCFEdge, "edge-worker", rev, observe.Result{Scope: "identity", Data: cfEdgeIdentity{Worker: "edge-worker", SiteID: "demo", ManagerID: "manager-1", Origin: "https://edge.example"}}, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.DB.Exec(`UPDATE observations SET success_at = '2000-01-01T00:00:00Z' WHERE kind = ?`, kindCFEdge); err != nil {
		t.Fatal(err)
	}
	report, err := h.integrationsReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, iv := range report.Inventory {
		if iv.Kind == "workers" {
			for _, item := range iv.Items {
				if item.Name == "edge-worker" {
					found = true
					if item.Checked != "stale" || item.Identity != nil || len(item.Bound) > 0 {
						t.Fatalf("expired identity promoted: %+v", item)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("worker missing")
	}
}

func TestR2BindingRejectsInventoryFromPreviousAccount(t *testing.T) {
	newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	if _, err := h.observe.Refresh(ctx, kindCFInventory, "r2"); err != nil {
		t.Fatal(err)
	}
	runObserveJobs(t, app)
	if !h.cloudflareResourceDiscovered(ctx, "r2", "media-bucket") {
		t.Fatal("current bucket not discovered")
	}
	if err := h.d.Settings.Set(ctx, settingCFAccount, fakeCFOther); err != nil {
		t.Fatal(err)
	}
	_, fields, err := h.bindStorage(ctx, storageBindInput{Source: "r2", Bucket: "media-bucket", AccessKey: "test-access", SecretKey: "test-secret", Purposes: []string{"media"}})
	if err != nil {
		t.Fatal(err)
	}
	if fields["source"] != "admin.integrations.storage.error.inventory" {
		t.Fatalf("old account bucket binding was accepted: %v", fields)
	}
	var count int
	if err := h.d.DB.QueryRow(`SELECT count(*) FROM settings WHERE key LIKE 'storage.destination.%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid binding wrote a destination")
	}
}

func TestCloudflarePendingCheckDoesNotRestoreOldPermissionLamps(t *testing.T) {
	newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	h.queueCloudflareChecks(ctx)
	runObserveJobs(t, app)
	storeCloudflare(t, h, fakeCFToken+"NEW", fakeCFAccount)
	h.queueCloudflareChecks(ctx)
	report, err := h.integrationsReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Cloudflare.AccountsCurrent {
		t.Fatal("accounts from old token marked current")
	}
	if len(report.Cloudflare.Accounts) == 0 {
		t.Fatal("legacy account snapshot disappeared")
	}
	if name := h.cloudflareAccountName(ctx, fakeCFAccount); name != "" {
		t.Fatalf("old account label reused: %q", name)
	}
	for _, scope := range report.Cloudflare.Scopes {
		if scope.Status == "passed" {
			t.Fatalf("old permission displayed as passed while refreshing: %+v", scope)
		}
	}
}

func TestCloudflareDiscoveryCannotSwitchConfigurationMidRequest(t *testing.T) {
	newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(ctx, settingCFAccount, fakeCFOther); err != nil {
		t.Fatal(err)
	}
	if _, err := h.observe.Refresh(ctx, kindCFInventory, "r2"); err != nil {
		t.Fatal(err)
	}
	runObserveJobs(t, app)
	if !h.cloudflareResourceDiscovered(ctx, "r2", "media-bucket") {
		t.Fatal("new account inventory unavailable")
	}
	if h.cloudflareResourceDiscoveredFor(ctx, snapshot, "r2", "media-bucket") {
		t.Fatal("new account inventory authorized old snapshot endpoint")
	}
}

func TestWorkerIdentityRejectsConfigurationChangedDuringRemoteCheck(t *testing.T) {
	newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	if _, err := h.observe.Refresh(ctx, kindCFInventory, "workers"); err != nil {
		t.Fatal(err)
	}
	runObserveJobs(t, app)
	client := &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		if err := h.d.Settings.Set(ctx, settingCFAccount, fakeCFOther); err != nil {
			t.Fatal(err)
		}
		return jsonResponse(200, `{"ok":true,"data":{"site_id":"demo","manager_id":"manager-1","label":"Demo manager","primary":true}}`), nil
	})}
	_, err := h.identifyWorker(ctx, client, "edge-worker", "https://edge.example", "edge-manager-key-value-0123456789ABCDEFGHIJ")
	if !errors.Is(err, errConnectionConflict) {
		t.Fatalf("config drift accepted: %v", err)
	}
	list, err := h.observe.List(ctx, kindCFEdge)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatal("identity saved after configuration changed")
	}
}

func TestR2BindingAcceptsCurrentDiscoveredBucket(t *testing.T) {
	newFakeCloudflare(t)
	h, app := integrationsHandlers(t)
	ctx := context.Background()
	storeCloudflare(t, h, fakeCFToken, fakeCFAccount)
	if _, err := h.observe.Refresh(ctx, kindCFInventory, "r2"); err != nil {
		t.Fatal(err)
	}
	runObserveJobs(t, app)
	result, fields, err := h.bindStorage(ctx, storageBindInput{Source: "r2", Bucket: "media-bucket", AccessKey: "test-access", SecretKey: "test-secret", Purposes: []string{"media"}})
	if err != nil || len(fields) > 0 || result.Destinations["media"] == "" {
		t.Fatalf("current R2 binding failed: %+v %v %v", result, fields, err)
	}
}
