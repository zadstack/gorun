package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/db"
)

func TestCapabilityAPIUsesSavedEvidenceAndPreservesSettings(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	client := apitest.NewClient(t, app, "system.health")
	ctx := context.Background()
	id := db.NewID()
	profile := map[string]any{"id": id, "version": db.NewID(), "name": "PRIVATE_PROFILE_NAME", "kind": "mcp", "endpoint": "https://mcp.example/PRIVATE_ENDPOINT", "test_status": "pass", "tested_at": "2026-10-19T10:00:00Z", "test_count": 10}
	for key, value := range map[string]any{"connections.profile." + id: profile, "site.theme": "custom-theme", "site.title": "Preserved site", "site.identity": map[string]string{"color": "#123456"}} {
		if err := app.Deps.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	before, err := app.Deps.Settings.All(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	w := client.Do("GET", "/site/capabilities", nil, nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response %d: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data admin.CapabilityReport `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	report := envelope.Data
	if len(report.Connections) != 1 || len(report.Modules) == 0 || report.Coverage == "" || report.ObservedAt == "" {
		t.Fatalf("incomplete report: %+v", report)
	}
	c := report.Connections[0]
	if c.Tested || c.Enabled != nil || c.LastCheck == nil || c.LastCheck.Status != "passed" || c.LastCheck.CheckedAt == nil || *c.LastCheck.CheckedAt != profile["tested_at"] || c.LastCheck.ConfigurationMatch != nil {
		t.Fatalf("legacy discovery incorrectly promoted to operational evidence: %+v", c)
	}
	for _, private := range []string{"PRIVATE_PROFILE_NAME", "PRIVATE_ENDPOINT", "mcp.example", "test_count"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("exposed %s", private)
		}
	}
	after, err := app.Deps.Settings.All(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("capability read changed site settings or saved checks")
	}
	var jobs int
	if err = app.Deps.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind LIKE 'connection%'`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("GET enqueued checks: %d %v", jobs, err)
	}
	if status, _ := client.Anonymous().Get("/site/capabilities"); status != http.StatusUnauthorized {
		t.Fatalf("anonymous got %d", status)
	}
	limited := apitest.NewClient(t, app, "content.view")
	if status, _ := limited.Get("/site/capabilities"); status != http.StatusForbidden {
		t.Fatalf("limited key got %d", status)
	}
	if status, body := client.Anonymous().Get("/api/v1/openapi.json"); status != 200 || body["paths"].(map[string]any)["/v1/site/capabilities"] == nil {
		t.Fatal("capability API missing from OpenAPI")
	}
}

func TestCapabilityAPIReadFailureDoesNotReturnEmptySuccess(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	client := apitest.NewClient(t, app, "system.health")
	if err := app.Deps.Settings.Set(context.Background(), "connections.profile.corrupt", "not a profile"); err != nil {
		t.Fatal(err)
	}
	status, body := client.Get("/site/capabilities")
	if status != http.StatusInternalServerError || apitest.ErrorCode(body) != "internal_error" {
		t.Fatalf("failed read fabricated success: %d %+v", status, body)
	}
}
