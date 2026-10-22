package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func capabilityReport(t *testing.T, client *apitest.Client) admin.CapabilityReport {
	t.Helper()
	w := client.Do("GET", "/site/capabilities", nil, nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data admin.CapabilityReport `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data
}

func connectionState(r admin.CapabilityReport, id string) admin.CapabilityState {
	for _, c := range r.Connections {
		if c.ID == id {
			return c
		}
	}
	return admin.CapabilityState{}
}

func drain(t *testing.T, app *kernel.App) {
	t.Helper()
	for i := 0; i < 10; i++ {
		ran, err := app.Deps.Jobs.RunOne(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !ran {
			return
		}
	}
}

func saveProfile(t *testing.T, app *kernel.App, id string, profile map[string]any) {
	t.Helper()
	if err := app.Deps.Settings.Set(context.Background(), "connections.profile."+id, profile); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityOperationTestsRunInBackgroundWithIsolatedSamples(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	ctx := context.Background()
	// Fake OpenAI-compatible model on the loopback address (the only plain-HTTP exception).
	var aiCalls atomic.Int32
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aiCalls.Add(1)
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"probe-model"}]}`)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/chat/completions" || body["max_tokens"] != float64(16) || r.Header.Get("Authorization") != "Bearer private-ai-token" {
			t.Errorf("unexpected AI request %s %v", r.URL.Path, body)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer ai.Close()
	// Fake bucket: objects live in memory and must be deleted by the test.
	objects := map[string][]byte{}
	var s3Calls []string
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s3Calls = append(s3Calls, r.Method+" "+r.URL.Path)
		switch r.Method {
		case "PUT":
			b := make([]byte, r.ContentLength)
			r.Body.Read(b)
			objects[r.URL.Path] = b
		case "GET":
			if b, ok := objects[r.URL.Path]; ok {
				w.Write(b)
			} else {
				w.WriteHeader(404)
			}
		case "DELETE":
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	defer s3.Close()
	h := admin.New()
	_ = h
	helper := &admin.TestHelper{App: app}
	aiID, s3ID, mcpID := db.NewID(), db.NewID(), db.NewID()
	saveProfile(t, app, aiID, map[string]any{"id": aiID, "version": "v1", "name": "Model", "kind": "ai", "provider": "openai", "endpoint": ai.URL + "/v1", "model": "probe-model", "secret": helper.EncryptSecrets(t, "private-ai-token", "", "")})
	saveProfile(t, app, s3ID, map[string]any{"id": s3ID, "version": "v1", "name": "Bucket", "kind": "s3", "endpoint": s3.URL, "bucket": "probe-bucket", "region": "auto", "secret": helper.EncryptSecrets(t, "", "AKIAPROBE", "probe-secret")})
	saveProfile(t, app, mcpID, map[string]any{"id": mcpID, "version": "v1", "name": "Tools", "kind": "mcp", "endpoint": "https://mcp.example.invalid"})

	tester := apitest.NewClient(t, app, "system.health", admin.PermCapabilityTest)
	report := capabilityReport(t, tester)
	for _, id := range []string{aiID, s3ID} {
		if st := connectionState(report, id); st.Tested || st.Operation != nil || st.Connected != nil || !st.OperationSupported {
			t.Fatalf("invented evidence before any test: %+v", st)
		}
	}
	if st := connectionState(report, mcpID); st.OperationSupported {
		t.Fatal("MCP reported an operation test")
	}
	if aiCalls.Load() != 0 || len(s3Calls) != 0 {
		t.Fatal("GET contacted a provider")
	}
	// Queue both operation tests; a repeat is deduplicated.
	for _, id := range []string{aiID, s3ID} {
		w := tester.Do("POST", "/site/capabilities/connections/"+id+"/operation", nil, nil)
		if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"deduplicated":false`) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if w := tester.Do("POST", "/site/capabilities/connections/"+id+"/operation", nil, nil); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"deduplicated":true`) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if w := tester.Do("POST", "/site/capabilities/connections/"+mcpID+"/operation", nil, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "operation_unsupported") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if w := tester.Do("POST", "/site/capabilities/connections/"+db.NewID()+"/operation", nil, nil); w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}
	drain(t, app)
	report = capabilityReport(t, tester)
	for _, id := range []string{aiID, s3ID} {
		st := connectionState(report, id)
		if !st.Tested || st.Operation == nil || st.Operation.Status != "passed" || st.Operation.Kind != "operation" || st.Operation.ExpiresAt == nil || st.Operation.ConfigurationMatch == nil || !*st.Operation.ConfigurationMatch {
			t.Fatalf("%s: %+v op=%+v", id, st, st.Operation)
		}
		if st.Reason != "operation_verified" {
			t.Fatalf("%+v", st)
		}
	}
	if connectionState(report, aiID).Operation.Scope != "operation:complete" || connectionState(report, s3ID).Operation.Scope != "operation:write_read_delete" {
		t.Fatal("scopes not recorded")
	}
	if aiCalls.Load() != 1 {
		t.Fatalf("ai calls %d", aiCalls.Load())
	}
	if len(objects) != 0 || len(s3Calls) != 3 || !strings.Contains(s3Calls[0], "/gorun-capability-probe-") || !strings.HasPrefix(s3Calls[0], "PUT /probe-bucket/") || !strings.HasPrefix(s3Calls[2], "DELETE ") {
		t.Fatalf("probe object not isolated/cleaned: %v leftover=%d", s3Calls, len(objects))
	}
	body := tester.Do("GET", "/site/capabilities", nil, nil).Body.String()
	for _, secret := range []string{"private-ai-token", "probe-secret", "AKIAPROBE", "Bucket", "Model", s3.URL, ai.URL} {
		if strings.Contains(body, secret) {
			t.Fatalf("report exposed %q", secret)
		}
	}
	// Editing the profile invalidates the evidence without deleting it.
	var profile map[string]any
	app.Deps.Settings.Get(ctx, "connections.profile."+aiID, &profile)
	profile["version"] = "v2"
	saveProfile(t, app, aiID, profile)
	st := connectionState(capabilityReport(t, tester), aiID)
	if st.Tested || st.Operation == nil || st.Operation.Status != "stale" || st.Operation.Result != "passed" {
		t.Fatalf("edited configuration kept certification: %+v", st.Operation)
	}
	// Expiry: an old success stops counting but stays recorded.
	app.Deps.DB.Exec(`UPDATE observations SET success_at = '2026-01-01T00:00:00Z', success_revision = 'v1' WHERE kind = 'operation.s3' AND key = ?`, s3ID)
	st = connectionState(capabilityReport(t, tester), s3ID)
	if st.Tested || st.Operation.Status != "expired" || st.Operation.Result != "passed" || st.Operation.ExpiresAt == nil {
		t.Fatalf("%+v", st.Operation)
	}
	// Permissions: the health scope reads but cannot test; settings.manage checks but cannot run operations.
	reader := apitest.NewClient(t, app, "system.health")
	if w := reader.Do("POST", "/site/capabilities/connections/"+aiID+"/operation", nil, nil); w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}
	if w := reader.Do("POST", "/site/capabilities/connections/"+aiID+"/check", nil, nil); w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}
	settings := apitest.NewClient(t, app, "settings.manage")
	if w := settings.Do("POST", "/site/capabilities/connections/"+aiID+"/operation", nil, nil); w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}
	if w := settings.Do("POST", "/site/capabilities/connections/"+aiID+"/check", nil, nil); w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	drain(t, app)
	// The connectivity check applies the strict profile validation (a
	// loopback HTTP endpoint is not a valid saved connection), so it fails
	// honestly: reflected as connected=false, never promoted to tested.
	st = connectionState(capabilityReport(t, tester), aiID)
	if st.Connected == nil || *st.Connected || st.LastCheck == nil || st.LastCheck.Status != "failed" || st.LastCheck.Kind != "connectivity" || st.Tested || st.LastCheck.CheckedAt == nil {
		t.Fatalf("connectivity check not reflected or promoted: %+v", st.LastCheck)
	}
	app.Deps.Settings.Get(ctx, "connections.profile."+aiID, &profile)
	if profile["test_status"] != "fail" || profile["test_version"] != "v2" {
		t.Fatalf("legacy probe fields not kept in step: %v", profile)
	}
}

func TestCapabilityMailServiceEvidence(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	ctx := context.Background()
	tester := apitest.NewClient(t, app, "system.health", admin.PermCapabilityTest)
	report := capabilityReport(t, tester)
	if len(report.Services) != 1 || report.Services[0].ID != "mail" || *report.Services[0].Configured || report.Services[0].Tested {
		t.Fatalf("%+v", report.Services)
	}
	if w := tester.Do("POST", "/site/capabilities/services/mail/operation", nil, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "capability_disabled") {
		t.Fatalf("unconfigured mail accepted a test: %d %s", w.Code, w.Body.String())
	}
	// Configure SMTP against a closed port: the operation runs in the
	// background, fails honestly and never claims delivery.
	for k, v := range map[string]any{"mail.host": "127.0.0.1", "mail.port": 1, "mail.from": "site@example.invalid"} {
		if err := app.Deps.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	w := tester.Do("POST", "/site/capabilities/services/mail/operation", nil, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	start := time.Now()
	drain(t, app)
	if time.Since(start) > 25*time.Second {
		t.Fatal("mail test did not respect its timeout")
	}
	svc := capabilityReport(t, tester).Services[0]
	if !*svc.Configured || svc.Tested || svc.Operation == nil || svc.Operation.Status != "failed" || svc.Operation.Result != "failed" || strings.Contains(svc.Operation.Reason, "127.0.0.1") {
		t.Fatalf("%+v op=%+v", svc, svc.Operation)
	}
	if w := apitest.NewClient(t, app, "system.health").Do("POST", "/site/capabilities/services/mail/operation", nil, nil); w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}
}

func TestCapabilityModulesReportPublishedRequirements(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	app.Deps.Hooks.Filter(admin.ModuleRequirementFilter, 10, func(_ context.Context, v any) (any, error) {
		list := v.(*[]admin.ModuleRequirement)
		*list = append(*list, admin.ModuleRequirement{Module: "admin", Configured: false, Reason: "example_missing", Remediation: "Set the example."})
		return list, nil
	})
	report := capabilityReport(t, apitest.NewClient(t, app, "system.health"))
	for _, m := range report.Modules {
		if m.ID == "admin" {
			if m.Configured == nil || *m.Configured || m.ConfigurationStatus != "incomplete" || m.Reason != "example_missing" || m.Remediation != "Set the example." || m.Source != "module.requirements" {
				t.Fatalf("%+v", m)
			}
			return
		}
	}
	t.Fatal("admin module missing")
}
