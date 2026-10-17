package site_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

func TestPageAccessDirectUnlockAndAnonymousProjections(t *testing.T) {
	e := bootReal(t)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	it := publish(t, cs, content.Input{Type: "page", Slug: "protected-fixture", Title: "Unique protected headline", Body: "Confidential fixture text"})
	publish(t, cs, content.Input{Type: "page", Slug: "public-fixture", Title: "Unique visible headline"})
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "owner", Role: "admin", Status: "active"})
	ps := cs.(content.PageAccessService)
	if _, err := ps.SavePageAccess(ctx, it.ID, content.PageAccessInput{Mode: "password", Password: "correct page fixture password"}, 0); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/search?q=Unique", "/sitemap.xml", "/"} {
		r, b := e.get(path)
		if r.StatusCode != 200 {
			t.Fatal(path, r.StatusCode)
		}
		mustNotContain(t, b, it.Title, "Confidential fixture text")
		if path == "/sitemap.xml" {
			mustNotContain(t, b, "protected-fixture")
		}
	}
	r, b := e.get("/protected-fixture")
	if r.StatusCode != 401 {
		t.Fatal("gate", r.StatusCode)
	}
	mustNotContain(t, b, it.Title, "Confidential fixture text")
	csrf := csrfFrom(t, b)
	endpoint := "/_page/" + it.ID + "/unlock"
	r, _ = e.do("POST", endpoint, url.Values{"password": {"correct page fixture password"}})
	if r.StatusCode != 403 {
		t.Fatal("csrf", r.StatusCode)
	}
	r, _ = e.do("POST", endpoint, url.Values{"_csrf": {csrf}, "password": {"wrong"}})
	if r.StatusCode != 401 {
		t.Fatal("wrong", r.StatusCode)
	}
	r, _ = e.do("POST", endpoint, url.Values{"_csrf": {csrf}, "password": {"correct page fixture password"}})
	if r.StatusCode != 303 {
		t.Fatal("unlock", r.StatusCode)
	}
	r, b = e.get("/protected-fixture")
	if r.StatusCode != 200 || !strings.Contains(b, "Confidential fixture text") || r.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("unlocked page/cache", r.StatusCode)
	}
	_, b = e.get("/search?q=Unique")
	mustNotContain(t, b, it.Title)
	if _, err := ps.SavePageAccess(ctx, it.ID, content.PageAccessInput{Mode: "password"}, 1); err != nil {
		t.Fatal(err)
	}
	r, _ = e.get("/protected-fixture")
	if r.StatusCode != 401 {
		t.Fatal("old cookie still valid", r.StatusCode)
	}
	if _, err := ps.SavePageAccess(ctx, it.ID, content.PageAccessInput{Mode: "blocked"}, 2); err != nil {
		t.Fatal(err)
	}
	r, _ = e.get("/protected-fixture")
	if r.StatusCode != 503 {
		t.Fatal("blocked", r.StatusCode)
	}
	if _, err := ps.SavePageAccess(ctx, it.ID, content.PageAccessInput{Mode: "public", ClearPassword: true}, 3); err != nil {
		t.Fatal(err)
	}
	r, _ = e.get("/protected-fixture")
	if r.StatusCode != 200 {
		t.Fatal("restore", r.StatusCode)
	}
}

func TestPageAccessNativeAdminCASAndNoSecretReflection(t *testing.T) {
	e := bootReal(t)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	it := publish(t, cs, content.Input{Type: "page", Slug: "page-access-admin", Title: "Public managed page"})
	e.loginAdmin()
	path := "/admin/settings/page-access?id=" + it.ID
	r, b := e.get(path)
	if r.StatusCode != 200 {
		t.Fatal("admin", r.StatusCode)
	}
	csrf := csrfFrom(t, b)
	form := url.Values{"_csrf": {csrf}, "content_id": {it.ID}, "revision": {"0"}, "mode": {"password"}, "password": {"admin fixture password"}, "message": {"Private visitor note"}}
	r, b = e.do("POST", "/admin/settings/page-access", form)
	if r.StatusCode != 303 {
		t.Fatal("save", r.StatusCode, b)
	}
	r, b = e.get(path)
	mustNotContain(t, b, "admin fixture password", "$argon2")
	mustContain(t, b, "Private visitor note")
	form.Set("message", "Unsaved stale message")
	form.Set("password", "")
	r, b = e.do("POST", "/admin/settings/page-access", form)
	if r.StatusCode != 409 {
		t.Fatal("stale", r.StatusCode)
	}
	mustContain(t, b, "Unsaved stale message", `name="revision" value="0"`)
	ps := cs.(content.PageAccessService)
	p, err := ps.PageAccess(context.Background(), it.ID)
	if err != nil || p.Message != "Private visitor note" || p.Revision != 1 {
		t.Fatal("stale changed record", err)
	}
	r, _ = e.get("/page-access-admin")
	if r.StatusCode != 401 {
		t.Fatal("admin public bypass", r.StatusCode)
	}
	form.Set("revision", "1")
	form.Set("mode", "public")
	form.Set("clear_password", "1")
	r, _ = e.do("POST", "/admin/settings/page-access", form)
	if r.StatusCode != 303 {
		t.Fatal("clear", r.StatusCode)
	}
	p, err = ps.PageAccess(context.Background(), it.ID)
	if err != nil || p.PasswordHash != "" {
		t.Fatal("hash retained", err)
	}
}

func TestPageAccessGateUsesContentLocaleAfterPrefixAndFailedUnlock(t *testing.T) {
	e := bootReal(t)
	e.setting("site.locale", "en")
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "owner", Role: "admin", Status: "active"})
	ps := cs.(content.PageAccessService)
	for _, locale := range []string{"ar", "en"} {
		it := publish(t, cs, content.Input{Type: "page", Slug: "locale-protected", Locale: locale, Title: "Locale protected"})
		if _, err := ps.SavePageAccess(ctx, it.ID, content.PageAccessInput{Mode: "password", Password: "safe locale password"}, 0); err != nil {
			t.Fatal(err)
		}
		path := "/locale-protected"
		if locale == "ar" {
			path = "/ar" + path
		}
		r, b := e.get(path)
		if r.StatusCode != 401 {
			t.Fatal(locale, r.StatusCode)
		}
		mustContain(t, b, `lang="`+locale+`"`)
		r, b = e.do("POST", "/_page/"+it.ID+"/unlock", url.Values{"_csrf": {csrfFrom(t, b)}, "password": {"wrong"}})
		if r.StatusCode != 401 {
			t.Fatal("wrong password", r.StatusCode)
		}
		mustContain(t, b, `lang="`+locale+`"`)
	}
}

func TestPageAccessOmitsContentMenuAndProtectedTranslation(t *testing.T) {
	e := bootReal(t)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	ctx := context.Background()
	ar := publish(t, cs, content.Input{Type: "page", Slug: "linked-page", Title: "عنوان عام", Locale: "ar"})
	en := publish(t, cs, content.Input{Type: "page", Slug: "linked-page", Title: "Private translation", Locale: "en"})
	if err := cs.LinkTranslation(ctx, ar.ID, en.ID); err != nil {
		t.Fatal(err)
	}
	ms, _ := kernel.Get[menus.Service](e.app.Deps, kernel.ServiceMenus)
	m, err := ms.Save(ctx, menus.Menu{Slug: "guarded", Name: "Guarded", Location: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	if err = ms.SaveItems(ctx, m.ID, []menus.Item{{Kind: menus.KindContent, RefID: en.ID}}); err != nil {
		t.Fatal(err)
	}
	admin := auth.WithUser(ctx, &auth.User{ID: "owner", Role: "admin", Status: "active"})
	if _, err = cs.(content.PageAccessService).SavePageAccess(admin, en.ID, content.PageAccessInput{Mode: "blocked"}, 0); err != nil {
		t.Fatal(err)
	}
	menu, err := ms.ByLocation(ctx, "primary")
	if err != nil || len(menu.Items) != 0 {
		t.Fatal("protected menu leak", err)
	}
	_, body := e.get("/linked-page")
	mustNotContain(t, body, "Private translation", `hreflang="en"`, "/en/linked-page")
	e.setting("site.front_page", en.ID)
	if _, err = cs.(content.PageAccessService).SavePageAccess(admin, ar.ID, content.PageAccessInput{Mode: "blocked"}, 0); err != nil {
		t.Fatal(err)
	}
	_, body = e.get("/sitemap.xml")
	mustNotContain(t, body, "<loc>http://example.test/</loc>", "/en/linked-page")
}

func TestPageAccessUnlockedOwnCommentsAndBlockedSubmission(t *testing.T) {
	e := bootReal(t)
	e.setting("site.locale", "en")
	comments, it := openComments(t, e)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	c, err := comments.SubmitComment(context.Background(), content.CommentInput{ContentID: it.ID, AuthorName: "Fixture author", AuthorEmail: "fixture@example.invalid", Body: "Protected comment fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = comments.SetCommentStatus(context.Background(), c.ID, content.CommentApproved); err != nil {
		t.Fatal(err)
	}
	owner := auth.WithUser(context.Background(), &auth.User{ID: "owner", Role: "admin", Status: "active"})
	if _, err = cs.(content.PageAccessService).SavePageAccess(owner, it.ID, content.PageAccessInput{Mode: "password", Password: "comments fixture password"}, 0); err != nil {
		t.Fatal(err)
	}
	_, body := e.get("/ar/post/hello")
	mustNotContain(t, body, "Protected comment fixture")
	token := csrfFrom(t, body)
	form := url.Values{"_csrf": {token}, "content_id": {it.ID}, "name": {"Fixture"}, "email": {"fixture@example.invalid"}, "body": {"Rejected before unlock"}, "_back": {"/ar/post/hello"}}
	r, gateBody := e.do("POST", "/forms/comment", form)
	mustContain(t, gateBody, `lang="ar"`)
	if r.StatusCode != 401 {
		t.Fatal("locked comment submission", r.StatusCode)
	}
	r, _ = e.do("POST", "/_page/"+it.ID+"/unlock", url.Values{"_csrf": {token}, "password": {"comments fixture password"}})
	if r.StatusCode != 303 {
		t.Fatal("unlock", r.StatusCode)
	}
	_, body = e.get("/ar/post/hello")
	mustContain(t, body, "Protected comment fixture", `name="content_id" value="`+it.ID+`"`)
	form.Set("body", "Allowed after unlock")
	form.Set("_csrf", csrfFrom(t, body))
	r, _ = e.do("POST", "/forms/comment", form)
	if r.StatusCode != 303 {
		t.Fatal("unlocked submission", r.StatusCode)
	}
	list, err := comments.Comments(context.Background(), content.CommentQuery{Status: content.CommentPending})
	if err != nil || list.Total != 1 || list.Items[0].Body != "Allowed after unlock" {
		t.Fatal("submission boundary", err)
	}
	draft, err := cs.Create(context.Background(), content.Input{Type: "post", Title: "Draft protected", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cs.(content.PageAccessService).SavePageAccess(owner, draft.ID, content.PageAccessInput{Mode: "blocked", Message: "Private draft policy message"}, 0); err != nil {
		t.Fatal(err)
	}
	form.Set("content_id", draft.ID)
	r, gateBody = e.do("POST", "/forms/comment", form)
	if r.StatusCode != 404 {
		t.Fatal("draft comment target", r.StatusCode)
	}
	mustNotContain(t, gateBody, "Private draft policy message")

}
