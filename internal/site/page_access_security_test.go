package site

import (
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPageRuleValidationAndUnlockIsolation(t *testing.T) {
	hash, e := auth.HashPassword("safe fixture password")
	if e != nil {
		t.Fatal(e)
	}
	id := "01M4QAQM4RP3T0YSGHN6N7HJWS"
	rule := PageRule{ContentID: id, Mode: "password", Revision: 1, PasswordHash: hash}
	if !validPageRules([]PageRule{rule}) {
		t.Fatal("valid rejected")
	}
	for _, change := range []func(*PageRule){func(p *PageRule) { p.ContentID = "/private" }, func(p *PageRule) { p.Mode = "banner" }, func(p *PageRule) { p.Revision = -1 }, func(p *PageRule) { p.PasswordHash = strings.Replace(hash, "m=19456", "m=999999999", 1) }, func(p *PageRule) { p.Message = strings.Repeat("x", 8001) }} {
		bad := rule
		change(&bad)
		if validPageRules([]PageRule{bad}) {
			t.Fatal("unsafe rule accepted")
		}
	}
	if validPageRules([]PageRule{rule, rule}) {
		t.Fatal("duplicates accepted")
	}
	key := []byte("fixture")
	req := httptest.NewRequest("GET", "http://site.test/private", nil)
	req.AddCookie(&http.Cookie{Name: pageCookie(id), Value: signUnlock(key, "scope-one", time.Now().Add(time.Minute).Unix())})
	if !pageUnlocked(req, key, id, "scope-one") || pageUnlocked(req, key, "other", "scope-one") || pageUnlocked(req, key, id, "scope-two") || pageUnlocked(req, []byte("other"), id, "scope-one") {
		t.Fatal("cookie scope")
	}
}

func TestPageAccessHostRulePrecedence(t *testing.T) {
	id := "01M4QAQM4RP3T0YSGHN6N7HJWS"
	for _, localMode := range []string{"public", "blocked", "password"} {
		for _, hostMode := range []string{"public", "blocked", "password"} {
			local := content.PageAccess{ContentID: id, Mode: localMode, PasswordHash: "local"}
			got := choosePageAccess(local, []PageRule{{ContentID: strings.ToLower(id), Mode: hostMode, PasswordHash: "host"}})
			want := hostMode
			if hostMode == "public" {
				want = localMode
			}
			if got.Mode != want {
				t.Fatalf("%s/%s -> %s", hostMode, localMode, got.Mode)
			}
			if hostMode != "public" && got.PasswordHash != "host" {
				t.Fatal("tenant overrides host password")
			}
		}
	}
}

func TestPageAccessAdminWarningUsesCanonicalID(t *testing.T) {
	id := "01M4QAQM4RP3T0YSGHN6N7HJWS"
	for _, mode := range []string{"blocked", "password"} {
		if !platformPageRestricted(Availability{Mode: "public", PageRules: []PageRule{{ContentID: strings.ToLower(id), Mode: mode}}}, id) {
			t.Fatal("missing host warning", mode)
		}
	}
	if platformPageRestricted(Availability{Mode: "public", PageRules: []PageRule{{ContentID: id, Mode: "public"}}}, id) {
		t.Fatal("false restriction warning")
	}
}
