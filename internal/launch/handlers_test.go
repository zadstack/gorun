package launch_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/launch"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

func bootLaunch(t *testing.T) (*kernel.App, *httptest.Server, *http.Client) {
	t.Helper()
	app, err := kernel.Boot(context.Background(), config.Config{
		Env: "dev", DataDir: t.TempDir(), BaseURL: "https://example.test", SecretKey: make([]byte, 32),
	}, []kernel.Module{settingsmod.New(), auth.New(), launch.New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return app, srv, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func csrfFrom(t *testing.T, body string) string {
	t.Helper()
	const marker = `name="_csrf" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatal("csrf missing")
	}
	rest := body[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	return rest[:j]
}

func TestLaunchPolishAndStartTitles(t *testing.T) {
	app, srv, c := bootLaunch(t)
	resp, err := c.Get(srv.URL + httpx.LaunchPolishPath)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "launch-brand") {
		t.Fatalf("polish.js: %d %s", resp.StatusCode, body)
	}

	svc, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "ops@example.test", "Ops", "correct horse battery", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(context.Background(), "site.title", "Studio"); err != nil {
		t.Fatal(err)
	}

	resp, err = c.Get(srv.URL + "/start")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/start: %d", resp.StatusCode)
	}
	html := string(page)
	for _, want := range []string{
		`<title>ابدأ موقعك — Studio</title>`,
		`href="/static/launch.css"`,
		`src="/static/launch-polish.js"`,
		`action="/start"`,
		`/start/login`,
		`/admin/login`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("/start missing %q", want)
		}
	}

	resp, err = c.Get(srv.URL + "/start/login?hl=en")
	if err != nil {
		t.Fatal(err)
	}
	loginPage, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(loginPage), `<title>Sign in — Studio</title>`) {
		t.Fatalf("/start/login title: %d %s", resp.StatusCode, loginPage)
	}
	if strings.Contains(string(loginPage), "· GoRun") {
		t.Fatal("launch login must not use admin title punctuation")
	}
}

func TestStartLoginCustomerSession(t *testing.T) {
	app, srv, c := bootLaunch(t)
	svc, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := svc.Create(context.Background(), "ops@example.test", "Ops", "correct horse battery", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(context.Background(), "cust@example.test", "Cust", "correct horse battery", auth.RoleCustomer); err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL + "/start/login?hl=en")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	form := url.Values{
		"_csrf":    {csrfFrom(t, string(body))},
		"email":    {"cust@example.test"},
		"password": {"correct horse battery"},
	}
	resp, err = c.PostForm(srv.URL+"/start/login", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/account" {
		t.Fatalf("login redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
