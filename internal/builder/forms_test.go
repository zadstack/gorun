package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/content"
)

func formSection(id, slug string) Section {
	return Section{ID: id, Kind: "form", Variant: "default", Title: "Book a call", Text: "Pick a time.", Form: &FormSpec{
		Slug: slug, SubmitLabel: "Send", Success: "Thanks",
		Fields: []FormFieldSpec{
			{Name: "name", Label: "Name", Type: "text", Required: true},
			{Name: "email", Label: "Email", Type: "email", Required: true, Help: "We reply here"},
			{Name: "topic", Label: "Topic", Type: "choice", Choices: []string{"Sales", "Support"}},
		},
	}}
}

func TestFormSectionValidates(t *testing.T) {
	if err := ValidatePublication(Document{Version: 1, Sections: []Section{formSection("f", "booking")}}); err != nil {
		t.Fatalf("valid form rejected: %v", err)
	}
	// A draft may hold an unfinished form: no slug and no fields yet.
	draft := formSection("f", "")
	draft.Form.Fields = nil
	if err := Validate(Document{Version: 1, Sections: []Section{draft}}); err != nil {
		t.Fatalf("unfinished draft rejected: %v", err)
	}
	if ValidatePublication(Document{Version: 1, Sections: []Section{draft}}) == nil {
		t.Fatal("unfinished form publishable")
	}
	with := func(edit func(s *Section)) Document {
		s := formSection("f", "booking")
		edit(&s)
		return Document{Version: 1, Sections: []Section{s}}
	}
	fields := func(n int) []FormFieldSpec {
		out := make([]FormFieldSpec, n)
		for i := range out {
			out[i] = FormFieldSpec{Name: fmt.Sprintf("f%d", i), Label: "F", Type: "text"}
		}
		return out
	}
	choices := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("c%d", i)
		}
		return out
	}
	long := func(n int) string { return strings.Repeat("ب", n) }
	publish := map[string]Document{
		"empty slug":      with(func(s *Section) { s.Form.Slug = "" }),
		"bad slug":        with(func(s *Section) { s.Form.Slug = "Book Call" }),
		"long slug":       with(func(s *Section) { s.Form.Slug = strings.Repeat("a", 41) }),
		"reserved slug":   with(func(s *Section) { s.Form.Slug = "contact" }),
		"no form":         with(func(s *Section) { s.Form = nil }),
		"no fields":       with(func(s *Section) { s.Form.Fields = nil }),
		"bad field name":  with(func(s *Section) { s.Form.Fields[0].Name = "Bad-Name" }),
		"underscore name": with(func(s *Section) { s.Form.Fields[0].Name = "_csrf" }),
		"honeypot name":   with(func(s *Section) { s.Form.Fields[0].Name = "website" }),
		"duplicate name":  with(func(s *Section) { s.Form.Fields[1].Name = "name" }),
		"unknown type":    with(func(s *Section) { s.Form.Fields[0].Type = "color" }),
		"choice no list":  with(func(s *Section) { s.Form.Fields[2].Choices = nil }),
		"empty choice":    with(func(s *Section) { s.Form.Fields[2].Choices = []string{"Sales", " "} }),
		"duplicate slug":  {Version: 1, Sections: []Section{formSection("a", "booking"), formSection("b", "booking")}},
	}
	// Bounds hold for drafts too.
	always := map[string]Document{
		"31 fields":       with(func(s *Section) { s.Form.Fields = fields(31) }),
		"21 choices":      with(func(s *Section) { s.Form.Fields[2].Choices = choices(21) }),
		"long choice":     with(func(s *Section) { s.Form.Fields[2].Choices = []string{long(201)} }),
		"long label":      with(func(s *Section) { s.Form.Fields[0].Label = long(201) }),
		"long help":       with(func(s *Section) { s.Form.Fields[0].Help = long(501) }),
		"long title":      with(func(s *Section) { s.Title = long(201) }),
		"long submit":     with(func(s *Section) { s.Form.SubmitLabel = long(201) }),
		"long success":    with(func(s *Section) { s.Form.Success = long(1001) }),
		"form on hero":    with(func(s *Section) { s.Kind = "hero" }),
		"form in library": with(func(s *Section) { s.Library = SectionLibrary }),
		"too many forms": func() Document {
			doc := Document{Version: 1}
			for i := 0; i <= maxFormSections; i++ {
				doc.Sections = append(doc.Sections, formSection(fmt.Sprintf("s%d", i), fmt.Sprintf("form-%d", i)))
			}
			return doc
		}(),
	}
	for name, doc := range always {
		if Validate(doc) == nil {
			t.Errorf("%s: draft accepted", name)
		}
		publish[name] = doc
	}
	for name, doc := range publish {
		if ValidatePublication(doc) == nil {
			t.Errorf("%s: publishable", name)
		}
	}
	// Labels count characters, not bytes: 200 Arabic letters fit.
	if err := ValidatePublication(with(func(s *Section) { s.Form.Fields[0].Label = long(200); s.Title = long(200) })); err != nil {
		t.Fatalf("200-character Arabic label rejected: %v", err)
	}
	// 30 fields is the limit, not past it; a hidden form is not checked for publishing.
	if err := ValidatePublication(with(func(s *Section) { s.Form.Fields = fields(30) })); err != nil {
		t.Fatalf("30 fields rejected: %v", err)
	}
	hidden := draft
	hidden.Hidden = true
	if err := ValidatePublication(Document{Version: 1, Sections: []Section{DefaultSection("hero", "en"), hidden}}); err != nil {
		t.Fatalf("hidden unfinished form blocked publishing: %v", err)
	}
}

func TestFormSlugFromArabicTitle(t *testing.T) {
	for in, want := range map[string]string{
		"احجز موعد":                "",
		"Book a call":              "book-a-call",
		"  Book -- a Call!  ":      "book-a-call",
		"احجز Demo 2026":           "demo-2026",
		strings.Repeat("abc ", 20): "abc-abc-abc-abc-abc-abc-abc-abc-abc-abc",
		"Ünïcode café":             "n-code-caf",
	} {
		if got := SlugFromTitle(in); got != want {
			t.Errorf("SlugFromTitle(%q) = %q, want %q", in, got, want)
		}
		if got := SlugFromTitle(in); got != "" && !formSlugPattern.MatchString(got) {
			t.Errorf("SlugFromTitle(%q) = %q is not a valid slug", in, got)
		}
	}
	if s := DefaultSection("form", "ar"); s.Form == nil || s.Form.Slug != "" || len(s.Form.Fields) == 0 {
		t.Fatalf("ar default form: %+v", s.Form)
	}
	if s := DefaultSection("form", "en"); s.Form == nil || s.Form.Slug != "form" {
		t.Fatalf("en default form: %+v", s.Form)
	}
}

func TestFormSectionRenders(t *testing.T) {
	s := formSection("f1", "booking")
	for _, doc := range []Document{
		{Version: 1, Sections: []Section{s}},
		{Version: 1, Sections: []Section{{Library: SectionLibrary, ID: "hero", Kind: "hero", Title: "Atlas"}, s}},
	} {
		html, err := Render(doc)
		if err != nil {
			t.Fatal(err)
		}
		want := `<section id="gp-f1" class="gp-section gp-form gp-default" data-form="booking"><div class="gp-inner"><div class="gp-heading">`
		if !strings.Contains(html, want) || !strings.Contains(html, "Book a call") || !strings.Contains(html, `<p class="gp-text">Pick a time.</p>`) {
			t.Fatalf("form markup:\n%s", html)
		}
		if strings.Contains(html, "<form") {
			t.Fatalf("form element rendered at publish time:\n%s", html)
		}
	}
}

// formEvents captures builder.forms.published payloads as delivered (JSON).
func formEvents(s *service) <-chan FormsPublished {
	ch := make(chan FormsPublished, 8)
	s.d.Hooks.Action(HookFormsPublished, 10, func(_ context.Context, payload any) error {
		raw, ok := payload.(json.RawMessage)
		if !ok {
			return fmt.Errorf("payload %T", payload)
		}
		var ev FormsPublished
		if err := json.Unmarshal(raw, &ev); err != nil {
			return err
		}
		ch <- ev
		return nil
	})
	return ch
}

func nextEvent(t *testing.T, ch <-chan FormsPublished) FormsPublished {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no builder.forms.published event")
	}
	return FormsPublished{}
}

func queuedFormEvents(t *testing.T, s *service) int {
	t.Helper()
	var n int
	if err := s.d.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind = 'hooks.event' AND json_extract(payload_json, '$.name') = ?`, HookFormsPublished).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func publishDoc(t *testing.T, s *service, cs content.Service, ctx context.Context, title string, doc Document) (string, error) {
	t.Helper()
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: title, Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Save(ctx, it.ID, doc, content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	return it.ID, s.Publish(ctx, it.ID, saved.UpdatedAt)
}

func TestPublishEmitsFormsHook(t *testing.T) {
	s, cs, ctx := builderTest(t)
	events := formEvents(s)
	id, err := publishDoc(t, s, cs, ctx, "Booking", Document{Version: 1, Sections: []Section{DefaultSection("hero", "en"), formSection("f", "booking")}})
	if err != nil {
		t.Fatal(err)
	}
	ev := nextEvent(t, events)
	want := FormsPublished{PageID: id, Forms: []PublishedForm{{Slug: "booking", Title: "Book a call", SubmitLabel: "Send", Success: "Thanks", Fields: formSection("f", "booking").Form.Fields}}}
	if got, _ := json.Marshal(ev); string(got) != string(mustJSON(t, want)) {
		t.Fatalf("event:\n got %s\nwant %s", got, mustJSON(t, want))
	}
	// The wire names are snake_case (ruling P1).
	raw := string(mustJSON(t, want))
	for _, key := range []string{`"page_id":`, `"forms":`, `"submit_label":`, `"success":`, `"fields":`, `"name":`, `"label":`, `"type":`, `"required":`, `"help":`, `"choices":`} {
		if !strings.Contains(raw, key) {
			t.Errorf("payload lacks %s: %s", key, raw)
		}
	}
	// A page with no form section still announces an empty list, so site closes removed forms.
	_, err = publishDoc(t, s, cs, ctx, "Plain", Document{Version: 1, Sections: []Section{DefaultSection("hero", "en")}})
	if err != nil {
		t.Fatal(err)
	}
	ev = nextEvent(t, events)
	if ev.Forms == nil || len(ev.Forms) != 0 {
		t.Fatalf("plain page forms = %#v", ev.Forms)
	}
	if !strings.Contains(string(mustJSON(t, ev)), `"forms":[]`) {
		t.Fatalf("empty forms not encoded as []: %s", mustJSON(t, ev))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPublishRejectsSlugTakenByAnotherPage(t *testing.T) {
	s, cs, ctx := builderTest(t)
	hero := DefaultSection("hero", "en")
	first, err := publishDoc(t, s, cs, ctx, "First", Document{Version: 1, Sections: []Section{hero, formSection("f", "booking")}})
	if err != nil {
		t.Fatal(err)
	}
	before := queuedFormEvents(t, s)
	second, err := publishDoc(t, s, cs, ctx, "Second", Document{Version: 1, Sections: []Section{hero, formSection("g", "booking")}})
	var taken *FormSlugTakenError
	if !errors.As(err, &taken) || taken.Slug != "booking" || !strings.Contains(err.Error(), `"booking"`) {
		t.Fatalf("publish with taken slug: %v", err)
	}
	if got, _ := cs.Get(ctx, second); got.Status == "published" {
		t.Fatal("rejected page was published")
	}
	if queuedFormEvents(t, s) != before {
		t.Fatal("rejected publish emitted forms")
	}
	if failure := layoutFailure(err); failure.code != "validation_failed" || !strings.Contains(failure.fields["document"], "booking") {
		t.Fatalf("API failure = %+v", failure)
	}
	// The owning page can republish its own slug, and another slug is free.
	draft, err := s.Load(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(ctx, first, draft.UpdatedAt); err != nil {
		t.Fatalf("owner republish: %v", err)
	}
	if _, err = publishDoc(t, s, cs, ctx, "Third", Document{Version: 1, Sections: []Section{hero, formSection("h", "quote")}}); err != nil {
		t.Fatalf("free slug: %v", err)
	}
}

func TestDraftFormNotRegistered(t *testing.T) {
	s, cs, ctx := builderTest(t)
	events := formEvents(s)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Draft", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Save(ctx, it.ID, Document{Version: 1, Sections: []Section{formSection("f", "booking")}}, content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, it.ID, Document{Version: 1, Sections: []Section{formSection("f", "booking-2")}}, saved.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if n := queuedFormEvents(t, s); n != 0 {
		t.Fatalf("draft saves queued %d form events", n)
	}
	select {
	case ev := <-events:
		t.Fatalf("draft emitted %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
	// A draft's form slug does not block another page's publish.
	if _, err = publishDoc(t, s, cs, ctx, "Other", Document{Version: 1, Sections: []Section{formSection("g", "booking")}}); err != nil {
		t.Fatalf("draft slug blocked a publish: %v", err)
	}
}

// Starter activation publishes its pages in one batch: each page is
// announced after the commit, and two pages cannot share a form slug.
func TestStarterActivationAnnouncesForms(t *testing.T) {
	h, cs, ctx := starterTest(t)
	events := formEvents(h.svc)
	token, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.create(ctx, starterInput(token.Token, "company"))
	if err != nil {
		t.Fatal(err)
	}
	addForm := func(page, slug string) {
		draft, err := h.svc.Load(ctx, run.Pages[page])
		if err != nil {
			t.Fatal(err)
		}
		draft.Document.Sections = append(draft.Document.Sections, formSection("form-"+page, slug))
		if _, err := h.svc.Save(ctx, run.Pages[page], draft.Document, draft.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	addForm("home", "booking")
	addForm("contact", "booking")
	var taken *FormSlugTakenError
	if err := h.activateReviewed(ctx, run.Token, reviewFor(t, h, cs, ctx, run)); !errors.As(err, &taken) {
		t.Fatalf("duplicate slug across the batch: %v", err)
	}
	if n := queuedFormEvents(t, h.svc); n != 0 {
		t.Fatalf("rejected activation queued %d events", n)
	}
	draft, err := h.svc.Load(ctx, run.Pages["contact"])
	if err != nil {
		t.Fatal(err)
	}
	draft.Document.Sections[len(draft.Document.Sections)-1].Form.Slug = "quote"
	if _, err := h.svc.Save(ctx, run.Pages["contact"], draft.Document, draft.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := h.activateReviewed(ctx, run.Token, reviewFor(t, h, cs, ctx, run)); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for range run.Pages {
		ev := nextEvent(t, events)
		slugs := []string{}
		for _, f := range ev.Forms {
			slugs = append(slugs, f.Slug)
		}
		got[ev.PageID] = strings.Join(slugs, ",")
	}
	if got[run.Pages["home"]] != "booking" || got[run.Pages["contact"]] != "quote" || len(got) != len(run.Pages) {
		t.Fatalf("announced %v", got)
	}
}
