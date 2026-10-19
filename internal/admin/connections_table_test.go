package admin_test

import (
	"context"
	"github.com/zadstack/gorun/internal/db"
	"strings"
	"testing"
)

func TestConnectionTableHostsOnlyHistoricalMetadataAndNoMutation(t *testing.T) {
	s := boot(t)
	s.login("directory@example.test", "admin", "en")
	for _, kind := range []string{"mcp", "ai", "s3"} {
		id := db.NewID()
		row := map[string]any{"id": id, "version": db.NewID(), "kind": kind, "name": "Synthetic " + kind, "endpoint": "https://archive.example/private-capability?token=QUERY_CAPABILITY", "secret": "ENCRYPTED_SENTINEL", "provider": "openai", "model": "saved-model", "bucket": "draft-archive", "region": "auto", "test_status": "pass", "tested_at": "2026-10-19T10:00:00Z", "test_count": 2, "test_protocol": "2025-11-25"}
		if e := s.app.Deps.Settings.Set(context.Background(), "connections.profile."+id, row); e != nil {
			t.Fatal(e)
		}
	}
	var before int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&before)
	response, body := s.do("GET", "/admin/settings/connections", nil)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(response.StatusCode, response.Header)
	}
	mustContain(t, body, `id="connections-directory-table"`, "archive.example", "Synthetic mcp", "Synthetic ai", "Synthetic s3", "saved-model", "draft-archive", "Saved · not validated here", "Passed · saved result")
	mustNotContain(t, body, "private-capability", "QUERY_CAPABILITY", "ENCRYPTED_SENTINEL", `data-metricool-summary`)
	if strings.Count(body, `data-connection-id=`) != 3 {
		t.Fatal("profiles not unified in table")
	}
	var after int
	s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&after)
	if before != after {
		t.Fatal("directory GET mutated audit")
	}
	s.login("directory-editor@example.test", "editor", "en")
	response, _ = s.do("GET", "/admin/settings/connections", nil)
	if response.StatusCode != 403 {
		t.Fatal("permission bypass", response.StatusCode)
	}
}

func TestConnectionTableEmptyDoesNotInventLegacyStatus(t *testing.T) {
	s := boot(t)
	s.login("empty-directory@example.test", "admin", "ar")
	_, body := s.do("GET", "/admin/settings/connections", nil)
	mustContain(t, body, "لا توجد اتصالات محفوظة", `data-connection-create`)
	mustNotContain(t, body, `data-connection-id=`, `data-metricool-summary`)
}
