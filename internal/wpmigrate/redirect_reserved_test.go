package wpmigrate

import (
	"context"
	"encoding/json"
	"testing"
)

// M3: a WordPress redirect from a path GoRun reserves (/checkout/,
// /account/…) is skipped, not failed, and nothing is saved.
func TestImportRedirectSkipsReservedPath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	m := &migrationRow{ID: "m1", SourceID: "s1"}
	for _, from := range []string{"/checkout/", "/account/orders/"} {
		b, _ := json.Marshal(redirectPayload{From: from, To: "/shop"})
		it := &itemRow{Kind: "redirect", Payload: b}
		if err := e.svc.importRedirect(ctx, m, it); err != nil {
			t.Fatalf("%s: %v", from, err)
		}
		if it.Status != ItemSkipped || it.Error != "reserved_path" {
			t.Fatalf("%s: status=%s error=%s", from, it.Status, it.Error)
		}
	}
	if n := e.count(`SELECT count(*) FROM redirects`); n != 0 {
		t.Fatalf("redirects saved: %d", n)
	}
}
