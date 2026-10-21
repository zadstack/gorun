package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestAPIDocsMatchCurrentOpenAPI(t *testing.T) {
	var out bytes.Buffer
	if err := apiDocsCmd([]string{"--check", "../../docs/API.md"}, &out); err != nil {
		t.Fatal(err)
	}
}

func TestEveryRESTOperationEnforcesAuthenticationAndScopes(t *testing.T) {
	app := bootAPIContractRegistry(t)
	full := apitest.NewClient(t, app)
	anonymous := full.Anonymous()
	agent := apitest.New(t, app, apitest.Options{Kind: "agent"})
	limited := map[string]*apitest.Client{}
	param := regexp.MustCompile(`\{[^}]+\}`)
	permissions := app.Deps.Perms.All()
	for _, op := range app.Deps.API.Operations() {
		if len(op.Scopes) == 0 {
			continue
		}
		for _, p := range permissions {
			if !slices.Contains(op.Scopes, p.Name) {
				if limited[p.Name] == nil {
					limited[p.Name] = apitest.NewClient(t, app, p.Name)
				}
				break
			}
		}
	}
	status, env := full.Post("/content", map[string]any{"type": "post", "title": "permission sentinel", "body": "<p>Unchanged</p>"})
	if status != 201 {
		t.Fatalf("fixture content: %d %v", status, env)
	}
	id := apitest.Data(env)["id"].(string)
	for _, op := range app.Deps.API.Operations() {
		path := param.ReplaceAllString(op.Path, "01K00000000000000000000000")
		path = strings.ReplaceAll(path, "*", "fixture")
		t.Run(op.Method+" "+op.Path, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				client *apitest.Client
				status int
				code   string
			}{
				{"anonymous", anonymous, 401, "unauthorized"}, {"agent", agent, 403, "agent_key_not_allowed"},
			} {
				if tc.name == "anonymous" && slices.Contains(auth.AnonymousAPIPaths, "/api"+path) && len(op.Scopes) == 0 {
					tc.status, tc.code = 200, ""
				}
				w := tc.client.Do(op.Method, path, `{}`, nil)
				var env map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
					t.Fatal(err)
				}
				if w.Code != tc.status || apitest.ErrorCode(env) != tc.code {
					t.Fatalf("%s: %d %v", tc.name, w.Code, env)
				}
			}
			if len(op.Scopes) > 0 {
				for scope, c := range limited {
					if slices.Contains(op.Scopes, scope) {
						continue
					}
					w := c.Do(op.Method, path, `{}`, nil)
					var env map[string]any
					if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
						t.Fatal(err)
					}
					if w.Code != 403 || apitest.ErrorCode(env) != "forbidden" {
						t.Fatalf("missing scope: %d %v", w.Code, env)
					}
					return
				}
				t.Fatal("no unrelated scope available")
			}
		})
	}
	status, env = full.Get("/content/" + id)
	item := apitest.Data(env)
	if status != 200 || item["title"] != "permission sentinel" || item["body"] != "<p>Unchanged</p>" || item["status"] != "draft" {
		t.Fatalf("permission tests changed content: %v", item)
	}
	for _, table := range []string{"media", "builder_documents"} {
		var count int
		if err := app.Deps.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s changed: %d %v", table, count, err)
		}
	}
	t.Logf("Negative permissions tested for all %d registered operations; proper-scope semantic success remains covered by domain fixtures.", len(app.Deps.API.Operations()))
}

func TestOperationTableReportsPublicAccess(t *testing.T) {
	tables, err := apiOperationTables([]byte(`{"paths":{"/v1/site/info":{"get":{"summary":"Public identity","tags":["site"],"security":[]}},"/v1/me":{"get":{"summary":"Principal","tags":["site"]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tables, "| GET | `/api/v1/site/info` | Public identity | public |") {
		t.Fatalf("public endpoint is misdocumented: %s", tables)
	}
	if !strings.Contains(tables, "| GET | `/api/v1/me` | Principal | any key or session |") {
		t.Fatalf("default authentication lost: %s", tables)
	}
}

func TestOpenAPIOperationIDsAndRoutesMatchFullRegistry(t *testing.T) {
	app := bootAPIContractRegistry(t)
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	var doc struct {
		Paths map[string]map[string]struct {
			ID     string   `json:"operationId"`
			Scopes []string `json:"x-scopes"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	count := 0
	for _, methods := range doc.Paths {
		for _, op := range methods {
			if op.ID == "" || ids[op.ID] {
				t.Fatalf("duplicate/empty operation ID %q", op.ID)
			}
			ids[op.ID] = true
			count++
		}
	}
	if count != len(app.Deps.API.Operations()) {
		t.Fatalf("OpenAPI=%d registry=%d", count, len(app.Deps.API.Operations()))
	}
	pathParams := regexp.MustCompile(`\{([^}:]+)(:[^}]*)?\}`)
	for _, op := range app.Deps.API.Operations() {
		path := pathParams.ReplaceAllString(op.Path, "{$1}")
		if strings.HasSuffix(path, "/*") {
			path = strings.TrimSuffix(path, "*") + "{path}"
		}
		if !strings.HasPrefix(path, "/v1") {
			path = "/v1" + path
		}
		documented := doc.Paths[path][strings.ToLower(op.Method)]
		if documented.ID == "" || !slices.Equal(documented.Scopes, op.Scopes) {
			t.Errorf("route/scope mismatch: %s %s", op.Method, path)
		}
	}
	for _, id := range []string{"patch_v1_content_id", "put_v1_builder_id_draft", "post_v1_builder_id_publish", "post_v1_media"} {
		if !ids[id] {
			t.Errorf("stable ID missing %s", id)
		}
	}
	if api.ContractVersion == "" {
		t.Fatal("empty contract version")
	}
}

func TestCLIPropagatesCoreBuildVersion(t *testing.T) {
	oldVersion, oldCoreVersion := version, api.CoreVersion
	t.Cleanup(func() { version, api.CoreVersion = oldVersion, oldCoreVersion })
	version = "v0.1.0-gorun.21-fixture"
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	app := bootAPIContractRegistry(t)
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	var doc struct {
		Meta struct {
			CoreVersion string `json:"core_version"`
		} `json:"x-gorun"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Meta.CoreVersion != version {
		t.Fatalf("core version %q want %q", doc.Meta.CoreVersion, version)
	}
}

func bootAPIContractRegistry(t *testing.T) *kernel.App {
	t.Helper()
	kernel.LogOutput = io.Discard
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", BaseURL: "http://localhost", Theme: "default", SecretKey: make([]byte, 32)}
	app, err := kernel.BootWithOptions(context.Background(), cfg, Modules(), kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}

func TestAPIDocsIgnoresProviderAndProductionEnvironment(t *testing.T) {
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_SECRET_KEY", "invalid production key fixture")
	t.Setenv("GORUN_S3_ENDPOINT", "https://must-not-contact.invalid")
	t.Setenv("STRIPE_SECRET_KEY", "never-use-fixture")
	var out bytes.Buffer
	if err := apiDocsCmd(nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "operations**") {
		t.Fatalf("unexpected generated table: %s", out.String())
	}
}

func TestTemplateEditorOpenAPIContract(t *testing.T) {
	app := bootAPIContractRegistry(t)
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	if w.Code != 200 {
		t.Fatalf("OpenAPI: %d", w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	list := schemas["TemplateListDTO"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"templates", "overrides"} {
		if list[name].(map[string]any)["type"] != "array" {
			t.Fatalf("untyped %s", name)
		}
	}
	paths := doc["paths"].(map[string]any)
	draft := paths["/v1/templates/{name}/draft"].(map[string]any)["put"].(map[string]any)
	if limit, ok := draft["x-max-body-bytes"].(float64); !ok || limit <= 0 {
		t.Fatal("draft byte limit missing")
	}
	response := draft["responses"].(map[string]any)["400"]
	raw, _ := json.Marshal(response)
	if !bytes.Contains(raw, []byte("bad_request")) || !bytes.Contains(raw, []byte("non-null")) {
		t.Fatalf("incomplete draft error contract: %s", raw)
	}
}
