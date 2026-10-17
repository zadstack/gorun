package webhooks_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/webhooks"
)

// Query-string tokens in a webhook URL appear only in storage, the single
// webhook (edit) views and the create response, never in lists.
func TestWebhookListsRedactURLQuery(t *testing.T) {
	app := apitest.Boot(t, webhooks.New())
	c := apitest.NewClient(t, app, webhooks.Perm)
	full := "https://crm.example.com/hook?token=s3cret"
	res := c.Do("POST", "/v1/webhooks", map[string]any{"name": "CRM", "url": full, "events": []string{"*"}}, nil)
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	id := regexp.MustCompile(`"id":"([^"]+)"`).FindStringSubmatch(res.Body.String())[1]
	if res := c.Do("GET", "/v1/webhooks", nil, nil); res.Code != http.StatusOK || strings.Contains(res.Body.String(), "s3cret") || !strings.Contains(res.Body.String(), "https://crm.example.com/hook?…") {
		t.Fatalf("API list: %d %s", res.Code, res.Body)
	}
	if res := c.Do("GET", "/v1/webhooks/"+id, nil, nil); !strings.Contains(res.Body.String(), "s3cret") {
		t.Fatalf("API get keeps the full URL: %s", res.Body)
	}

	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) string {
		r, err := hc.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return string(b)
	}
	m := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(get("/admin/login"))
	if m == nil {
		t.Fatal("no csrf on the login page")
	}
	r, err := hc.PostForm(srv.URL+"/admin/login", url.Values{"_csrf": {m[1]}, "email": {c.User.Email}, "password": {"correct horse battery staple"}})
	if err != nil || r.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %v %v", r, err)
	}
	r.Body.Close()
	if page := get("/admin/webhooks"); strings.Contains(page, "s3cret") || !strings.Contains(page, "https://crm.example.com/hook?…") {
		t.Fatalf("admin list must redact the query:\n%s", page)
	}
	if page := get("/admin/webhooks/" + id); !strings.Contains(page, "s3cret") {
		t.Fatal("the edit form keeps the full URL")
	}
}
