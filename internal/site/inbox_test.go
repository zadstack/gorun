package site_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/site"
)

var pdfBytes = append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 2000)...)

// postMultipart sends the contact form as multipart/form-data with an
// optional file; csrf "" uses a fresh token from /contact.
func (e *env) postMultipart(path, csrf string, fields map[string]string, fileName string, file []byte) (*http.Response, string) {
	e.t.Helper()
	if csrf == "" {
		_, page := e.get("/contact")
		csrf = csrfFrom(e.t, page)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_csrf", csrf)
	_ = mw.WriteField("_back", "/contact")
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if fileName != "" {
		w, _ := mw.CreateFormFile("attachment", fileName)
		_, _ = w.Write(file)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

var visitor = map[string]string{"name": "Sara Ali", "email": "sara@example.com", "message": "Quote please"}

func (e *env) row(msg string) (id, name, delivery string, size int64, ok bool) {
	e.t.Helper()
	err := e.app.Deps.DB.QueryRow(`SELECT id, attachment_name, delivery, attachment_size FROM form_submissions WHERE message = ?`, msg).
		Scan(&id, &name, &delivery, &size)
	return id, name, delivery, size, err == nil
}

func (e *env) submissions() int {
	e.t.Helper()
	var n int
	_ = e.app.Deps.DB.QueryRow(`SELECT count(*) FROM form_submissions`).Scan(&n)
	return n
}

func bootForms(t *testing.T, fm *fakeMailer) *env {
	e := boot(t, fm)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact us"})
	e.setting("site.locale", "en")
	e.setting(site.SettingAttachments, true)
	e.setting(site.SettingAttachmentMaxMB, 1)
	e.setting(site.SettingFormsRecipients, "team@example.com, sales@example.com")
	return e
}

func TestContactAttachmentIsStoredAndMailedToEveryRecipient(t *testing.T) {
	fm := &fakeMailer{}
	e := bootForms(t, fm)
	_, page := e.get("/contact")
	mustContain(t, page, `enctype="multipart/form-data"`, `name="attachment"`, "up to 1 MB")

	resp, body := e.postMultipart("/forms/contact", "", visitor, "../../عرض <سعر>.exe", pdfBytes)
	if resp.StatusCode != 200 {
		t.Fatalf("submit: %d\n%s", resp.StatusCode, body)
	}
	id, name, _, size, ok := e.row("Quote please")
	if !ok || name != "عرض سعر.pdf" || size != int64(len(pdfBytes)) {
		t.Fatalf("row: ok=%v name=%q size=%d", ok, name, size)
	}
	stored, err := os.ReadFile(filepath.Join(e.app.Deps.Config.DataDir, "form-uploads", id))
	if err != nil || !bytes.Equal(stored, pdfBytes) {
		t.Fatalf("stored file: %v", err)
	}
	waitFor(t, "two emails", func() bool { return len(fm.messages()) == 2 })
	for i, m := range fm.messages() {
		if want := []string{"team@example.com", "sales@example.com"}[i]; m.To != want {
			t.Fatalf("recipient %d = %q", i, m.To)
		}
		if len(m.Attachments) != 1 || m.Attachments[0].Name != "عرض سعر.pdf" || m.Attachments[0].ContentType != "application/pdf" || !bytes.Equal(m.Attachments[0].Data, pdfBytes) {
			t.Fatalf("attachment: %+v", m.Attachments)
		}
		if !strings.Contains(m.Text, "http://example.test/admin/system/form-submissions/"+id) {
			t.Fatalf("no dashboard link: %s", m.Text)
		}
	}
	waitFor(t, "delivery sent", func() bool { _, _, d, _, _ := e.row("Quote please"); return d == "sent" })

	// The localized mount takes multipart too.
	if resp, body := e.postMultipart("/ar/forms/contact", "", map[string]string{"name": "B", "email": "b@example.com", "message": "Arabic route"}, "", nil); resp.StatusCode != 200 {
		t.Fatalf("/ar/: %d\n%s", resp.StatusCode, body)
	}
}

func TestContactAttachmentRejections(t *testing.T) {
	cases := []struct {
		name, file string
		data       []byte
		status     int
		text       string
	}{
		{"html", "page.pdf", []byte("<!doctype html><script>alert(1)</script>"), http.StatusUnprocessableEntity, "not accepted"},
		{"svg", "x.png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), http.StatusUnprocessableEntity, "not accepted"},
		{"text with markup", "notes.txt", []byte("hello\n<html><body>x</body></html>"), http.StatusUnprocessableEntity, "not accepted"},
		{"zip not office", "a.zip", []byte("PK\x03\x04rest"), http.StatusUnprocessableEntity, "not accepted"},
		{"over the limit", "big.pdf", append([]byte("%PDF-"), make([]byte, 1<<20)...), http.StatusUnprocessableEntity, "larger than"},
		{"over the body cap", "huge.pdf", append([]byte("%PDF-"), make([]byte, 2<<20)...), http.StatusRequestEntityTooLarge, "larger than"},
	}
	var e *env
	for i, c := range cases {
		if i%4 == 0 { // a fresh site every 4 posts: the limit is 5 per minute per IP
			e = bootForms(t, &fakeMailer{})
		}
		resp, body := e.postMultipart("/forms/contact", "", visitor, c.file, c.data)
		if resp.StatusCode != c.status || !strings.Contains(body, c.text) {
			t.Fatalf("%s: %d, want %d with %q\n%s", c.name, resp.StatusCode, c.status, c.text, body)
		}
		if n := e.submissions(); n != 0 {
			t.Fatalf("%s: rejected submission stored", c.name)
		}
		if entries, _ := os.ReadDir(filepath.Join(e.app.Deps.Config.DataDir, "form-uploads")); len(entries) != 0 {
			t.Fatalf("%s: files left behind: %v", c.name, entries)
		}
	}
	// A wrong token is still refused for multipart bodies.
	if resp, _ := e.postMultipart("/forms/contact", strings.Repeat("x", 43), visitor, "a.pdf", pdfBytes); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad csrf: %d", resp.StatusCode)
	}
}

// The per-IP limit applies before the body is read, also without a valid
// token: the 6th upload in a minute is refused without being parsed.
func TestRateLimitPrecedesUploadParsing(t *testing.T) {
	e := bootForms(t, &fakeMailer{})
	bad := strings.Repeat("x", 43)
	for i := 1; i <= 5; i++ {
		if resp, _ := e.postMultipart("/forms/contact", bad, visitor, "a.pdf", pdfBytes); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("post %d: %d", i, resp.StatusCode)
		}
	}
	if resp, _ := e.postMultipart("/forms/contact", bad, visitor, "huge.pdf", make([]byte, 3<<20)); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th: %d, want 429", resp.StatusCode)
	}
}

func TestAttachmentsOffIgnoresFilesAndCapsBody(t *testing.T) {
	e := bootForms(t, &fakeMailer{})
	e.setting(site.SettingAttachments, false)
	_, page := e.get("/contact")
	mustNotContain(t, page, `name="attachment"`)
	if resp, body := e.postMultipart("/forms/contact", "", visitor, "a.pdf", pdfBytes); resp.StatusCode != 200 {
		t.Fatalf("small file with attachments off: %d\n%s", resp.StatusCode, body)
	}
	if _, name, _, size, ok := e.row("Quote please"); !ok || name != "" || size != 0 {
		t.Fatalf("file kept although attachments are off: %q %d", name, size)
	}
	if resp, _ := e.postMultipart("/forms/contact", "", visitor, "a.pdf", make([]byte, 100<<10)); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("100 KiB body with attachments off: %d", resp.StatusCode)
	}
}

func TestNotifyOffKeepsMessageWithoutMail(t *testing.T) {
	fm := &fakeMailer{}
	e := bootForms(t, fm)
	e.setting(site.SettingFormsNotify, false)
	e.submitContact("Sara", "sara@example.com", "Quiet message")
	waitFor(t, "delivery off", func() bool { _, _, d, _, _ := e.row("Quiet message"); return d == "off" })
	if n := len(fm.messages()); n != 0 {
		t.Fatalf("mailed %d messages with notify off", n)
	}
	if n := e.auditCount("site.form.failed"); n != 0 {
		t.Fatalf("notify off audited as failure %d times", n)
	}
}

func TestInboxActions(t *testing.T) {
	fm := &fakeMailer{fail: errors.New("smtp down")}
	e := bootForms(t, fm)
	if resp, body := e.postMultipart("/forms/contact", "", map[string]string{"name": "Sara", "email": "sara@example.com", "message": "=HYPERLINK(\"x\")"}, "q.pdf", pdfBytes); resp.StatusCode != 200 {
		t.Fatalf("submit: %d\n%s", resp.StatusCode, body)
	}
	id, _, _, _, _ := e.row(`=HYPERLINK("x")`)
	waitFor(t, "delivery failed", func() bool { _, _, d, _, _ := e.row(`=HYPERLINK("x")`); return d == "failed" })
	e.loginAdmin()
	base := "/admin/system/form-submissions"

	_, list := e.get(base + "?view=new")
	mustContain(t, list, "Sara", "q.pdf", "فشل البريد", `aria-current="page"`)
	resp, detail := e.get(base + "/" + id)
	if resp.StatusCode != 200 {
		t.Fatalf("detail: %d", resp.StatusCode)
	}
	mustContain(t, detail, "smtp down", base+"/"+id+"/attachment")
	var status string
	_ = e.app.Deps.DB.QueryRow(`SELECT status FROM form_submissions WHERE id = ?`, id).Scan(&status)
	if status != "read" {
		t.Fatalf("opening did not mark read: %q", status)
	}

	resp, file := e.get(base + "/" + id + "/attachment")
	if resp.StatusCode != 200 || file != string(pdfBytes) || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment;") ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download: %d %q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}

	csrf := csrfFrom(t, detail)
	fm.mu.Lock()
	fm.fail = nil
	fm.mu.Unlock()
	if resp, _ := e.do("POST", base+"/"+id+"/resend", url.Values{"_csrf": {csrf}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("resend: %d", resp.StatusCode)
	}
	if _, _, d, _, _ := e.row(`=HYPERLINK("x")`); d != "sent" || len(fm.messages()) != 2 {
		t.Fatalf("after resend: delivery=%q mails=%d", d, len(fm.messages()))
	}

	if resp, _ := e.do("POST", base+"/"+id+"/status", url.Values{"_csrf": {csrf}, "status": {"spam"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("spam: %d", resp.StatusCode)
	}
	_, inbox := e.get(base)
	mustNotContain(t, inbox, base+"/"+id+`"`)
	_, spam := e.get(base + "?view=spam")
	mustContain(t, spam, base+"/"+id+`"`)
	if resp, _ := e.do("POST", base+"/"+id+"/status", url.Values{"_csrf": {csrf}, "status": {"bogus"}}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bogus status: %d", resp.StatusCode)
	}

	resp, body := e.get(base + ".csv?view=spam")
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\ufeff"))).ReadAll()
	if resp.StatusCode != 200 || err != nil || len(rows) != 2 || rows[1][6] != `'=HYPERLINK("x")` {
		t.Fatalf("csv: %d %v %q", resp.StatusCode, err, rows)
	}

	if resp, _ := e.do("POST", base+"/settings", url.Values{"_csrf": {csrf}, "notify": {"1"}, "recipients": {"not an email"}, "max_mb": {"5"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("bad settings: %d", resp.StatusCode)
	}
	if got := e.app.Deps.Settings.GetString(context.Background(), site.SettingFormsRecipients, ""); got != "team@example.com, sales@example.com" {
		t.Fatalf("invalid recipients were saved: %q", got)
	}
	if resp, _ := e.do("POST", base+"/settings", url.Values{"_csrf": {csrf}, "recipients": {"a@example.com;b@example.com"}, "max_mb": {"3"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("settings: %d", resp.StatusCode)
	}
	if got := e.app.Deps.Settings.GetString(context.Background(), site.SettingFormsRecipients, ""); got != "a@example.com, b@example.com" {
		t.Fatalf("recipients = %q", got)
	}

	if resp, _ := e.do("POST", base+"/"+id+"/delete", url.Values{"_csrf": {csrf}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.app.Deps.Config.DataDir, "form-uploads", id)); !os.IsNotExist(err) {
		t.Fatalf("attachment kept after delete: %v", err)
	}
	if resp, _ := e.get(base + "/" + id); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted detail: %d", resp.StatusCode)
	}
	for _, a := range []string{"site.form.status", "site.form.delete", "site.form.export", "site.form.settings"} {
		if e.auditCount(a) == 0 {
			t.Errorf("no %s audit", a)
		}
	}
}

func TestValidateFormSetting(t *testing.T) {
	for _, c := range []struct {
		key, in string
		want    any
		ok      bool
	}{
		{site.SettingFormsNotify, "true", true, true},
		{site.SettingFormsNotify, "maybe", nil, false},
		{site.SettingAttachmentMaxMB, "20", 20, true},
		{site.SettingAttachmentMaxMB, "21", nil, false},
		{site.SettingAttachmentMaxMB, "0", nil, false},
		{site.SettingFormsRecipients, " a@x.test ,A@x.test\nb@x.test ", "a@x.test, b@x.test", true},
		{site.SettingFormsRecipients, "", "", true},
		{site.SettingFormsRecipients, "Name <a@x.test>", nil, false},
		{site.SettingFormsRecipients, strings.Repeat("a@x.test,", 0) + "1@x.t,2@x.t,3@x.t,4@x.t,5@x.t,6@x.t,7@x.t,8@x.t,9@x.t,10@x.t,11@x.t", nil, false},
		{"forms.other", "x", nil, false},
	} {
		got, err := site.ValidateFormSetting(c.key, c.in)
		if (err == nil) != c.ok || (c.ok && got != c.want) {
			t.Errorf("%s %q = %v, %v", c.key, c.in, got, err)
		}
		if err != nil && !errors.Is(err, site.ErrFormSetting) {
			t.Errorf("%s: error does not wrap ErrFormSetting", c.key)
		}
	}
}
