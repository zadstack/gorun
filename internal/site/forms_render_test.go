package site_test

import (
	"bytes"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/site"
)

// formFunc returns the registered "form" theme func.
func (e *env) formFunc() func(slug, csrf, back string, locale ...string) template.HTML {
	e.t.Helper()
	fn, ok := e.app.Deps.Theme.Funcs()["form"].(func(string, string, string, ...string) template.HTML)
	if !ok {
		e.t.Fatalf("form func not registered: %T", e.app.Deps.Theme.Funcs()["form"])
	}
	return fn
}

// tagFor returns the control tag (input, textarea, select) named name.
func tagFor(t *testing.T, markup, name string) string {
	t.Helper()
	m := regexp.MustCompile(`<(input|textarea|select)\b[^>]*\bname="` + name + `"[^>]*>`).FindString(markup)
	if m == "" {
		t.Fatalf("no control named %s in:\n%s", name, markup)
	}
	return m
}

func allTypes() []site.FormField {
	return []site.FormField{
		{Name: "name", Label: "Your <b>name</b>", Type: "text", Required: true, Help: `As on your ID & "passport"`},
		{Name: "details", Label: "Details", Type: "textarea"},
		{Name: "email", Label: "Email", Type: "email", Required: true},
		{Name: "guests", Label: "Guests", Type: "number"},
		{Name: "day", Label: "Day", Type: "date"},
		{Name: "room", Label: "Room", Type: "choice", Choices: []string{"Sea <view>", "Garden"}},
		{Name: "agree", Label: "I agree", Type: "checkbox", Required: true},
		{Name: "cv", Label: "CV", Type: "file", Required: true},
	}
}

func upsertBooking(t *testing.T, e *env, def site.FormDefinition) {
	t.Helper()
	def.Slug = "booking"
	if def.Title == "" {
		def.Title = "Booking"
	}
	if err := site.UpsertBuilderForms(e.app.Deps, "page-1", []site.FormDefinition{def}); err != nil {
		t.Fatal(err)
	}
}

func TestFormFuncRendersFields(t *testing.T) {
	e := boot(t)
	e.setting(site.SettingAttachments, true)
	upsertBooking(t, e, site.FormDefinition{SubmitLabel: "Book <now>", Fields: allTypes()})
	out := string(e.formFunc()("booking", "tok123", "/book", "en"))

	mustContain(t, out, `class="sb-form gp-form-fields"`, `method="post"`, `action="/forms/booking"`, `enctype="multipart/form-data"`,
		`name="_csrf" value="tok123"`, `name="_back" value="/book"`, `name="website"`)
	mustNotContain(t, out, "novalidate", "<b>", "<view>", "Book <now>")
	mustContain(t, out, "Your &lt;b&gt;name&lt;/b&gt;", "Sea &lt;view&gt;", "Book &lt;now&gt;", "As on your ID &amp; &#34;passport&#34;")

	// Every visible control has a label tied to it, with the sf-{slug}- prefix.
	for _, f := range allTypes() {
		tag := tagFor(t, out, f.Name)
		id := "sf-booking-" + f.Name
		if !strings.Contains(tag, `id="`+id+`"`) {
			t.Fatalf("%s: id missing: %s", f.Name, tag)
		}
		mustContain(t, out, `<label for="`+id+`"`)
		if f.Required != strings.Contains(tag, " required") {
			t.Fatalf("%s: required=%v but tag %s", f.Name, f.Required, tag)
		}
	}
	name := tagFor(t, out, "name")
	mustContain(t, name, `type="text"`, `maxlength="4000"`, `aria-describedby="sf-booking-name-help"`)
	mustContain(t, out, `<p id="sf-booking-name-help"`)
	mustContain(t, tagFor(t, out, "details"), "<textarea", `maxlength="4000"`)
	mustContain(t, tagFor(t, out, "email"), `type="email"`, `dir="ltr"`)
	mustContain(t, tagFor(t, out, "guests"), `type="number"`, `dir="ltr"`)
	mustContain(t, tagFor(t, out, "day"), `type="date"`)
	mustContain(t, tagFor(t, out, "room"), "<select")
	mustContain(t, out, `<option value="Sea &lt;view&gt;">Sea &lt;view&gt;</option>`, `<option value="Garden">Garden</option>`)
	mustContain(t, tagFor(t, out, "agree"), `type="checkbox"`)
	mustContain(t, tagFor(t, out, "cv"), `type="file"`, `accept="`, `aria-describedby="sf-booking-cv-help"`)
	if strings.Count(out, `type="file"`) != 1 {
		t.Fatalf("one file input expected:\n%s", out)
	}
	if !regexp.MustCompile(`<button type="submit"[^>]*>Book &lt;now&gt;</button>`).MatchString(out) {
		t.Fatalf("submit button:\n%s", out)
	}
	// Honeypot is visually hidden and out of the tab order.
	mustContain(t, out, `aria-hidden="true"`, `tabindex="-1"`)

	// Default submit label, in the locale asked for.
	upsertBooking(t, e, site.FormDefinition{Fields: []site.FormField{{Name: "name", Label: "Name", Type: "text"}}})
	if out := string(e.formFunc()("booking", "t", "/", "en")); !strings.Contains(out, ">Send</button>") {
		t.Fatalf("default en label:\n%s", out)
	}
	if out := string(e.formFunc()("booking", "t", "/", "ar")); !strings.Contains(out, ">إرسال</button>") {
		t.Fatalf("default ar label:\n%s", out)
	}
	// Two forms on one page do not share ids.
	if err := site.UpsertBuilderForms(e.app.Deps, "page-2", []site.FormDefinition{{Slug: "quote", Fields: []site.FormField{{Name: "name", Label: "Name", Type: "text"}}}}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(e.formFunc()("quote", "t", "/", "en")), `id="sf-quote-name"`)
}

func TestFormFuncFileNeedsAttachments(t *testing.T) {
	e := boot(t)
	upsertBooking(t, e, site.FormDefinition{Fields: allTypes()})
	out := string(e.formFunc()("booking", "t", "/", "en"))
	mustNotContain(t, out, `type="file"`, "multipart", `name="cv"`)
}

func TestFormFuncClosedRendersNothing(t *testing.T) {
	e, tok := formsEnv(t)
	fn := e.formFunc()
	if out := fn("nope", "t", "/"); out != "" {
		t.Fatalf("unknown slug rendered: %s", out)
	}
	if out := fn("Bad Slug", "t", "/"); out != "" {
		t.Fatalf("invalid slug rendered: %s", out)
	}
	upsertBooking(t, e, site.FormDefinition{Fields: []site.FormField{{Name: "name", Label: "Name", Type: "text"}}})
	if fn("booking", "t", "/") == "" {
		t.Fatal("open form rendered nothing")
	}
	if err := site.UpsertBuilderForms(e.app.Deps, "page-1", nil); err != nil {
		t.Fatal(err)
	}
	if out := fn("booking", "t", "/"); out != "" {
		t.Fatalf("closed form rendered: %s", out)
	}
	// A captured form has no declared fields to render.
	if resp, _ := e.postForm("quote", tok, "name", "A"); !okStatus(resp.StatusCode) {
		t.Fatalf("capture: %d", resp.StatusCode)
	}
	if out := fn("quote", "t", "/"); out != "" {
		t.Fatalf("captured form rendered: %s", out)
	}
}

// A published builder page shows its form, and the form it shows submits.
func TestBuilderPageRendersAndSubmitsForm(t *testing.T) {
	e := boot(t)
	e.setting("site.locale", "en")
	upsertBooking(t, e, site.FormDefinition{Success: "Thanks, we will call you <soon>.",
		Fields: []site.FormField{{Name: "name", Label: "Name", Type: "text", Required: true}}})
	e.fake.Content.Add(content.Item{Type: "page", Slug: "book", Title: "Book", Status: "published", Template: "builder",
		Body: `<div class="gp-builder"><section class="gp-section gp-form gp-default" data-form="booking"><div class="gp-inner"><div class="gp-heading"><h2>Book a call</h2></div></div></section></div>`})
	resp, page := e.get("/book")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page: %d", resp.StatusCode)
	}
	mustContain(t, page, `action="/forms/booking"`, `name="_back" value="/book"`, `id="sf-booking-name"`)
	tok := csrfFrom(t, page[strings.Index(page, `action="/forms/booking"`):])
	resp, body := e.postForm("booking", tok, "name", "Sara")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit: %d\n%s", resp.StatusCode, body)
	}
	// The builder's success message replaces the generic one, escaped.
	mustContain(t, body, "Thanks, we will call you &lt;soon&gt;.")
	if got := e.entryData("booking"); len(got) != 1 {
		t.Fatalf("entries: %v", got)
	}
}

// postFile posts a builder form as multipart with one file under field.
func (e *env) postFile(slug, csrf string, fields map[string]string, field, fileName string, file []byte) (*http.Response, string) {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_csrf", csrf)
	_ = mw.WriteField("_back", "/contact")
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if fileName != "" {
		w, _ := mw.CreateFormFile(field, fileName)
		_, _ = w.Write(file)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.srv.URL+"/forms/"+slug, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (e *env) entryAttachment(slug string) (id, name, ctype string, size int64) {
	e.t.Helper()
	err := e.app.Deps.DB.QueryRow(`SELECT fe.id, fe.attachment_name, fe.attachment_type, fe.attachment_size FROM form_entries fe
		JOIN forms f ON f.id = fe.form_id WHERE f.slug = ? ORDER BY fe.created_at DESC, fe.rowid DESC LIMIT 1`, slug).Scan(&id, &name, &ctype, &size)
	if err != nil {
		e.t.Fatal(err)
	}
	return
}

func TestBuilderFormFileUpload(t *testing.T) {
	e, tok := formsEnv(t)
	e.setting("site.locale", "en")
	e.setting(site.SettingAttachments, true)
	e.setting(site.SettingAttachmentMaxMB, 1)
	upsertBooking(t, e, site.FormDefinition{Fields: []site.FormField{
		{Name: "name", Label: "Name", Type: "text", Required: true},
		{Name: "cv", Label: "CV", Type: "file", Required: true},
	}})

	// A required file is enforced.
	if resp, body := e.postFile("booking", tok, map[string]string{"name": "Sara"}, "", "", nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing file: %d\n%s", resp.StatusCode, body)
	}
	// The contact form's type allowlist applies.
	if resp, body := e.postFile("booking", tok, map[string]string{"name": "Sara"}, "cv", "x.pdf", []byte("<!doctype html><script>1</script>")); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "not accepted") {
		t.Fatalf("html file: %d\n%s", resp.StatusCode, body)
	}
	// An upload larger than the 64 KiB form cap is accepted: the cap is
	// raised for a form with a file field.
	big := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 200<<10)...)
	resp, body := e.postFile("booking", tok, map[string]string{"name": "Sara"}, "cv", "../my cv.exe", big)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: %d\n%s", resp.StatusCode, body)
	}
	id, name, ctype, size := e.entryAttachment("booking")
	if name != "my cv.pdf" || ctype != "application/pdf" || size != int64(len(big)) {
		t.Fatalf("attachment columns: %q %q %d", name, ctype, size)
	}
	path := filepath.Join(e.app.Deps.Config.DataDir, "form-uploads", id)
	if stored, err := os.ReadFile(path); err != nil || !bytes.Equal(stored, big) {
		t.Fatalf("stored file: %v", err)
	}
	if got := e.entryData("booking"); len(got) != 1 || got[0] != `{"cv":"my cv.pdf","name":"Sara"}` {
		t.Fatalf("entry data: %v", got)
	}
	// A double submit stores one entry and one file.
	if resp, _ := e.postFile("booking", tok, map[string]string{"name": "Sara"}, "cv", "../my cv.exe", big); resp.StatusCode != http.StatusOK {
		t.Fatalf("replay: %d", resp.StatusCode)
	}
	if got := e.entryData("booking"); len(got) != 1 {
		t.Fatalf("replay stored twice: %v", got)
	}
	if files, _ := os.ReadDir(filepath.Dir(path)); len(files) != 1 {
		t.Fatalf("replay left files: %v", files)
	}

	// Private download from the entry page.
	e.loginAdmin()
	_, entry := e.get("/admin/forms/booking/entries/" + id)
	mustContain(t, entry, "/admin/forms/booking/entries/"+id+"/attachment", "my cv.pdf")
	resp, data := e.get("/admin/forms/booking/entries/" + id + "/attachment")
	if resp.StatusCode != http.StatusOK || data != string(big) {
		t.Fatalf("download: %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download headers: %v", resp.Header)
	}
	if resp, _ := e.get("/admin/forms/booking/entries/missing/attachment"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing entry download: %d", resp.StatusCode)
	}
	// Deleting the entry removes its file.
	_, entry = e.get("/admin/forms/booking/entries/" + id)
	if resp, _ := e.do("POST", "/admin/forms/booking/entries/"+id+"/delete", map[string][]string{"_csrf": {csrfFrom(t, entry)}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete entry: %d", resp.StatusCode)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file kept after entry delete: %v", err)
	}

	// Deleting the form removes its entries' files.
	_, contact := e.get("/contact") // the session token changed on login
	tok = csrfFrom(t, contact)
	if resp, body := e.postFile("booking", tok, map[string]string{"name": "Ali"}, "cv", "b.pdf", pdfBytes); resp.StatusCode != http.StatusOK {
		t.Fatalf("second upload: %d\n%s", resp.StatusCode, body)
	}
	id2, _, _, _ := e.entryAttachment("booking")
	if err := site.UpsertBuilderForms(e.app.Deps, "page-1", nil); err != nil { // closed: deletable
		t.Fatal(err)
	}
	_, confirm := e.get("/admin/forms/booking/delete")
	if resp, _ := e.do("POST", "/admin/forms/booking/delete", map[string][]string{"_csrf": {csrfFrom(t, confirm)}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete form: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), id2)); !os.IsNotExist(err) {
		t.Fatalf("file kept after form delete: %v", err)
	}
}

func TestBuilderFileFieldIgnoredWithoutAttachments(t *testing.T) {
	e, tok := formsEnv(t)
	upsertBooking(t, e, site.FormDefinition{Fields: []site.FormField{
		{Name: "name", Label: "Name", Type: "text", Required: true},
		{Name: "cv", Label: "CV", Type: "file", Required: true},
	}})
	if resp, body := e.postForm("booking", tok, "name", "Sara"); resp.StatusCode != http.StatusOK {
		t.Fatalf("required file enforced with attachments off: %d\n%s", resp.StatusCode, body)
	}
	if resp, body := e.postFile("booking", tok, map[string]string{"name": "Ali"}, "cv", "a.pdf", pdfBytes); resp.StatusCode != http.StatusOK {
		t.Fatalf("file post: %d\n%s", resp.StatusCode, body)
	}
	if files, _ := os.ReadDir(filepath.Join(e.app.Deps.Config.DataDir, "form-uploads")); len(files) != 0 {
		t.Fatalf("file stored with attachments off: %v", files)
	}
}

func TestCapturedFormRejectsFiles(t *testing.T) {
	e, tok := formsEnv(t)
	e.setting(site.SettingAttachments, true)
	if resp, _ := e.postFile("quote", tok, map[string]string{"name": "A"}, "cv", "a.pdf", pdfBytes); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("captured file: %d", resp.StatusCode)
	}
	if _, _, ok := e.formRow("quote"); ok {
		t.Fatal("a rejected file created a form")
	}
}

func TestDefinitionAllowsOneFileField(t *testing.T) {
	e := boot(t)
	err := site.UpsertBuilderForms(e.app.Deps, "page-1", []site.FormDefinition{{Slug: "booking", Fields: []site.FormField{
		{Name: "cv", Type: "file"}, {Name: "photo", Type: "file"}}}})
	if err == nil {
		t.Fatal("two file fields accepted")
	}
}
