package admin_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
)

func TestEdgeManagerDiscoveryReadsDoNotChangeSettingsOrQueueJobs(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	client := apitest.NewClient(t, app, "settings.manage")
	ctx := context.Background()
	before, err := app.Deps.Settings.All(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var queuedBefore int
	if err = app.Deps.DB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&queuedBefore); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/site/edge/instances/capabilities", "/site/edge/instances", "/site/edge/instances/plans"} {
		response := client.Do("GET", path, nil, nil)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		if path == "/site/edge/instances/capabilities" {
			var body struct {
				Data struct {
					Contract                          string `json:"contract"`
					Create, Independent, Experimental bool
				}
			}
			if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.Contract != "edge-instances.v1" || !body.Data.Create || !body.Data.Independent || !body.Data.Experimental {
				t.Fatalf("capabilities do not describe independent experimental provisioning: %s", response.Body.String())
			}
		}
	}
	after, err := app.Deps.Settings.All(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var queuedAfter int
	if err = app.Deps.DB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&queuedAfter); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || queuedBefore != queuedAfter {
		t.Fatal("discovery read changed configuration or queued provider operations")
	}
}

func TestEdgeManagerRoutesRequireSettingsPermissionAndPublishContract(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	anonymous := apitest.NewClient(t, app, "system.health").Anonymous()
	health := apitest.NewClient(t, app, "system.health")
	paths := []string{"/site/edge/instances/capabilities", "/site/edge/instances", "/site/edge/instances/plans"}
	for _, path := range paths {
		if w := anonymous.Do("GET", path, nil, nil); w.Code != 401 {
			t.Fatalf("anonymous %s=%d", path, w.Code)
		}
		if w := health.Do("GET", path, nil, nil); w.Code != 403 {
			t.Fatalf("health-only %s=%d", path, w.Code)
		}
	}
	w := anonymous.Do("GET", "/api/v1/openapi.json", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/v1/site/edge/instances/plans/{id}/execute") {
		t.Fatal("creation contract is not discoverable in OpenAPI")
	}
}
