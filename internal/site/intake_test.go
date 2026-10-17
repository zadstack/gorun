package site_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

func intakeForm(t *testing.T, e *env, kind string) url.Values {
	t.Helper()
	response, body := e.get("/reports/" + kind)
	if response.StatusCode != 200 {
		t.Fatalf("intake form: %d", response.StatusCode)
	}
	match := regexp.MustCompile(`name="request_id" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("missing request ID")
	}
	return url.Values{"_csrf": {csrfFrom(t, body)}, "request_id": {match[1]}, "category": {"safety"}, "area": {"Example district"}, "description": {"Synthetic confidential report details"}, "consent": {"yes"}}
}

func TestPrivateIntakeDisabledAndEncrypted(t *testing.T) {
	e := boot(t)
	if r, _ := e.get("/reports/incident"); r.StatusCode != 404 {
		t.Fatal("intake enabled by default")
	}
	e.setting("intake.enabled", true)
	e.setting("site.locale", "en")
	form := intakeForm(t, e, "incident")
	noCSRF := url.Values{"description": {"x"}}
	if r, _ := e.do("POST", "/reports/incident", noCSRF); r.StatusCode != 403 {
		t.Fatalf("csrf %d", r.StatusCode)
	}
	response, body := e.do("POST", "/reports/incident", form)
	if response.StatusCode != 200 {
		t.Fatalf("submit %d: %s", response.StatusCode, body)
	}
	mustContain(t, body, "Received", "not an emergency")
	mustNotContain(t, body, form.Get("description"))
	if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("private response cacheable")
	}
	var payload, status string
	if err := e.app.Deps.DB.QueryRow("SELECT payload,status FROM private_intake WHERE id=?", form.Get("request_id")).Scan(&payload, &status); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "Synthetic") || status != "new" {
		t.Fatal("report not encrypted or incorrect initial status")
	}
	if r, _ := e.do("POST", "/reports/incident", form); r.StatusCode != 200 {
		t.Fatal("safe retry failed")
	}
	var n int
	_ = e.app.Deps.DB.QueryRow("SELECT count(*) FROM private_intake").Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate report: %d", n)
	}
	_ = e.app.Deps.DB.QueryRow("SELECT count(*) FROM form_submissions").Scan(&n)
	if n != 0 {
		t.Fatal("sensitive report reached email/contact inbox")
	}
	for _, path := range []string{"/search?q=Synthetic", "/sitemap.xml", "/reports/" + form.Get("request_id")} {
		_, body = e.get(path)
		mustNotContain(t, body, "Synthetic confidential", form.Get("request_id"))
	}
}

func TestPrivateIntakeReviewPermissionAndConflict(t *testing.T) {
	e := boot(t)
	e.setting("intake.enabled", true)
	e.setting("site.locale", "en")
	form := intakeForm(t, e, "incident")
	if r, _ := e.do("POST", "/reports/incident", form); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	path := "/admin/reports/" + form.Get("request_id")
	if r, body := e.get(path); r.StatusCode == 200 || strings.Contains(body, "Synthetic confidential") {
		t.Fatal("anonymous report read")
	}
	e.loginAdmin()
	response, body := e.get(path)
	if response.StatusCode != 200 {
		t.Fatalf("review %d", response.StatusCode)
	}
	mustContain(t, body, "Synthetic confidential report details")
	version := regexp.MustCompile(`name="version" value="([^"]+)"`).FindStringSubmatch(body)[1]
	update := url.Values{"_csrf": {csrfFrom(t, body)}, "version": {version}, "status": {"reviewing"}, "note": {"Synthetic reviewer note"}}
	if r, _ := e.do("POST", path, update); r.StatusCode != 303 {
		t.Fatalf("review update %d", r.StatusCode)
	}
	if r, _ := e.do("POST", path, update); r.StatusCode != 409 {
		t.Fatalf("stale update %d", r.StatusCode)
	}
	_, body = e.get(path)
	mustContain(t, body, "Synthetic reviewer note")
	_, logout := e.get(path)
	e.do("POST", "/admin/logout", url.Values{"_csrf": {csrfFrom(t, logout)}})
	svc, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	_, err := svc.Create(context.Background(), "editor@example.com", "Editor", "correct horse battery", "editor")
	if err != nil {
		t.Fatal(err)
	}
	_, login := e.get("/admin/login")
	e.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, login)}, "email": {"editor@example.com"}, "password": {"correct horse battery"}})
	if r, body := e.get(path); r.StatusCode != 403 || strings.Contains(body, "Synthetic confidential") {
		t.Fatal("ordinary content editor can read private reports")
	}
}

func TestPrivateIntakeValidationLocaleAndHoneypot(t *testing.T) {
	e := boot(t)
	e.setting("intake.enabled", true)
	e.setting("site.locale", "en")
	form := intakeForm(t, e, "follow-up")
	form.Set("reference", "Previous report reference")
	form.Set("consent", "")
	if r, _ := e.do("POST", "/reports/follow-up", form); r.StatusCode != 422 {
		t.Fatal("missing consent accepted")
	}
	form.Set("consent", "yes")
	form.Set("contact", "invalid-contact")
	if r, _ := e.do("POST", "/reports/follow-up", form); r.StatusCode != 422 {
		t.Fatal("invalid email accepted")
	}
	form.Set("contact", "")
	form.Set("website", "bot")
	if r, _ := e.do("POST", "/reports/follow-up", form); r.StatusCode != 200 {
		t.Fatal("honeypot response")
	}
	var n int
	_ = e.app.Deps.DB.QueryRow("SELECT count(*) FROM private_intake").Scan(&n)
	if n != 0 {
		t.Fatal("honeypot stored")
	}
	response, body := e.get("/ar/reports/incident")
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.StatusCode)
	}
	mustContain(t, body, `lang="ar"`, `action="/ar/reports/incident"`)
}

func TestPrivateIntakeNonceConflictAndPrivateAdminList(t *testing.T) {
	e := boot(t)
	e.setting("intake.enabled", true)
	e.setting("site.locale", "en")
	form := intakeForm(t, e, "incident")
	if r, _ := e.do("POST", "/reports/incident", form); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	form.Set("description", "Different sensitive content")
	if r, _ := e.do("POST", "/reports/incident", form); r.StatusCode != 409 {
		t.Fatal("changed nonce payload accepted")
	}
	e.loginAdmin()
	r, b := e.get("/admin/reports")
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	mustContain(t, b, form.Get("request_id"))
	mustNotContain(t, b, "Synthetic confidential", "Different sensitive")
	if !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("admin list cacheable")
	}
	e.setting("intake.enabled", false)
	if r, _ := e.get("/reports/incident"); r.StatusCode != 404 {
		t.Fatal("disabled public intake")
	}
	if r, _ := e.get("/admin/reports/" + form.Get("request_id")); r.StatusCode != 200 {
		t.Fatal("disable hid existing record")
	}
}

func TestPrivateIntakeEnableSettingsAndEncryptedNotes(t *testing.T) {
	e := boot(t)
	e.setting("site.locale", "en")
	e.loginAdmin()
	_, body := e.get("/admin/settings/reports")
	form := url.Values{"_csrf": {csrfFrom(t, body)}, "previous": {"false"}, "enabled": {"yes"}}
	if r, _ := e.do("POST", "/admin/settings/reports", form); r.StatusCode != 303 {
		t.Fatalf("enable %d", r.StatusCode)
	}
	if r, _ := e.do("POST", "/admin/settings/reports", form); r.StatusCode != 409 {
		t.Fatal("stale enable accepted")
	}
	report := intakeForm(t, e, "incident")
	e.do("POST", "/reports/incident", report)
	path := "/admin/reports/" + report.Get("request_id")
	_, body = e.get(path)
	version := regexp.MustCompile(`name="version" value="([^"]+)"`).FindStringSubmatch(body)[1]
	update := url.Values{"_csrf": {csrfFrom(t, body)}, "version": {version}, "status": {"closed"}, "note": {"Private confidential note"}}
	if r, _ := e.do("POST", path, update); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	var encrypted string
	if err := e.app.Deps.DB.QueryRow("SELECT payload FROM private_intake WHERE id=?", report.Get("request_id")).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "Private confidential note") {
		t.Fatal("review note plaintext")
	}
	var meta string
	if err := e.app.Deps.DB.QueryRow("SELECT group_concat(meta_json) FROM audit_log WHERE action LIKE 'intake.%'").Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(meta, "confidential") {
		t.Fatal("private payload leaked into audit")
	}
}

func TestPrivateIntakeBodyLimitUnicodeAndReviewValidation(t *testing.T) {
	e := boot(t)
	e.setting("intake.enabled", true)
	e.setting("site.locale", "en")
	form := intakeForm(t, e, "incident")
	form.Set("description", strings.Repeat("🙂", 5000))
	if r, _ := e.do("POST", "/reports/incident", form); r.StatusCode != 200 {
		t.Fatalf("documented unicode limit rejected %d", r.StatusCode)
	}
	next := intakeForm(t, e, "incident")
	next.Set("description", strings.Repeat("x", 100000))
	if r, _ := e.do("POST", "/reports/incident", next); r.StatusCode == 200 {
		t.Fatal("oversize report accepted")
	}
	e.loginAdmin()
	path := "/admin/reports/" + form.Get("request_id")
	_, body := e.get(path)
	version := regexp.MustCompile(`name="version" value="([^"]+)"`).FindStringSubmatch(body)[1]
	update := url.Values{"_csrf": {csrfFrom(t, body)}, "version": {version}, "status": {"published"}, "note": {"<script>alert('private')</script>"}}
	if r, _ := e.do("POST", path, update); r.StatusCode != 422 {
		t.Fatal("invalid review status")
	}
	update.Set("status", "reviewing")
	if r, _ := e.do("POST", path, update); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	_, body = e.get(path)
	mustNotContain(t, body, "<script>alert('private')</script>")
	mustContain(t, body, "&lt;script&gt;")
}

func TestPrivateIntakeLimitsAndInvalidReviewStayPrivate(t *testing.T) {
	e := boot(t)
	e.setting("intake.enabled", true)
	e.setting("site.locale", "en")
	form := intakeForm(t, e, "incident")
	for i := 0; i < 5; i++ {
		if r, _ := e.do("POST", "/reports/incident", form); r.StatusCode != 200 {
			t.Fatal(r.StatusCode)
		}
	}
	if r, _ := e.do("POST", "/reports/incident", form); r.StatusCode != 429 {
		t.Fatal("rate limit missing")
	}
	e.loginAdmin()
	if r, _ := e.get("/admin/reports/not-an-id"); r.StatusCode != 404 {
		t.Fatal("invalid report ID")
	}
	if r, _ := e.get("/admin/reports/01AAAAAAAAAAAAAAAAAAAAAAAA"); r.StatusCode != 404 {
		t.Fatal("missing report")
	}
	path := "/admin/reports/" + form.Get("request_id")
	_, body := e.get(path)
	version := regexp.MustCompile(`name="version" value="([^"]+)"`).FindStringSubmatch(body)[1]
	update := url.Values{"_csrf": {csrfFrom(t, body)}, "version": {version}, "status": {"reviewing"}, "note": {strings.Repeat("x", 3001)}}
	if r, _ := e.do("POST", path, update); r.StatusCode != 422 {
		t.Fatal("oversized private note")
	}
	if _, err := e.app.Deps.DB.Exec("UPDATE private_intake SET payload='broken-encrypted-payload' WHERE id=?", form.Get("request_id")); err != nil {
		t.Fatal(err)
	}
	if r, b := e.get(path); r.StatusCode != 500 || strings.Contains(b, "broken-encrypted-payload") {
		t.Fatal("corrupt report leaked or accepted")
	}
}

func TestPrivateIntakePaginationAndArabicSubmit(t *testing.T) {
	e := boot(t)
	e.setting("site.locale", "en")
	if r, _ := e.do("POST", "/reports/incident", url.Values{}); r.StatusCode != 404 {
		t.Fatal("disabled POST accepted")
	}
	e.setting("intake.enabled", true)
	_, body := e.get("/ar/reports/incident")
	id := regexp.MustCompile(`name="request_id" value="([^"]+)"`).FindStringSubmatch(body)[1]
	form := url.Values{"_csrf": {csrfFrom(t, body)}, "request_id": {id}, "category": {"safety"}, "description": {"بلاغ تجريبي فقط"}, "consent": {"yes"}}
	if r, b := e.do("POST", "/ar/reports/incident", form); r.StatusCode != 200 || !strings.Contains(b, "تم الاستلام") {
		t.Fatal("Arabic submit")
	}
	for i := 0; i < 26; i++ {
		id := db.NewID()
		payload, err := db.Encrypt(e.app.Deps.Config.SecretKey, []byte(`{"Category":"safety","Description":"Synthetic restricted fixture"}`), "private-intake:"+id)
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.app.Deps.DB.Exec("INSERT INTO private_intake(id,kind,payload,version,created_at,updated_at) VALUES(?,'incident',?,?,?,?)", id, payload, db.NewID(), db.Now(), db.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	e.loginAdmin()
	_, body = e.get("/admin/reports?page=1")
	mustContain(t, body, "?page=2")
	mustNotContain(t, body, "Synthetic restricted fixture")
	if n := len(regexp.MustCompile(`/admin/reports/[0-9A-HJKMNP-TV-Z]{26}`).FindAllString(body, -1)); n != 25 {
		t.Fatalf("page size %d", n)
	}
	_, body = e.get("/admin/reports?page=2")
	mustContain(t, body, "?page=1")
	if n := len(regexp.MustCompile(`/admin/reports/[0-9A-HJKMNP-TV-Z]{26}`).FindAllString(body, -1)); n != 2 {
		t.Fatalf("page two size %d", n)
	}
}

func TestPrivateIntakeConcurrentReviewKeepsOneUpdate(t *testing.T) {
	e := boot(t)
	e.setting("intake.enabled", true)
	e.setting("site.locale", "en")
	form := intakeForm(t, e, "incident")
	e.do("POST", "/reports/incident", form)
	e.loginAdmin()
	path := "/admin/reports/" + form.Get("request_id")
	_, body := e.get(path)
	version := regexp.MustCompile(`name="version" value="([^"]+)"`).FindStringSubmatch(body)[1]
	csrf := csrfFrom(t, body)
	statuses := make(chan int, 2)
	for _, note := range []string{"First reviewer update", "Second reviewer update"} {
		go func(note string) {
			values := url.Values{"_csrf": {csrf}, "version": {version}, "status": {"reviewing"}, "note": {note}}
			request, _ := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(values.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := e.c.Do(request)
			if err != nil {
				statuses <- 0
				return
			}
			resp.Body.Close()
			statuses <- resp.StatusCode
		}(note)
	}
	a, b := <-statuses, <-statuses
	if !((a == 303 && b == 409) || (a == 409 && b == 303)) {
		t.Fatalf("concurrent status %d %d", a, b)
	}
	_, body = e.get(path)
	if strings.Contains(body, "First reviewer update") == strings.Contains(body, "Second reviewer update") {
		t.Fatal("exactly one review must survive")
	}
}
