package theme

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"golang.org/x/net/html"
)

func taskSearchFixture(action, parameter string) string {
	return `<section class="gp-native-ui"><div class="gp-task-search gp-task-param-` + parameter + `"><a class="gp-task-search-target" href="` + action + `">Find &amp; choose</a><span class="gp-task-search-placeholder">A &quot;quoted&quot; search</span></div></section>`
}

func TestTaskSearchExpandsAfterSanitizationWithLocaleAndUniqueLabels(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `{{builderBody .Content}}`})
	e := bootRealTheme(t, root)
	e.setting("site.locale", "ar")
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	markup := taskSearchFixture("/search", "q") + taskSearchFixture("/tools", "term")
	page, err := cs.Create(context.Background(), content.Input{Type: "page", Title: "Task entry", Locale: "en", Status: "published", Template: "builder", Body: markup})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page.Body, "<form") {
		t.Fatal("stored content must not contain raw forms")
	}
	request := httptest.NewRequest("GET", `/en/task?q=%22%3E%3Cscript%3Ebad%3C%2Fscript%3E&term=tools`, nil)
	rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: page, Locale: "en", Request: request})
	if err != nil {
		t.Fatal(err)
	}
	document, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	var forms, inputs, labels []*html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "form":
				forms = append(forms, node)
			case "input":
				inputs = append(inputs, node)
			case "label":
				labels = append(labels, node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if len(forms) != 2 || len(inputs) != 2 || len(labels) != 2 {
		t.Fatalf("need two labeled search forms: %s", rec.Body.String())
	}
	attribute := func(node *html.Node, name string) string {
		for _, a := range node.Attr {
			if a.Key == name {
				return a.Val
			}
		}
		return ""
	}
	if attribute(forms[0], "action") != "/en/search" || attribute(forms[1], "action") != "/tools" {
		t.Fatal("search destinations lost page locale or explicit path")
	}
	seen := map[string]bool{}
	for index, input := range inputs {
		id := attribute(input, "id")
		if id == "" || seen[id] || attribute(labels[index], "for") != id {
			t.Fatal("search labels must target unique inputs")
		}
		seen[id] = true
		if attribute(forms[index], "method") != "get" {
			t.Fatal("task search must remain a read operation")
		}
	}
	if attribute(inputs[0], "value") != `"><script>bad</script>` || attribute(inputs[1], "value") != "tools" {
		t.Fatal("search values not retained as text")
	}
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatal("search query became active markup")
	}
}

func TestTaskSearchInvalidMarkersRetainSafeFallbackWithoutAForm(t *testing.T) {
	for _, tc := range []struct{ name, action, param string }{
		{"external", "https://outside.example/search", "q"},
		{"protocol relative", "//outside.example/search", "q"},
		{"query in action", "/search?hidden=1", "q"},
		{"fragment in action", "/search#other", "q"},
		{"encoded authority", "/%2foutside.example/search", "q"},
		{"invalid parameter", "/search", "q:bad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTheme(t, root, "default", "", map[string]string{"templates/page-builder.html": `{{builderBody .Content}}`})
			e := bootRealTheme(t, root)
			cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
			page, err := cs.Create(context.Background(), content.Input{Type: "page", Title: "Invalid entry", Status: "published", Template: "builder", Body: taskSearchFixture(tc.action, tc.param)})
			if err != nil {
				t.Fatal(err)
			}
			rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: page})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(rec.Body.String(), "<form") {
				t.Fatal("invalid task marker expanded to a form")
			}
			if !strings.Contains(rec.Body.String(), "Find &amp; choose") {
				t.Fatal("navigation fallback lost")
			}
		})
	}
}
