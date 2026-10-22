package admin_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

func TestIntegrationsPageRendersPopulatedInventory(t *testing.T) {
	s := boot(t)
	ctx := context.Background()
	enc, err := db.Encrypt(s.app.Deps.Config.SecretKey, []byte(httpCFToken), "integrations.cloudflare.token")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"integrations.cloudflare.token": enc, "integrations.cloudflare.account_id": httpCFAccount} {
		if err := s.app.Deps.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	revision := fmt.Sprintf("%x", sha256.Sum256([]byte(enc+"\x00"+httpCFAccount)))[:16]
	now := time.Now().UTC().Format(time.RFC3339)
	for kind, item := range map[string]map[string]any{
		"r2":      {"id": "bucket-qa", "name": "bucket-qa", "location": "WEUR"},
		"d1":      {"id": "database-qa", "name": "database-qa", "tables": 7, "size_bytes": 4096},
		"workers": {"id": "worker-qa", "name": "worker-qa", "modified_at": now},
	} {
		data, err := json.Marshal(map[string]any{"kind": kind, "items": []any{item}, "count": 1, "complete": true})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.app.Deps.DB.Exec(`INSERT INTO observations(kind,key,success_at,success_revision,success_scope,result_json,updated_at) VALUES(?,?,?,?,?,?,?)`, "integrations.cloudflare.inventory", kind, now, revision, "discovery", string(data), now)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, locale := range []string{"en", "ar"} {
		s.login("inventory-"+locale+"@example.com", "admin", locale)
		resp, page := s.do("GET", "/admin/settings/integrations", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s populated inventory: %d\n%s", locale, resp.StatusCode, page)
		}
		mustContain(t, page, `data-use-bucket="bucket-qa"`, "WEUR", "database-qa", "4096", `/cloudflare/workers/worker-qa/identify`)
		mustNotContain(t, page, httpCFToken, enc)
	}
}
