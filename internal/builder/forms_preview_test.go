package builder

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/theme"
)

// draftFormDoc is a page whose only form exists in the draft: nothing was
// published, so site has no row for it.
func draftFormDoc(t *testing.T) string {
	t.Helper()
	sec := DefaultSection("form", "en")
	sec.Title = "Book a call"
	sec.Form = &FormSpec{Slug: "book-a-call", SubmitLabel: `Book <now>`, Fields: []FormFieldSpec{
		{Name: "name", Label: `<b>Name</b> & "co"`, Type: "text", Required: true, Help: `Your <i>full</i> name`},
		{Name: "email", Label: "Email", Type: "email", Required: true},
		{Name: "plan", Label: "Plan", Type: "choice", Choices: []string{"basic", "<pro>"}},
		{Name: "notes", Label: "Notes", Type: "textarea"},
		{Name: "agree", Label: "I agree", Type: "checkbox"},
		{Name: "cv", Label: "CV", Type: "file"},
	}}
	raw, err := json.Marshal(Document{Version: 1, Sections: []Section{sec}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPreviewShowsDraftOnlyFormDisabled(t *testing.T) {
	h, cs, ctx := starterTest(t)
	if err := theme.New(theme.WithThemesDir("../../themes")).Register(h.d); err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(ctx, "site.theme", "default"); err != nil {
		t.Fatal(err)
	}
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Book", Status: "draft", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	a := &adminHandlers{svc: h.svc, d: h.d}
	rec := adminCall(ctx, a.preview, it.ID, `{"document":`+draftFormDoc(t)+`}`)
	if rec.Code != 200 {
		t.Fatalf("preview HTTP %d: %s", rec.Code, rec.Body.String())
	}
	page := rec.Body.String()
	for _, want := range []string{
		`class="sb-form gp-form-fields"`,
		`<div class="sf-field sf-text"><label for="sf-book-a-call-name">&lt;b&gt;Name&lt;/b&gt; &amp; &#34;co&#34;</label>`,
		`id="sf-book-a-call-name-help" class="sf-help">Your &lt;i&gt;full&lt;/i&gt; name</p>`,
		`<div class="sf-field sf-email">`,
		`<option value="&lt;pro&gt;">&lt;pro&gt;</option>`,
		`<div class="sf-field sf-textarea">`,
		`<div class="sf-field sf-checkbox">`,
		`<div class="sf-field sf-file">`,
		`Book &lt;now&gt;</button>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("draft form preview missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<b>Name</b>") || strings.Contains(page, "<pro>") {
		t.Fatalf("draft form labels not escaped:\n%s", page)
	}
	if strings.Contains(page, `action="/forms/`) || strings.Contains(page, `name="_csrf"`) {
		t.Fatalf("draft preview form can post:\n%s", page)
	}
	controls := regexp.MustCompile(`<(input|select|textarea|button)\b[^>]*>`).FindAllString(page, -1)
	if len(controls) < 7 {
		t.Fatalf("draft preview controls %d:\n%s", len(controls), page)
	}
	for _, tag := range controls {
		if !strings.Contains(tag, "disabled") {
			t.Fatalf("draft preview control not disabled: %s\n%s", tag, page)
		}
	}
}

func TestDraftFormMarkup(t *testing.T) {
	doc := Document{Version: 1, Sections: []Section{
		{ID: "a", Kind: "form", Form: &FormSpec{Slug: "", Fields: []FormFieldSpec{{Name: "", Label: "الاسم", Type: "text"}, {Name: "", Label: "Other", Type: "mystery"}}}},
		{ID: "b", Kind: "form", Form: &FormSpec{Slug: "dup", Fields: []FormFieldSpec{{Name: "x", Label: "X", Type: "number"}}}},
		{ID: "c", Kind: "form", Form: &FormSpec{Slug: "dup", Fields: []FormFieldSpec{{Name: "x", Label: "X", Type: "date"}}}},
		{ID: "d", Kind: "form", Form: &FormSpec{Slug: "empty"}},
		{ID: "e", Kind: "text"},
	}}
	got := draftFormMarkup(doc, func(key string) string { return key })
	if len(got) != 3 {
		t.Fatalf("markup per form section with fields: %v", got)
	}
	if !strings.Contains(got["a"], `id="sf-a-f0"`) || !strings.Contains(got["a"], `id="sf-a-f1" name="" type="text"`) {
		t.Fatalf("unnamed fields need unique ids: %s", got["a"])
	}
	if !strings.Contains(got["a"], `form_preview_submit</button>`) {
		t.Fatalf("default submit label: %s", got["a"])
	}
	if !strings.Contains(got["b"], `id="sf-dup-x"`) || !strings.Contains(got["c"], `id="sf-c-x"`) {
		t.Fatalf("repeated slug ids: %s | %s", got["b"], got["c"])
	}
	if !strings.Contains(got["b"], `type="number" dir="ltr" step="any"`) || !strings.Contains(got["c"], `type="date"`) {
		t.Fatalf("input types: %s | %s", got["b"], got["c"])
	}
}
