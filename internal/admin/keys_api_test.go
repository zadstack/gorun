package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

type keySecret struct {
	Data struct {
		Key struct {
			ID          string   `json:"id"`
			Scopes      []string `json:"scopes"`
			RotatedFrom string   `json:"rotated_from"`
			Status      string   `json:"status"`
		} `json:"key"`
		Token string `json:"token"`
	} `json:"data"`
}

func TestKeyConsoleAPIGuardsScopesAndOwnership(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	scoped := apitest.NewClient(t, app, "api_keys.manage", "content.view")

	// Keys cannot mint keys: creation is session-only, so a leaked key can
	// never spawn children that outlive its revocation.
	for _, scopes := range [][]string{{"*"}, {"users.manage"}, {"content.view"}} {
		res := scoped.Do("POST", "/v1/keys", map[string]any{"name": "x", "scopes": scopes}, nil)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), `"session_required"`) {
			t.Fatalf("creating a key %v with an API key: %d %s", scopes, res.Code, res.Body)
		}
	}
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	token, k, err := keys.CreateKind(context.Background(), auth.KeyKindAPI, "reader", scoped.User.ID, []string{"content.view"}, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	var created keySecret
	created.Data.Token, created.Data.Key.ID = token, k.ID

	// Another admin's key can list and revoke, but not rotate someone else's key.
	other := apitest.NewClient(t, app, "api_keys.manage", "content.view")
	if res := other.Do("POST", "/v1/keys/"+created.Data.Key.ID+"/rotate", map[string]any{}, nil); res.Code != http.StatusForbidden {
		t.Fatalf("rotating another user's key: %d %s", res.Code, res.Body)
	}
	res := scoped.Do("POST", "/v1/keys/"+created.Data.Key.ID+"/rotate", map[string]any{"grace_seconds": 3600}, nil)
	var rotated keySecret
	_ = json.Unmarshal(res.Body.Bytes(), &rotated)
	if res.Code != http.StatusCreated || rotated.Data.Key.RotatedFrom != created.Data.Key.ID || rotated.Data.Token == created.Data.Token {
		t.Fatalf("rotate: %d %s", res.Code, res.Body)
	}
	if res := scoped.Do("POST", "/v1/keys/"+created.Data.Key.ID+"/rotate", map[string]any{"grace_seconds": 999999999}, nil); res.Code != http.StatusUnprocessableEntity && res.Code != http.StatusForbidden && res.Code != http.StatusConflict {
		t.Fatalf("invalid grace: %d %s", res.Code, res.Body)
	}
	if res := other.Do("DELETE", "/v1/keys/"+rotated.Data.Key.ID, nil, nil); res.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", res.Code, res.Body)
	}
	res = scoped.Do("GET", "/v1/keys?status=revoked", nil, nil)
	var list struct {
		Data []struct {
			ID, Status string
		} `json:"data"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &list)
	if res.Code != http.StatusOK || len(list.Data) != 1 || list.Data[0].ID != rotated.Data.Key.ID {
		t.Fatalf("list revoked: %d %s", res.Code, res.Body)
	}
	if res := apitest.NewClient(t, app, "content.view").Do("GET", "/v1/keys", nil, nil); res.Code != http.StatusForbidden {
		t.Fatalf("listing keys without api_keys.manage: %d", res.Code)
	}
}

func TestKeyConsoleAPICreateIsSessionOnly(t *testing.T) {
	app := apitest.Boot(t, admin.New())
	owner := apitest.NewClient(t, app) // fresh admin; password "correct horse battery staple"
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) string {
		res, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var b strings.Builder
		_, _ = io.Copy(&b, res.Body)
		return b.String()
	}
	m := csrfFieldRe.FindStringSubmatch(get("/admin/login"))
	if m == nil {
		t.Fatal("no csrf on the login page")
	}
	res, err := c.PostForm(srv.URL+"/admin/login", url.Values{"_csrf": {m[1]}, "email": {owner.User.Email}, "password": {"correct horse battery staple"}})
	if err != nil || res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %v %v", res, err)
	}
	res.Body.Close()
	meta := csrfMetaRe.FindStringSubmatch(get("/admin"))
	if meta == nil {
		t.Fatal("no csrf meta")
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/keys", strings.NewReader(`{"name":"ci","scopes":["content.view"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", meta[1])
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var created keySecret
	_ = json.NewDecoder(res.Body).Decode(&created)
	if res.StatusCode != http.StatusCreated || created.Data.Token == "" {
		t.Fatalf("session create: %d %+v", res.StatusCode, created)
	}
}
