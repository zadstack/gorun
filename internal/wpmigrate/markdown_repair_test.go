package wpmigrate

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func TestRepairMarkdown(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{
			name: "list with intro line",
			in:   "<p><strong>يفيد:</strong>\n- مطوري الـbackend\n- فرق DevOps</p>",
			want: "<p><strong>يفيد:</strong></p><ul><li>مطوري الـbackend</li><li>فرق DevOps</li></ul>", changed: true,
		},
		{
			name: "list then trailing text",
			in:   "<p>- one\n- two\nafter</p>",
			want: "<ul><li>one</li><li>two</li></ul><p>after</p>", changed: true,
		},
		{
			name: "autop br lines",
			in:   "<p>intro<br>\n- one<br/>\n- two<br />\nafter</p>\n",
			want: "<p>intro</p><ul><li>one</li><li>two</li></ul><p>after</p>\n", changed: true,
		},
		{
			name: "table with code cells",
			in: "<p><strong>خيارات:</strong>\n| Server | الاستخدام |\n|--------|-----------|\n" +
				"| <code>postgres</code> | استعلام |\n| <code>github</code> | إدارة |</p>",
			want: "<p><strong>خيارات:</strong></p><table><thead><tr><th>Server</th><th>الاستخدام</th></tr></thead>" +
				"<tbody><tr><td><code>postgres</code></td><td>استعلام</td></tr><tr><td><code>github</code></td><td>إدارة</td></tr></tbody></table>",
			changed: true,
		},
		{
			name: "aligned separator",
			in:   "<p>| a | b |\n|:---|---:|\n| 1 | 2 |</p>",
			want: "<table><thead><tr><th>a</th><th>b</th></tr></thead><tbody><tr><td>1</td><td>2</td></tr></tbody></table>", changed: true,
		},
		{
			name: "inline backticks",
			in:   "<p>من أداة `generate_chart` (Chart.js).</p><h2>`x`</h2>",
			want: "<p>من أداة <code>generate_chart</code> (Chart.js).</p><h2><code>x</code></h2>", changed: true,
		},
		{
			name: "mixed paragraphs",
			in:   "<p>plain one</p>\n<p>see `cfg`:\n- a\n- b</p>\n<p>plain two</p>",
			want: "<p>plain one</p>\n<p>see <code>cfg</code>:</p><ul><li>a</li><li>b</li></ul>\n<p>plain two</p>", changed: true,
		},
		{
			name: "paragraph attributes kept",
			in:   `<p class="lead">intro` + "\n- a</p>",
			want: `<p class="lead">intro</p><ul><li>a</li></ul>`, changed: true,
		},
		{
			name: "code blocks untouched",
			in:   "<pre><code>- not a list\n| a | b |\n|---|---|\n`tick`</code></pre>\n<p>- yes</p><PRE class=\"x\">`y`</PRE>",
			want: "<pre><code>- not a list\n| a | b |\n|---|---|\n`tick`</code></pre>\n<ul><li>yes</li></ul><PRE class=\"x\">`y`</PRE>", changed: true,
		},
		{name: "no markdown", in: "<h2>عنوان</h2>\n<p>نص <strong>عادي</strong><br/>\nسطر ثانٍ</p>\n<ul><li>x</li></ul>", want: "<h2>عنوان</h2>\n<p>نص <strong>عادي</strong><br/>\nسطر ثانٍ</p>\n<ul><li>x</li></ul>"},
		{name: "lone pipe line", in: "<p>  intro\n| foo</p>", want: "<p>  intro\n| foo</p>"},
		{name: "pipe in prose", in: "<p>a|b and c | d</p>", want: "<p>a|b and c | d</p>"},
		{name: "dash without space", in: "<p>-5 degrees\n--flag</p>", want: "<p>-5 degrees\n--flag</p>"},
		{name: "backticks in existing code", in: "<p><code>a `b` c</code></p>", want: "<p><code>a `b` c</code></p>"},
		{name: "backticks in attributes", in: `<p><a title="` + "`x`" + `" href="/">l</a></p>`, want: `<p><a title="` + "`x`" + `" href="/">l</a></p>`},
		{name: "pre prefix tag", in: "<preview>`a`</preview>", want: "<preview><code>a</code></preview>", changed: true},
		{name: "empty", in: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := repairMarkdown(tc.in)
			if got != tc.want || changed != tc.changed {
				t.Fatalf("repairMarkdown(%q)\n got  %q (%v)\n want %q (%v)", tc.in, got, changed, tc.want, tc.changed)
			}
			again, changedAgain := repairMarkdown(got)
			if again != got || changedAgain {
				t.Fatalf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}

// Code blocks must survive byte for byte, as in a real-site repair.
func TestRepairMarkdownKeepsPreBlocks(t *testing.T) {
	in := "<p>run:\n- `make`</p>\n<pre class=\"wp-block-code\"><code>┌───┐\n- step\n| a | b |\n|---|---|\n`x` &lt;y&gt;\n</code></pre>\n<p>| h |\n|---|\n| v |</p>"
	got, changed := repairMarkdown(in)
	if !changed {
		t.Fatal("expected a repair")
	}
	pre := regexp.MustCompile(`(?is)<pre\b.*?</pre>`)
	if a, b := pre.FindAllString(in, -1), pre.FindAllString(got, -1); strings.Join(a, "\x00") != strings.Join(b, "\x00") {
		t.Fatalf("pre blocks changed:\n%q\n%q", a, b)
	}
	if !strings.Contains(got, "<li><code>make</code></li>") || !strings.Contains(got, "<td>v</td>") {
		t.Fatalf("got %q", got)
	}
}

// The repaired body is stored through content.Sanitize; its policy must keep
// the generated lists, tables and inline code.
func TestRepairMarkdownSurvivesSanitize(t *testing.T) {
	got, _ := repairMarkdown("<p>intro\n- a `x`\n| h1 | h2 |\n|---|---|\n| <code>c</code> | d |</p>")
	clean := content.Sanitize(got)
	for _, want := range []string{"<p>intro</p>", "<ul><li>a <code>x</code></li></ul>", "<table><thead><tr><th>h1</th><th>h2</th></tr></thead>",
		"<tbody><tr><td><code>c</code></td><td>d</td></tr></tbody></table>"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("sanitized body lost %q:\n%s", want, clean)
		}
	}
}

// TestImportRepairsMarkdown: a WXR body with raw Markdown inside <p> is
// stored as lists/inline code, flagged on the item and counted in the report.
func TestImportRepairsMarkdown(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	p := writeSample(t, e.wp.URL())
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`<p class="ep-note">سرير جسر</p>`),
		[]byte("<p class=\"ep-note\">سرير جسر:\n- أ\n- ب</p><p>شغّل `gorun`</p>"), 1)
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := ImportWXRFile(e.ctx(), e.app.Deps, p, Options{Currency: "AED"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report == nil || res.Report.MarkdownRepaired != 1 || !strings.Contains(res.Report.Markdown, "Markdown repaired") {
		t.Fatalf("report = %+v", res.Report)
	}
	it := e.item(res.MigrationID, "content", "page", "1108")
	if it.Meta["markdown_repaired"] != true {
		t.Fatalf("item meta = %v", it.Meta)
	}
	if other := e.item(res.MigrationID, "content", "page", "1107"); other.Meta["markdown_repaired"] == true {
		t.Fatalf("untouched body flagged: %v", other.Meta)
	}
	got, err := e.contentSvc().Get(e.ctx(), it.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Body, "<ul><li>أ</li><li>ب</li></ul>") || !strings.Contains(got.Body, "<code>gorun</code>") {
		t.Fatalf("body = %q", got.Body)
	}
}
