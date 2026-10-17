package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/builder"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/media"
)

func contractDocument(t *testing.T) map[string]any {
	t.Helper()
	app := apitest.Boot(t, content.New(), media.New(), builder.New())
	w := apitest.NewClient(t, app).Anonymous().Do("GET", "/api/v1/openapi.json", nil, nil)
	var doc map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &doc) != nil {
		t.Fatalf("OpenAPI: %d %s", w.Code, w.Body.String())
	}
	return doc
}

func contractOperation(doc map[string]any, path, method string) map[string]any {
	return doc["paths"].(map[string]any)[path].(map[string]any)[method].(map[string]any)
}

func requestSchema(doc, op map[string]any) map[string]any {
	s := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	name := strings.TrimPrefix(s["$ref"].(string), "#/components/schemas/")
	return doc["components"].(map[string]any)["schemas"].(map[string]any)[name].(map[string]any)
}

func TestContentCreateAndPatchHaveDistinctOptionalContracts(t *testing.T) {
	doc := contractDocument(t)
	post := contractOperation(doc, "/v1/content", "post")
	patch := contractOperation(doc, "/v1/content/{id}", "patch")
	createSchema, patchSchema := requestSchema(doc, post), requestSchema(doc, patch)
	if patchSchema["required"] != nil {
		t.Fatalf("PATCH fields must be optional: %v", patchSchema["required"])
	}
	required, _ := createSchema["required"].([]any)
	if len(required) != 1 || required[0] != "type" {
		t.Fatalf("creation requires only its content type: %v", required)
	}
	for _, name := range []string{"title", "fields", "seo", "terms", "menu_order"} {
		prop := patchSchema["properties"].(map[string]any)[name].(map[string]any)
		types, _ := prop["type"].([]any)
		if len(types) != 2 || types[1] != "null" || prop["description"] == nil {
			t.Errorf("%s must document nullable PATCH semantics: %v", name, prop)
		}
	}
	if post["responses"].(map[string]any)["201"] == nil {
		t.Error("content creation must document its actual 201 status")
	}
}

func TestBuilderAndMediaDocumentActualResponsesAndEncoding(t *testing.T) {
	doc := contractDocument(t)
	for _, op := range []map[string]any{
		contractOperation(doc, "/v1/builder/{id}/draft", "put"),
		contractOperation(doc, "/v1/builder/{id}/publish", "post"),
	} {
		if op["responses"].(map[string]any)["409"] == nil {
			t.Error("builder CAS conflict 409 missing")
		}
		if op["x-max-body-bytes"] != float64(1<<20) {
			t.Error("builder limit missing")
		}
	}
	publish := requestSchema(doc, contractOperation(doc, "/v1/builder/{id}/publish", "post"))
	if publish["properties"].(map[string]any)["updated_at"].(map[string]any)["format"] != nil {
		t.Error("opaque builder token must not claim date-time format")
	}

	upload := contractOperation(doc, "/v1/media", "post")
	if upload["requestBody"].(map[string]any)["content"].(map[string]any)["multipart/form-data"] == nil {
		t.Error("media upload must document multipart/form-data")
	}
	uploadBody := upload["requestBody"].(map[string]any)["content"].(map[string]any)["multipart/form-data"].(map[string]any)["schema"].(map[string]any)
	name := strings.TrimPrefix(uploadBody["$ref"].(string), "#/components/schemas/")
	schema := doc["components"].(map[string]any)["schemas"].(map[string]any)[name].(map[string]any)
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "file" || schema["properties"].(map[string]any)["file"].(map[string]any)["format"] != "binary" {
		t.Errorf("multipart file contract: %v", schema)
	}
	for _, status := range []string{"201", "413", "415", "429"} {
		if upload["responses"].(map[string]any)[status] == nil {
			t.Errorf("media upload missing %s", status)
		}
	}
}

func TestOpenAPIDiscoveryIncludesBuildContractCapabilitiesAndLimits(t *testing.T) {
	doc := contractDocument(t)
	meta, ok := doc["x-gorun"].(map[string]any)
	if !ok || meta["core_version"] == "" || meta["api_contract_version"] == nil {
		t.Fatalf("machine-readable build and contract versions missing: %v", meta)
	}
	caps, _ := meta["capabilities"].(map[string]any)
	patch, _ := caps["content_patch"].(map[string]any)
	if patch["merge"] != true || patch["cas"] != false {
		t.Fatalf("PATCH must disclose merge without CAS: %v", patch)
	}
	limits, _ := meta["limits"].(map[string]any)
	if limits["pagination_max_per_page"] != float64(100) {
		t.Fatalf("pagination limit missing: %v", limits)
	}
}

func TestContentPatchPermissionsAndAgentKeyBoundary(t *testing.T) {
	app := apitest.Boot(t, content.New())
	writer := apitest.NewClient(t, app)
	status, env := writer.Post("/content", map[string]any{"type": "post", "title": "original"})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, env)
	}
	path := "/content/" + apitest.Data(env)["id"].(string)
	for _, tc := range []struct {
		name   string
		client *apitest.Client
		status int
		code   string
	}{
		{"anonymous", writer.Anonymous(), 401, "unauthorized"},
		{"missing scope", apitest.NewClient(t, app, "content.view"), 403, "forbidden"},
		{"agent key", apitest.New(t, app, apitest.Options{Kind: "agent"}), 403, "agent_key_not_allowed"},
		{"valid scope", writer, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, env := tc.client.Patch(path, map[string]any{"title": "edited"})
			if status != tc.status || apitest.ErrorCode(env) != tc.code {
				t.Fatalf("PATCH: %d %v", status, env)
			}
		})
	}
}
