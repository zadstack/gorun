package site_test

import (
	"regexp"
	"testing"
)

// Reviewers see human titles: the list shows "Report · date" (it never
// decrypts payloads) and the detail uses the category; the raw ID stays in
// the link target and a muted ID disclosure. The admin defaults to Arabic.
func TestPrivateIntakeHumanTitles(t *testing.T) {
	e := boot(t)
	e.setting("intake.enabled", true)
	e.setting("site.locale", "en")
	form := intakeForm(t, e, "incident")
	if r, _ := e.do("POST", "/reports/incident", form); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	id := form.Get("request_id")
	e.loginAdmin()
	_, list := e.get("/admin/reports")
	if !regexp.MustCompile(`<a href="/admin/reports/` + id + `" class="row-link">بلاغ · \d{4}-\d{2}-\d{2} \d{2}:\d{2}</a>`).MatchString(list) {
		t.Fatal("list row has no human title")
	}
	mustNotContain(t, list, ">"+id+"<")
	_, detail := e.get("/admin/reports/" + id)
	mustContain(t, detail, `<h1 class="page-header-title">مخاوف تتعلق بالسلامة</h1>`, `<code dir="ltr">`+id+`</code>`)
}
