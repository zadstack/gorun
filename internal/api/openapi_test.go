package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

type createIn struct {
	Title  string         `json:"title" doc:"Title"`
	Status content.Status `json:"status" enum:"draft,published"`
}

func noop(http.ResponseWriter, *http.Request) {}

// syntheticOps covers both path conventions, chi regex params, generics,
// colliding type names, recursive types, nil schemas and a body on GET.
func syntheticOps() []kernel.APIOperation {
	list := Op("GET", "/content", "List content", []string{"content"}, []string{"content.view"}, nil, []content.Item{}, noop)
	list.Params = append(list.Params, PageParams...)
	return []kernel.APIOperation{
		list,
		Op("POST", "/v1/content", "Create content", []string{"content"}, []string{"content.create"}, createIn{}, content.Item{}, noop),
		Op("GET", "/content/{id}", "Get content", []string{"content"}, nil, nil, content.Item{}, noop),
		Op("PATCH", "/v1/content/{id:[0-9A-Z]+}", "Update content", []string{"content"}, []string{"content.edit"}, content.Input{}, content.Item{}, noop),
		Op("DELETE", "/content/{id}", "Delete content", []string{"content"}, []string{"content.delete"}, createIn{}, nil, noop),
		Op("GET", "/pages", "Paged", nil, nil, struct{ Q string }{}, content.Page[content.Item]{}, noop),
		Op("GET", "/menus/{slug}/items/{item}", "Menu items", []string{"menus"}, nil, nil, []menus.Item{}, noop),
		{Method: "get", Path: "/v1/raw", Handler: noop, Params: []kernel.APIParam{{Name: "q", Type: "string"}, {Name: "X-Trace", In: "header"}}},
	}
}

func buildDoc(t *testing.T, ops []kernel.APIOperation) (map[string]any, []byte) {
	t.Helper()
	b, err := json.Marshal(buildOpenAPI(ops, "https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, b
}

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

func TestOpenAPIDocumentValid(t *testing.T) {
	doc, raw := buildDoc(t, syntheticOps())

	// libopenapi must parse and build a v3 model without errors. Array
	// self-references (menus.Item.Children) are finite (an empty array ends
	// them), which libopenapi only accepts with IgnoreArrayCircularReferences;
	// every other circular reference still fails the build.
	d, err := libopenapi.NewDocumentWithConfiguration(raw, &datamodel.DocumentConfiguration{IgnoreArrayCircularReferences: true})
	if err != nil {
		t.Fatalf("libopenapi parse: %v", err)
	}
	model, err := d.BuildV3Model()
	if err != nil {
		t.Fatalf("libopenapi build: %v", err)
	}
	if model.Model.Paths.PathItems.Len() == 0 {
		t.Fatal("no paths in model")
	}

	// Structural checks libopenapi does not enforce.
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi version: %v", doc["openapi"])
	}
	info := doc["info"].(map[string]any)
	if info["title"] != "GoRun API" || info["version"] != Version {
		t.Fatalf("info: %v", info)
	}
	if srv := doc["servers"].([]any)[0].(map[string]any)["url"]; srv != "https://example.com/api" {
		t.Fatalf("server url: %v", srv)
	}
	schemes := doc["components"].(map[string]any)["securitySchemes"].(map[string]any)
	if schemes["bearerAuth"] == nil || schemes["cookieAuth"].(map[string]any)["name"] != "gp_session" {
		t.Fatalf("security schemes: %v", schemes)
	}
	ids := map[string]bool{}
	for p, item := range doc["paths"].(map[string]any) {
		if strings.Contains(p, ":") {
			t.Errorf("chi regex left in path %s", p)
		}
		ops := item.(map[string]any)
		if len(ops) == 0 {
			t.Errorf("path %s has no operations", p)
		}
		for method, o := range ops {
			op := o.(map[string]any)
			id, _ := op["operationId"].(string)
			if id == "" || ids[id] {
				t.Errorf("%s %s: missing or duplicate operationId %q", method, p, id)
			}
			ids[id] = true
			declared := map[string]bool{}
			params, _ := op["parameters"].([]any)
			for _, pa := range params {
				pm := pa.(map[string]any)
				if pm["in"] == "path" {
					if pm["required"] != true {
						t.Errorf("%s %s: path param %v not required", method, p, pm["name"])
					}
					declared[pm["name"].(string)] = true
				}
			}
			for _, m := range pathParamRe.FindAllStringSubmatch(p, -1) {
				if !declared[m[1]] {
					t.Errorf("%s %s: path param %s not declared", method, p, m[1])
				}
			}
			if (method == "get" || method == "delete") && op["requestBody"] != nil {
				t.Errorf("%s %s: requestBody on %s", method, p, method)
			}
			resp := op["responses"].(map[string]any)
			for _, code := range []string{"200", "400", "401", "403", "404", "405", "422", "429", "500"} {
				if resp[code] == nil {
					t.Errorf("%s %s: missing %s response", method, p, code)
				}
			}
		}
	}

	// Every $ref resolves inside the document.
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if r, ok := x["$ref"].(string); ok && !resolves(doc, r) {
				t.Errorf("unresolved $ref %s", r)
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(doc)
	if strings.Contains(string(raw), `"nullable"`) {
		t.Error("3.0 nullable keyword in a 3.1 document")
	}
}

func resolves(doc map[string]any, ref string) bool {
	if !strings.HasPrefix(ref, "#/components/") {
		return false
	}
	var cur any = doc
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[part]; !ok {
			return false
		}
	}
	return true
}

func TestOpenAPIListsEveryRegisteredOperation(t *testing.T) {
	ops := syntheticOps()
	doc, _ := buildDoc(t, ops)
	paths := doc["paths"].(map[string]any)
	n := 0
	for _, item := range paths {
		n += len(item.(map[string]any))
	}
	if n != len(ops) {
		t.Fatalf("documented %d operations, registered %d", n, len(ops))
	}
	want := map[string]string{
		"/v1/content":                   "get",
		"/v1/content/{id}":              "patch",
		"/v1/pages":                     "get",
		"/v1/menus/{slug}/items/{item}": "get",
		"/v1/raw":                       "get",
	}
	for p, m := range want {
		item, ok := paths[p].(map[string]any)
		if !ok || item[m] == nil {
			t.Errorf("missing %s %s in %v", m, p, keys(paths))
		}
	}
	post := paths["/v1/content"].(map[string]any)["post"].(map[string]any)
	if post["summary"] != "Create content" || post["tags"].([]any)[0] != "content" {
		t.Fatalf("post op: %v", post)
	}
	if s := post["x-scopes"].([]any); len(s) != 1 || s[0] != "content.create" {
		t.Fatalf("scopes: %v", post["x-scopes"])
	}
	body := post["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if body["$ref"] != "#/components/schemas/createIn" {
		t.Fatalf("request schema: %v", body)
	}
	ok200 := post["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	data := ok200["properties"].(map[string]any)["data"].(map[string]any)
	if data["$ref"] != "#/components/schemas/ContentItem" {
		t.Fatalf("response data schema: %v", data)
	}
	comps := doc["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"ContentItem", "MenusItem", "PageItem", "Input", "Error", "Meta"} {
		if comps[name] == nil {
			t.Errorf("missing component %s in %v", name, keys(comps))
		}
	}
	listParams := paths["/v1/content"].(map[string]any)["get"].(map[string]any)["parameters"].([]any)
	if len(listParams) != len(PageParams) {
		t.Fatalf("list params: %v", listParams)
	}
}
