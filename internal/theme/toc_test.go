package theme

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func TestTOCAnchorsHeadings(t *testing.T) {
	body := `<p>Intro</p><h2>Getting started</h2><p>a</p><h3>Install &amp; run</h3><h2 id="custom">Kept id</h2><h2>Getting started</h2><h2>ربط الدومين</h2><h4>Too deep</h4>`
	got := toc(&content.Item{Body: body})
	want := []TOCItem{
		{ID: "getting-started", Text: "Getting started", Level: 2},
		{ID: "install-run", Text: "Install & run", Level: 3},
		{ID: "custom", Text: "Kept id", Level: 2},
		{ID: "getting-started-2", Text: "Getting started", Level: 2},
		{ID: "ربط-الدومين", Text: "ربط الدومين", Level: 2},
	}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("Items = %+v", got.Items)
	}
	html := string(got.Body)
	for _, s := range []string{`<h2 id="getting-started">Getting started</h2>`, `<h3 id="install-run">Install &amp; run</h3>`, `<h2 id="custom">Kept id</h2>`, `<h2 id="getting-started-2">`, `<h4>Too deep</h4>`, `<p>Intro</p>`} {
		if !strings.Contains(html, s) {
			t.Errorf("body missing %s:\n%s", s, html)
		}
	}
}

func TestTOCWithoutHeadings(t *testing.T) {
	got := toc("<p>Only text</p>")
	if len(got.Items) != 0 || string(got.Body) != "<p>Only text</p>" {
		t.Fatalf("got %+v", got)
	}
	if got := toc(nil); len(got.Items) != 0 || got.Body != "" {
		t.Fatalf("nil: %+v", got)
	}
}

// An id never carries markup or quotes out of the attribute.
func TestTOCIDsAreSafe(t *testing.T) {
	got := toc(`<h2>a "quoted" &lt;b&gt; title</h2>`)
	if len(got.Items) != 1 || got.Items[0].ID != "a-quoted-b-title" || got.Items[0].Text != `a "quoted" <b> title` {
		t.Fatalf("items %+v", got.Items)
	}
	if !strings.Contains(string(got.Body), `<h2 id="a-quoted-b-title">a &#34;quoted&#34; &lt;b&gt; title</h2>`) && !strings.Contains(string(got.Body), `<h2 id="a-quoted-b-title">a "quoted" &lt;b&gt; title</h2>`) {
		t.Fatalf("body %s", got.Body)
	}
}
