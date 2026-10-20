package ext_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/ext"
)

// Real on-disk upgrade from the released schema, with both possible legacy
// installation histories. Nothing in that schema proves their installation method.
func TestInstallSourceUpgradePreservesLegacyRecords(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "legacy.db")
	database, err := db.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { database.Close() }()
	old, err := fs.ReadFile(ext.New().Migrations(), "migrations/0001_ext.sql")
	if err != nil {
		t.Fatal(err)
	}
	legacy := fstest.MapFS{"migrations/0001_ext.sql": &fstest.MapFile{Data: old}}
	if err = db.Migrate(ctx, database, "ext", legacy); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"legacy-upload", "legacy-directory"} {
		raw, _ := json.Marshal(manifest(slug, "https://example.test/hook", hook("content.published", "event", 0)))
		_, err = database.Exec(`INSERT INTO extensions(slug,name,version,manifest_json,status,endpoint_url,secret_enc,api_key_id,failures,total_failures,installed_at,activated_at,last_error) VALUES(?,?,?,?,'disabled',?,'encrypted-preserved','old-key',2,7,'old-install','old-activation','prior-error')`, slug, slug, "1.0.0", string(raw), "https://example.test/hook")
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.Exec(`INSERT INTO extension_subscriptions(extension_slug,hook,kind,timeout_ms) VALUES(?,'content.published','event',1000)`, slug)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = database.Exec(`INSERT INTO extension_deliveries(id,extension_slug,delivery_id,hook,kind,status,http_status,created_at) VALUES('old-delivery','legacy-upload','delivery-1','content.published','event','ok',204,'old-time')`); err != nil {
		t.Fatal(err)
	}
	reopen := func() {
		t.Helper()
		if err = database.Close(); err != nil {
			t.Fatal(err)
		}
		database, err = db.Open(filename)
		if err != nil {
			t.Fatal(err)
		}
	}
	reopen()
	snapshot := func() string {
		t.Helper()
		var raw string
		if err := database.QueryRow(`SELECT json_group_array(json_object('slug',slug,'name',name,'version',version,'manifest',manifest_json,'status',status,'endpoint',endpoint_url,'secret',secret_enc,'key',api_key_id,'failures',failures,'total',total_failures,'installed',installed_at,'activated',activated_at,'error',last_error)) FROM (SELECT * FROM extensions ORDER BY slug)`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot()
	if err = db.Migrate(ctx, database, "ext", ext.New().Migrations()); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		for _, slug := range []string{"legacy-upload", "legacy-directory"} {
			var source, status, secret, key, installed, activated, last string
			var failures, total, subscriptions int
			err = database.QueryRow(`SELECT install_source,status,secret_enc,api_key_id,failures,total_failures,installed_at,activated_at,last_error FROM extensions WHERE slug=?`, slug).Scan(&source, &status, &secret, &key, &failures, &total, &installed, &activated, &last)
			if err != nil {
				t.Fatal(err)
			}
			if source != "legacy_unknown" || status != "disabled" || secret != "encrypted-preserved" || key != "old-key" || failures != 2 || total != 7 || installed != "old-install" || activated != "old-activation" || last != "prior-error" {
				t.Fatalf("legacy provenance guessed or state changed: %s %s", slug, source)
			}
			if err = database.QueryRow(`SELECT count(*) FROM extension_subscriptions WHERE extension_slug=?`, slug).Scan(&subscriptions); err != nil || subscriptions != 1 {
				t.Fatal("subscription changed", err)
			}
		}
	}
	check()
	var delivered int
	if err = database.QueryRow(`SELECT count(*) FROM extension_deliveries WHERE id='old-delivery' AND status='ok' AND http_status=204`).Scan(&delivered); err != nil || delivered != 1 {
		t.Fatal("delivery history changed", err)
	}
	if snapshot() != before {
		t.Fatal("upgrade changed existing extension fields")
	}
	reopen()
	if err = db.Migrate(ctx, database, "ext", ext.New().Migrations()); err != nil {
		t.Fatal(err)
	}
	check()
	if snapshot() != before {
		t.Fatal("restart migration changed existing extension fields")
	}
	if _, err := database.Exec(`UPDATE extensions SET install_source='bundled'`); err == nil {
		t.Fatal("unverified bundled source accepted")
	}
}
