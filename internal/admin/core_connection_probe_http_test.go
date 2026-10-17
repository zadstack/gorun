package admin_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/mailer"
)

func TestCoreConnectionProbeHTTPKeepsConfigurationAndSecrets(t *testing.T) {
	s := boot(t)
	s.login("probe-owner@example.invalid", "admin", "en")
	var calls, connections atomic.Int64
	blocked := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	blocked.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	blocked.StartTLS()
	defer blocked.Close()
	ctx := context.Background()
	for _, kind := range []string{"ai", "s3"} {
		t.Run(kind, func(t *testing.T) {
			id, version := db.NewID(), db.NewID()
			cipher := ""
			if kind == "ai" {
				var err error
				cipher, err = mailer.EncryptSecret(s.app.Deps.Config.SecretKey, `{"token":"synthetic-probe-token"}`)
				if err != nil {
					t.Fatal(err)
				}
			}
			endpoint := blocked.URL
			if kind == "s3" {
				endpoint = "https://objects.example.invalid"
			}
			profile := map[string]any{"id": id, "version": version, "name": "Legacy fixture", "kind": kind, "endpoint": endpoint, "provider": "openai", "model": "stored-not-in-catalog", "bucket": "fixturebucket", "region": "auto", "secret": cipher, "tested_at": "2001-01-01T00:00:00Z", "test_status": "pass", "test_count": 4, "test_protocol": "old"}
			key := "connections.profile." + id
			if err := s.app.Deps.Settings.Set(ctx, key, profile); err != nil {
				t.Fatal(err)
			}
			path := "/admin/settings/connections/" + id + "/test"
			if response, _ := s.do("POST", path, url.Values{"version": {version}}); response.StatusCode != 403 {
				t.Fatal("missing CSRF accepted", response.StatusCode)
			}
			if response, _ := s.post(path, url.Values{"version": {"stale"}}); response.StatusCode != 409 {
				t.Fatal("stale version accepted", response.StatusCode)
			}
			var unchanged map[string]any
			if err := s.app.Deps.Settings.Get(ctx, key, &unchanged); err != nil {
				t.Fatal(err)
			}
			if unchanged["tested_at"] != profile["tested_at"] || unchanged["version"] != version {
				t.Fatal("rejected request changed probe/configuration")
			}
			before := time.Now().UTC()
			response, body := s.post(path, url.Values{"version": {version}})
			if response.StatusCode != http.StatusSeeOther {
				t.Fatal("probe fixture", response.StatusCode)
			}
			if strings.Contains(body, "synthetic-probe-token") || (cipher != "" && strings.Contains(body, cipher)) {
				t.Fatal("probe response leaked secret")
			}
			var saved map[string]any
			if err := s.app.Deps.Settings.Get(ctx, key, &saved); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"id", "version", "name", "kind", "endpoint", "provider", "model", "bucket", "region", "secret"} {
				if saved[field] != profile[field] && !(saved[field] == nil && profile[field] == "") {
					t.Errorf("probe changed configuration field %s", field)
				}
			}
			rawStamp, ok := saved["tested_at"].(string)
			if !ok {
				t.Fatal("missing probe timestamp")
			}
			stamp, err := time.Parse(time.RFC3339, rawStamp)
			count, countOK := saved["test_count"].(float64)
			if raw, exists := saved["test_count"]; exists && (!countOK || count != 0) {
				t.Fatal("failed probe retained count", raw)
			}
			if err != nil || stamp.Before(before.Truncate(time.Second)) || stamp.After(time.Now().UTC()) || saved["test_status"] != "fail" || (saved["test_protocol"] != nil && saved["test_protocol"] != "") {
				t.Fatal("failed probe did not record bounded fresh status", err)
			}
			if s.auditCount("connections.test", id) != 1 {
				t.Fatal("probe audit missing or duplicated")
			}
			if response, page := s.do("GET", "/admin/settings/connections", nil); response.StatusCode != 200 || strings.Contains(page, "synthetic-probe-token") || (cipher != "" && strings.Contains(page, cipher)) {
				t.Fatal("saved profile UI leaks or fails", response.StatusCode)
			}
		})
	}
	if calls.Load() != 0 || connections.Load() != 0 {
		t.Fatal("private endpoint probe reached fixture", calls.Load(), connections.Load())
	}
	var active string
	if err := s.app.Deps.Settings.Get(ctx, "ai.provider", &active); err == nil && active != "" {
		t.Fatal("testing a profile implicitly activated AI")
	}
}

func TestCoreConnectionProbeHTTPCorruptSecretDoesNotClaimProbe(t *testing.T) {
	s := boot(t)
	s.login("probe-corruption@example.invalid", "admin", "en")
	id, version := db.NewID(), db.NewID()
	profile := map[string]any{"id": id, "version": version, "name": "Broken fixture", "kind": "ai", "endpoint": "https://provider.example.invalid/v1", "provider": "openai", "model": "fixture-model", "secret": "invalid-ciphertext-fixture", "tested_at": "2001-01-01T00:00:00Z", "test_status": "pass"}
	key := "connections.profile." + id
	if err := s.app.Deps.Settings.Set(context.Background(), key, profile); err != nil {
		t.Fatal(err)
	}
	response, body := s.post("/admin/settings/connections/"+id+"/test", url.Values{"version": {version}})
	if response.StatusCode != 500 || strings.Contains(body, "invalid-ciphertext-fixture") {
		t.Fatal("corrupt secret leaked or claimed probe", response.StatusCode)
	}
	var saved map[string]any
	if err := s.app.Deps.Settings.Get(context.Background(), key, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["tested_at"] != profile["tested_at"] || saved["version"] != version || s.auditCount("connections.test", id) != 0 {
		t.Fatal("unexecuted probe advanced evidence")
	}
}
