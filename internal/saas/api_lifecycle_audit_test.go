package saas

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
)

// Full REST lifecycle through the booted kernel: management writes are audited and
// DELETE preserves referenced objects while removing unused ones.
func TestBootedAPILifecycleAuditsAndDeletes(t *testing.T) {
	m := New()
	app := apitest.Boot(t, m)
	admin := apitest.NewClient(t, app, "saas.manage", "saas.read", "saas.use")
	owner := admin.User.ID

	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/saas/assets.css", nil))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") || !strings.Contains(w.Body.String(), "body{") {
		t.Fatalf("assets: %d %q", w.Code, w.Header().Get("Content-Type"))
	}

	status, body := admin.Post("/v1/saas/apps", AppInput{OwnerID: owner, Name: "Audited", Slug: "audited", Status: "active", Mode: "subscription", IdempotencyKey: "audited-app-create-key"})
	if status != 200 {
		t.Fatalf("create app: %d %v", status, body)
	}
	appID, _ := apitest.Data(body)["id"].(string)
	appVersion, _ := apitest.Data(body)["version"].(string)
	status, body = admin.Patch("/v1/saas/apps/"+appID, AppInput{OwnerID: owner, Name: "Audited 2", Slug: "audited", Status: "active", Mode: "subscription", ExpectedVersion: appVersion})
	if status != 200 {
		t.Fatalf("update app: %d %v", status, body)
	}
	status, body = admin.Post("/v1/saas/entitlements", SubscriptionInput{AppID: appID, OwnerID: owner, PlanID: "basic", Status: "active", Limits: map[string]int64{"tasks": 3}, IdempotencyKey: "audited-sub-create-key"})
	if status != 200 {
		t.Fatalf("create sub: %d %v", status, body)
	}
	subID, _ := apitest.Data(body)["id"].(string)
	subVersion, _ := apitest.Data(body)["version"].(string)
	if w = admin.Do("PUT", "/v1/saas/entitlements/"+subID, SubscriptionInput{AppID: appID, OwnerID: owner, PlanID: "pro", Status: "active", Limits: map[string]int64{"tasks": 4}, ExpectedVersion: subVersion}, nil); w.Code != 200 {
		t.Fatalf("update sub: %d %s", w.Code, w.Body.String())
	}
	status, body = admin.Post("/v1/saas/entitlements/"+subID+"/consume", ConsumeRequest{"tasks", 1, "audited-consume-request"})
	if status != 200 {
		t.Fatalf("consume: %d %v", status, body)
	}
	status, body = admin.Patch("/v1/saas/entitlements/"+subID, StatusUpdate{ExpectedStatus: "active", Status: "suspended"})
	if status != 200 {
		t.Fatalf("status: %d %v", status, body)
	}
	// Recorded usage keeps both the subscription and its app.
	if status, body = admin.Delete("/v1/saas/entitlements/"+subID, nil); status != 409 || apitest.ErrorCode(body) != "conflict" {
		t.Fatalf("delete used sub: %d %v", status, body)
	}
	if status, _ = admin.Delete("/v1/saas/apps/"+appID, nil); status != 409 {
		t.Fatalf("delete referenced app: %d", status)
	}

	status, body = admin.Post("/v1/saas/apps", AppInput{OwnerID: owner, Name: "Unused", Slug: "unused", Status: "active", Mode: "subscription", IdempotencyKey: "unused-app-create-key"})
	if status != 200 {
		t.Fatalf("create unused app: %d %v", status, body)
	}
	unusedID, _ := apitest.Data(body)["id"].(string)
	status, body = admin.Delete("/v1/saas/apps/"+unusedID, nil)
	if status != 200 || apitest.Data(body)["id"] != unusedID {
		t.Fatalf("delete unused app: %d %v", status, body)
	}
	var n int
	if e := app.Deps.DB.QueryRow(`SELECT count(*) FROM saas_apps WHERE id=?`, unusedID).Scan(&n); e != nil || n != 0 {
		t.Fatal("unused app remains", n, e)
	}
	for _, action := range []string{"saas.app.update", "saas.subscription.update", "saas.usage.consume", "saas.entitlement.status", "saas.app.delete"} {
		if e := app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action=?`, action).Scan(&n); e != nil || n != 1 {
			t.Fatalf("audit %s: %d %v", action, n, e)
		}
	}
}
