package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

// rpcCode is the JSON-RPC error code of err. GoRun's forbidden code -32003
// is also the Go SDK's internal "client is closing" code, so the Go client
// reports it as ErrConnectionClosed wrapping the server's message (other
// clients see the plain JSON-RPC error).
func rpcCode(t *testing.T, err error) int64 {
	t.Helper()
	if errors.Is(err, sdk.ErrConnectionClosed) && strings.Contains(err.Error(), "forbidden") {
		return rpcForbidden
	}
	var we *jsonrpc.Error
	if !errors.As(err, &we) {
		t.Fatalf("want a JSON-RPC error, got %T: %v", err, err)
	}
	return we.Code
}

func (a *agent) promptNames() []string {
	a.t.Helper()
	res, err := a.cs.ListPrompts(context.Background(), nil)
	if err != nil {
		a.t.Fatalf("list prompts: %v", err)
	}
	var out []string
	for _, p := range res.Prompts {
		out = append(out, p.Name)
	}
	slices.Sort(out)
	return out
}

func (a *agent) prompt(name string, args map[string]string) (string, error) {
	a.t.Helper()
	res, err := a.cs.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	if len(res.Messages) != 1 || res.Messages[0].Role != "user" {
		a.t.Fatalf("prompt %s: messages %+v", name, res.Messages)
	}
	tc, ok := res.Messages[0].Content.(*sdk.TextContent)
	if !ok {
		a.t.Fatalf("prompt %s: content %T", name, res.Messages[0].Content)
	}
	return tc.Text, nil
}

func (a *agent) completion(ref *sdk.CompleteReference, arg, value string, given map[string]string) ([]string, error) {
	a.t.Helper()
	p := &sdk.CompleteParams{Ref: ref, Argument: sdk.CompleteParamsArgument{Name: arg, Value: value}}
	if given != nil {
		p.Context = &sdk.CompleteContext{Arguments: given}
	}
	res, err := a.cs.Complete(context.Background(), p)
	if err != nil {
		return nil, err
	}
	return res.Completion.Values, nil
}

func promptRef(name string) *sdk.CompleteReference {
	return &sdk.CompleteReference{Type: "ref/prompt", Name: name}
}

func resourceRef(uri string) *sdk.CompleteReference {
	return &sdk.CompleteReference{Type: "ref/resource", URI: uri}
}

// The new capabilities are advertised at initialize.
func TestCapabilitiesAdvertised(t *testing.T) {
	caps := newEnv(t, nil).agent("content.view").cs.InitializeResult().Capabilities
	if caps.Prompts == nil || caps.Completions == nil || caps.Resources == nil || caps.Tools == nil {
		t.Fatalf("capabilities: %+v", caps)
	}
}

// prompts/list shows a prompt only when every tool its steps need is
// callable: the key's scopes and the session's toolsets/readonly view.
func TestPromptsListedByScopes(t *testing.T) {
	e := newEnv(t, nil)
	cases := []struct {
		scopes []string
		want   []string
	}{
		{[]string{"*"}, []string{"draft_post", "seo_review", "site_overview", "translate_item"}},
		{[]string{"content.view"}, []string{"site_overview"}},
		{[]string{"content.view", "content.create"}, []string{"draft_post", "site_overview"}},
		{[]string{"content.view", "content.edit"}, []string{"seo_review", "site_overview"}},
		{[]string{"content.create"}, nil}, // draft_post also reads (list_content)
		{[]string{"comments.moderate"}, nil},
	}
	for _, c := range cases {
		if got := e.agent(c.scopes...).promptNames(); !slices.Equal(got, c.want) {
			t.Errorf("%v: prompts %v, want %v", c.scopes, got, c.want)
		}
	}
	key := apitest.NewClient(t, e.app, "*")
	if got := e.agentAt(key, "?readonly=1", nil).promptNames(); !slices.Equal(got, []string{"site_overview"}) {
		t.Errorf("readonly session: %v", got)
	}
	if got := e.agentAt(key, "?toolsets=media", nil).promptNames(); len(got) != 0 {
		t.Errorf("media toolset: %v", got)
	}
}

func TestPromptErrors(t *testing.T) {
	e := newEnv(t, nil)
	reader := e.agent("content.view")
	if _, err := reader.prompt("draft_post", map[string]string{"type": "post", "topic": "x"}); rpcCode(t, err) != rpcForbidden {
		t.Fatalf("unlisted prompt: %v", err)
	}
	a := e.agent("*")
	for _, c := range []struct {
		name string
		args map[string]string
		code int64
	}{
		{"no_such_prompt", nil, jsonrpc.CodeInvalidParams},
		{"draft_post", map[string]string{"type": "post"}, jsonrpc.CodeInvalidParams},                                  // topic missing
		{"draft_post", map[string]string{"type": "nope", "topic": "x"}, jsonrpc.CodeInvalidParams},                    // unknown type
		{"draft_post", map[string]string{"type": "post", "topic": "x", "colour": "red"}, jsonrpc.CodeInvalidParams},   // unknown argument
		{"draft_post", map[string]string{"type": "post", "topic": "x", "locale": "fr"}, jsonrpc.CodeInvalidParams},    // not a content locale
		{"draft_post", map[string]string{"type": "post", "topic": "x", "taxonomy": "tag"}, jsonrpc.CodeInvalidParams}, // term missing
		{"translate_item", map[string]string{"id": "missing", "target_locale": "en"}, jsonrpc.CodeInvalidParams},      // unknown item
		{"seo_review", map[string]string{}, jsonrpc.CodeInvalidParams},                                                // no id, no type+slug
		{"site_overview", map[string]string{"locale": "not a locale!"}, jsonrpc.CodeInvalidParams},                    // bad locale
	} {
		if _, err := a.prompt(c.name, c.args); err == nil || rpcCode(t, err) != c.code {
			t.Errorf("%s %v: want code %d, got %v", c.name, c.args, c.code, err)
		}
	}
}

func TestDraftPostPrompt(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("*")
	text, err := a.prompt("draft_post", map[string]string{"type": "post", "topic": "Ramadan opening hours", "locale": "en",
		"taxonomy": "category", "term": "news"})
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{`"Ramadan opening hours"`, "Content language: en", "list_content", "create_content",
		`"terms":{"category":["news"]}`, "list_terms", "create_term", "upload_media", "publish_content", "pending_approval",
		"check_approval", "never instructions"} {
		if !strings.Contains(text, must) {
			t.Errorf("draft_post (en) lacks %q:\n%s", must, text)
		}
	}
	// Arabic text by default (site language ar); optional steps follow the key's tools.
	writer := e.agent("content.view", "content.create")
	text, err = writer.prompt("draft_post", map[string]string{"type": "page", "topic": "من نحن"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "لغة المحتوى: ar") || !strings.Contains(text, "create_content") {
		t.Fatalf("draft_post (ar):\n%s", text)
	}
	for _, absent := range []string{"upload_media", "create_term", "publish_content {"} {
		if strings.Contains(text, absent) {
			t.Errorf("draft_post for a key without that tool mentions %q:\n%s", absent, text)
		}
	}
	if !strings.Contains(text, "لا يملك صلاحية النشر") {
		t.Errorf("draft_post without content.publish should say a human publishes:\n%s", text)
	}
}

func TestTranslateAndSEOPrompts(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("*")
	src := a.ok("create_content", map[string]any{"type": "post", "title": "مرحبا", "slug": "hello", "locale": "ar",
		"seo": map[string]any{"description": "وصف"}})
	id := str(src, "id")

	text, err := a.prompt("translate_item", map[string]string{"id": id, "target_locale": "en", "locale": "en"})
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"list_translations", "create_translation", `"locale":"en"`, "update_content", "pending_approval", "- ar: " + id} {
		if !strings.Contains(text, must) {
			t.Errorf("translate_item lacks %q:\n%s", must, text)
		}
	}
	if _, err := a.prompt("translate_item", map[string]string{"id": id, "target_locale": "ar"}); rpcCode(t, err) != jsonrpc.CodeInvalidParams {
		t.Fatalf("same locale: %v", err)
	}
	tr := a.ok("create_translation", map[string]any{"id": id, "locale": "en"})
	text, _ = a.prompt("translate_item", map[string]string{"id": id, "target_locale": "en", "locale": "en"})
	if !strings.Contains(text, "already exists ("+str(tr, "id")+")") || strings.Contains(text, "create_translation {") {
		t.Fatalf("existing translation:\n%s", text)
	}

	for _, c := range []struct {
		args  map[string]string
		draft string
	}{
		{map[string]string{"id": id, "locale": "en"}, "item is a draft"},
		{map[string]string{"type": "post", "slug": "hello", "locale": "ar"}, "العنصر مسودة"},
	} {
		text, err := a.prompt("seo_review", c.args)
		if err != nil {
			t.Fatal(err)
		}
		for _, must := range []string{`"id": "` + id + `"`, `"description": "وصف"`, "seo.title_template", "get_content", "update_content", c.draft} {
			if !strings.Contains(text, must) {
				t.Errorf("seo_review %v lacks %q:\n%s", c.args, must, text)
			}
		}
	}
	a.ok("publish_content", map[string]any{"id": id}) // "*" lacks mcp.approve_self: parks
	self := e.agent("*", "mcp.approve_self")
	self.ok("publish_content", map[string]any{"id": id})
	text, _ = a.prompt("seo_review", map[string]string{"id": id, "locale": "en"})
	if !strings.Contains(text, "This item is live") || !strings.Contains(text, "check_approval") {
		t.Fatalf("seo_review of a live item:\n%s", text)
	}
}

func TestSiteOverviewPrompt(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("*")
	a.ok("create_content", map[string]any{"type": "post", "title": "Fresh news", "locale": "en"})
	pending := a.ok("fake_sensitive", map[string]any{"note": "x"})
	other := e.agent("*")
	otherPending := other.ok("fake_sensitive", map[string]any{"note": "y"})

	text, err := a.prompt("site_overview", map[string]string{"locale": "en"})
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"post (1)", `"Fresh news"`, "fake_sensitive (approval_id " + str(pending, "approval_id"), "get_site_structure", "check_approval"} {
		if !strings.Contains(text, must) {
			t.Errorf("site_overview lacks %q:\n%s", must, text)
		}
	}
	if strings.Contains(text, str(otherPending, "approval_id")) {
		t.Fatalf("site_overview shows another key's approval:\n%s", text)
	}
	if strings.Contains(text, "@example.com") {
		t.Fatalf("site_overview leaks an email:\n%s", text)
	}
}

func TestCompletions(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("*")
	for _, title := range []string{"Alpha", "Alpine", "Beta"} {
		a.ok("create_content", map[string]any{"type": "post", "title": title, "slug": strings.ToLower(title), "locale": "en"})
	}
	a.ok("create_term", map[string]any{"taxonomy": "category", "name": "News", "slug": "news"})
	a.ok("create_term", map[string]any{"taxonomy": "category", "name": "Notes", "slug": "notes"})
	if ms, ok := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus); ok {
		if _, err := ms.Save(context.Background(), menus.Menu{Slug: "main", Name: "Main", Location: "primary"}); err != nil {
			t.Fatal(err)
		}
	}

	check := func(who *agent, ref *sdk.CompleteReference, arg, value string, given map[string]string, want []string) {
		t.Helper()
		got, err := who.completion(ref, arg, value, given)
		if err != nil {
			t.Fatalf("complete %s %s=%q: %v", ref.Name+ref.URI, arg, value, err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("complete %s %s=%q %v: %v, want %v", ref.Name+ref.URI, arg, value, given, got, want)
		}
	}
	check(a, promptRef("draft_post"), "type", "p", nil, []string{"page", "post"})
	check(a, promptRef("draft_post"), "type", "PO", nil, []string{"post"})
	check(a, promptRef("draft_post"), "locale", "", nil, []string{"ar", "en"})
	check(a, promptRef("draft_post"), "taxonomy", "", map[string]string{"type": "post"}, []string{"category", "series", "tag"})
	check(a, promptRef("draft_post"), "term", "n", map[string]string{"taxonomy": "category"}, []string{"news", "notes"})
	check(a, promptRef("draft_post"), "term", "n", nil, []string{}) // needs a taxonomy
	check(a, promptRef("draft_post"), "topic", "", nil, []string{}) // free text
	check(a, promptRef("seo_review"), "slug", "al", map[string]string{"type": "post"}, []string{"alpha", "alpine"})
	check(a, promptRef("seo_review"), "slug", "al", map[string]string{"type": "page"}, []string{})
	check(a, resourceRef(tmplContent), "slug", "b", map[string]string{"type": "post"}, []string{"beta"})
	check(a, resourceRef(tmplContentLocale), "locale", "e", nil, []string{"en"})
	check(a, resourceRef(tmplMenu), "slug", "", nil, []string{"main"})
	if got, _ := a.completion(promptRef("draft_post"), "template", "", nil); len(got) == 0 {
		t.Errorf("template names for a themes.manage key: none")
	}

	// Scope filtering: without themes.manage no template names; without
	// content.view no prompt or content template to complete at all.
	writer := e.agent("content.view", "content.create")
	check(writer, promptRef("draft_post"), "template", "", nil, []string{})
	check(writer, promptRef("draft_post"), "type", "po", nil, []string{"post"})
	if _, err := writer.completion(promptRef("seo_review"), "slug", "", map[string]string{"type": "post"}); rpcCode(t, err) != rpcForbidden {
		t.Fatalf("completion of an unlisted prompt: %v", err)
	}
	blind := e.agent("media.upload")
	if _, err := blind.completion(resourceRef(tmplContent), "slug", "", map[string]string{"type": "post"}); rpcCode(t, err) != rpcForbidden {
		t.Fatalf("content slugs without content.view: %v", err)
	}
	if _, err := a.completion(promptRef("nope"), "type", "", nil); rpcCode(t, err) != jsonrpc.CodeInvalidParams {
		t.Fatalf("unknown prompt: %v", err)
	}
	if _, err := a.completion(resourceRef("gorun://nope/{x}"), "x", "", nil); rpcCode(t, err) != jsonrpc.CodeInvalidParams {
		t.Fatalf("unknown template: %v", err)
	}
}

// Content slugs follow the content service's read rules: an author's key
// sees its own drafts and published items, never another author's drafts.
func TestContentSlugCompletionReadRules(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.agent("*")
	admin.ok("create_content", map[string]any{"type": "post", "title": "Secret", "slug": "secret-plan", "locale": "en"})
	author := e.agentFor(apitest.New(t, e.app, apitest.Options{Role: "author", Scopes: []string{"content.view", "content.edit"}}))
	got, err := author.completion(resourceRef(tmplContent), "slug", "se", map[string]string{"type": "post"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("author completes another author's draft: %v", got)
	}
	if got, _ := admin.completion(resourceRef(tmplContent), "slug", "se", map[string]string{"type": "post"}); !slices.Equal(got, []string{"secret-plan"}) {
		t.Fatalf("admin: %v", got)
	}
}

func readResource(a *agent, uri string) (map[string]any, error) {
	a.t.Helper()
	res, err := a.cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, err
	}
	if len(res.Contents) != 1 || res.Contents[0].MIMEType != "application/json" {
		a.t.Fatalf("%s: contents %+v", uri, res.Contents)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &env); err != nil {
		a.t.Fatalf("%s: not JSON: %s", uri, res.Contents[0].Text)
	}
	return env, nil
}

func templateURIs(a *agent) []string {
	a.t.Helper()
	res, err := a.cs.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		a.t.Fatal(err)
	}
	var out []string
	for _, rt := range res.ResourceTemplates {
		out = append(out, rt.URITemplate)
	}
	slices.Sort(out)
	return out
}

func resourceURIs(a *agent) []string {
	a.t.Helper()
	res, err := a.cs.ListResources(context.Background(), nil)
	if err != nil {
		a.t.Fatal(err)
	}
	var out []string
	for _, r := range res.Resources {
		out = append(out, r.URI)
	}
	slices.Sort(out)
	return out
}

func TestResourceTemplates(t *testing.T) {
	e := newEnv(t, nil)
	a := e.agent("*")
	ar := a.ok("create_content", map[string]any{"type": "post", "title": "مرحبا", "slug": "hello", "locale": "ar"})
	en := a.ok("create_translation", map[string]any{"id": str(ar, "id"), "locale": "en"})
	up := a.ok("upload_media", map[string]any{"filename": "dot.png", "data_base64": encodePNG(t), "alt": "dot"})
	if ms, ok := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus); ok {
		if _, err := ms.Save(context.Background(), menus.Menu{Slug: "main", Name: "Main", Location: "primary"}); err != nil {
			t.Fatal(err)
		}
	}

	// Listing is scope-filtered; gorun://guide is open to any key.
	if got := templateURIs(a); !slices.Equal(got, []string{"gorun://canvases/{id}", tmplContentLocale, tmplContent, tmplMedia, tmplMenu}) {
		t.Fatalf("templates for *: %v", got)
	}
	blind := e.agent("comments.moderate")
	if got := templateURIs(blind); len(got) != 0 {
		t.Fatalf("templates without content.view: %v", got)
	}
	if got := resourceURIs(blind); !slices.Equal(got, []string{guideURI}) {
		t.Fatalf("resources without content.view: %v", got)
	}

	// Same envelope (and data) as the matching tools.
	env, err := readResource(a, "gorun://content/post/hello?locale=en")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := env["data"].(map[string]any)
	if env["ok"] != true || str(data, "id") != str(en, "id") {
		t.Fatalf("content ?locale=en: %v", env)
	}
	env, _ = readResource(a, "gorun://content/post/hello/ar")
	if data, _ := env["data"].(map[string]any); str(data, "id") != str(ar, "id") || str(data, "status") != "draft" {
		t.Fatalf("content /ar (a draft, readable with content.view): %v", env)
	}
	tool := a.ok("get_content", map[string]any{"type": "post", "slug": "hello", "locale": "ar"})
	if got, want := mustJSON(t, env["data"]), mustJSON(t, tool); string(got) != string(want) {
		t.Fatalf("resource data differs from get_content:\n%s\n%s", got, want)
	}
	env, _ = readResource(a, "gorun://media/"+str(up, "id"))
	if data, _ := env["data"].(map[string]any); str(data, "alt") != "dot" || data["data_base64"] != nil {
		t.Fatalf("media: %v", env)
	}
	env, _ = readResource(a, "gorun://menu/main")
	if data, _ := env["data"].(map[string]any); str(data, "slug") != "main" {
		t.Fatalf("menu: %v", env)
	}

	// Denied and unknown reads use the documented JSON-RPC codes. A missing
	// resource is -32602 with the message "Resource not found" (SEP-2164).
	for _, c := range []struct {
		who  *agent
		uri  string
		code int64
		msg  string
	}{
		{blind, "gorun://content/post/hello", rpcForbidden, "forbidden"},
		{blind, "gorun://media/" + str(up, "id"), rpcForbidden, "forbidden"},
		{blind, "gorun://menu/main", rpcForbidden, "forbidden"},
		{a, "gorun://content/post/missing", jsonrpc.CodeInvalidParams, "Resource not found"},
		{a, "gorun://media/missing", jsonrpc.CodeInvalidParams, "Resource not found"},
		{a, "gorun://menu/missing", jsonrpc.CodeInvalidParams, "Resource not found"},
		{a, "gorun://nothing/here", jsonrpc.CodeInvalidParams, "Resource not found"},
		{a, "gorun://content/post/hello?lang=en", jsonrpc.CodeInvalidParams, "unknown query parameter lang"},
	} {
		if _, err := readResource(c.who, c.uri); err == nil || rpcCode(t, err) != c.code || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: want code %d (%s), got %v", c.uri, c.code, c.msg, err)
		}
	}
	// An author's key cannot read another author's draft through the template.
	author := e.agentFor(apitest.New(t, e.app, apitest.Options{Role: "author", Scopes: []string{"content.view"}}))
	if _, err := readResource(author, "gorun://content/post/hello/ar"); err == nil || rpcCode(t, err) != rpcForbidden {
		t.Fatalf("author reads another author's draft: %v", err)
	}
}

func encodePNG(t *testing.T) string {
	t.Helper()
	b, _ := json.Marshal(tinyPNG(t)) // []byte marshals as standard base64
	return strings.Trim(string(b), `"`)
}
