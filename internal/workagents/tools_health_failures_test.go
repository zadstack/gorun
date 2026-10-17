package workagents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (failingBody) Close() error             { return nil }

func stubGitHub(s *service, fn func(*http.Request) (*http.Response, error)) {
	s.githubClient = &http.Client{Transport: transportFunc(fn)}
}

func reply(status int, body string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

func TestGitHubStarsMapsUpstreamFailuresToToolErrors(t *testing.T) {
	s, _ := setup(t, nil)
	input := `{"repository":"octocat/hello-world"}`
	cases := []struct {
		name string
		fn   func(*http.Request) (*http.Response, error)
		want toolError
	}{
		{"transport", func(*http.Request) (*http.Response, error) { return nil, errors.New("dial failed") }, "github_upstream_failed"},
		{"forbidden", func(*http.Request) (*http.Response, error) { return reply(403, "{}") }, "github_rate_limited"},
		{"throttled", func(*http.Request) (*http.Response, error) { return reply(429, "{}") }, "github_rate_limited"},
		{"server error", func(*http.Request) (*http.Response, error) { return reply(500, "{}") }, "github_upstream_failed"},
		{"oversized", func(*http.Request) (*http.Response, error) { return reply(200, strings.Repeat(" ", (1<<20)+2)) }, "github_invalid_response"},
		{"body error", func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: failingBody{}, Header: make(http.Header)}, nil
		}, "github_invalid_response"},
		{"wrong repository", func(*http.Request) (*http.Response, error) {
			return reply(200, `{"full_name":"evil/repo","stargazers_count":1,"forks_count":1,"open_issues_count":1}`)
		}, "github_invalid_response"},
		{"missing counts", func(*http.Request) (*http.Response, error) { return reply(200, `{"full_name":"octocat/hello-world"}`) }, "github_invalid_response"},
		{"negative stars", func(*http.Request) (*http.Response, error) {
			return reply(200, `{"full_name":"octocat/hello-world","stargazers_count":-1,"forks_count":1,"open_issues_count":1}`)
		}, "github_invalid_response"},
	}
	for _, c := range cases {
		stubGitHub(s, c.fn)
		out, err := s.githubStars(context.Background(), input)
		if err != c.want || out != "" {
			t.Fatalf("%s: got %q %v want %v", c.name, out, err, c.want)
		}
		if err.Error() != string(c.want) {
			t.Fatal("tool error text", err.Error())
		}
	}
}

func TestGitHubStarredMapsUpstreamFailuresToToolErrors(t *testing.T) {
	s, _ := setup(t, nil)
	cases := []struct {
		name string
		fn   func(*http.Request) (*http.Response, error)
		want toolError
	}{
		{"transport", func(*http.Request) (*http.Response, error) { return nil, errors.New("dial failed") }, "github_upstream_failed"},
		{"server error", func(*http.Request) (*http.Response, error) { return reply(502, "") }, "github_upstream_failed"},
		{"null body", func(*http.Request) (*http.Response, error) { return reply(200, "null") }, "github_invalid_response"},
		{"body error", func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: failingBody{}, Header: make(http.Header)}, nil
		}, "github_invalid_response"},
		{"bad timestamp", func(*http.Request) (*http.Response, error) {
			return reply(200, `[{"starred_at":"yesterday","repo":{"id":1,"full_name":"a/b","stargazers_count":1}}]`)
		}, "github_invalid_response"},
	}
	for _, c := range cases {
		stubGitHub(s, c.fn)
		if _, err := s.githubStarred(context.Background(), `{"username":"octocat"}`); err != c.want {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	if _, err := s.githubStarred(context.Background(), `{"username":"bad--name"}`); err != toolError("invalid_github_username") {
		t.Fatal(err)
	}
}

func TestContentDraftValidatesInputAndRequiresContentService(t *testing.T) {
	s, ctx := setup(t, nil)
	for _, input := range []string{
		`{"type":"product","title":"T"}`,
		`{"type":"post","title":"  "}`,
		`{"type":"post","title":"` + strings.Repeat("t", 301) + `"}`,
		`{"type":"page","title":"T","locale":"fr"}`,
		`not json`,
	} {
		if _, err := s.contentDraft(ctx, "run", input); err != toolError("invalid_tool_input") {
			t.Fatalf("invalid draft input %q: %v", input, err)
		}
	}
	if _, err := s.contentDraft(ctx, "run", `{"type":"page","title":"T","body":"B","locale":"en"}`); err != toolError("content_service_unavailable") {
		t.Fatal("draft without content service", err)
	}
}

func TestContentDraftRefusesToReuseAnotherAuthorsSlug(t *testing.T) {
	s, ctx, app := bootWorkspace(t)
	svc, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	other, err := users.Create(context.Background(), "other-author@example.com", "Other author", "correct horse battery", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Create(ctx, content.Input{Type: "post", Slug: "agent-draft-taken", Title: "Existing", Body: "Original", AuthorID: other.ID, Status: content.Status("draft"), Locale: "ar"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.contentDraft(ctx, "TAKEN", `{"type":"post","title":"Overwrite","body":"New"}`); err != toolError("content_draft_conflict") {
		t.Fatal("another author's draft reused", err)
	}
	it, err := svc.GetBySlug(ctx, "post", "agent-draft-taken", "ar")
	if err != nil || it.Title != "Existing" || it.AuthorID != other.ID {
		t.Fatal("existing draft modified", it, err)
	}
}

func TestHealthChecksReportFailuresPausedImportsAndErrors(t *testing.T) {
	// Booted deps: the provider readiness probe needs the settings store.
	s, ctx, _ := bootWorkspace(t)
	if out, err := s.healthChecks(ctx, "not checks"); err != nil || out != "not checks" {
		t.Fatal("non-check value not passed through", out, err)
	}
	var nilChecks *[]admin.HealthCheck
	if out, err := s.healthChecks(ctx, nilChecks); err != nil || out != any(nilChecks) {
		t.Fatal("nil checks not passed through", out, err)
	}
	a, err := s.saveAgent(ctx, Agent{Name: "Agent", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.d.DB.Exec(`INSERT INTO workagents_runs(id,owner_id,agent_id,status,created_at)VALUES('failed-run',?,?,'failed','2020-01-01T00:00:00Z')`, a.OwnerID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.d.DB.Exec(`INSERT INTO workagents_workflows(id,owner_id,agent_id,source_id,name,enabled,created_at,updated_at)VALUES('imported',?,?,'src-1','Imported',0,'2020-01-01T00:00:00Z','2020-01-01T00:00:00Z')`, a.OwnerID, a.ID); err != nil {
		t.Fatal(err)
	}
	checks := []admin.HealthCheck{}
	if _, err = s.healthChecks(ctx, &checks); err != nil {
		t.Fatal(err)
	}
	names := map[string]admin.HealthCheck{}
	for _, c := range checks {
		names[c.Name] = c
	}
	if c := names["أدوات الإيجنتس المنقولة"]; c.Status != admin.CheckOK {
		t.Fatal("tool check", checks)
	}
	for _, n := range []string{"تشغيل الإيجنتس", "الفلوهات المنقولة", "مزود AI للإيجنتس"} {
		if c, ok := names[n]; !ok || c.Status != admin.CheckWarn {
			t.Fatal("missing warning", n, checks)
		}
	}
	// Each query failure is returned before any check is appended.
	for _, table := range []string{"workagents_runs", "workagents_workflows", "workagents_agents"} {
		t.Run(table, func(t *testing.T) {
			s, ctx := setup(t, nil)
			if _, err := s.d.DB.Exec(`DROP TABLE ` + table); err != nil {
				t.Fatal(err)
			}
			checks := []admin.HealthCheck{}
			if _, err := s.healthChecks(ctx, &checks); err == nil || len(checks) != 0 {
				t.Fatal("health query failure hidden", err, checks)
			}
		})
	}
}

// Without a settings store the default provider cannot be ready: the health
// check must warn instead of dereferencing the nil store.
func TestHealthChecksWarnsWithoutSettingsStore(t *testing.T) {
	s, ctx := setup(t, nil)
	if s.d.Settings != nil {
		t.Fatal("setup unexpectedly wired a settings store")
	}
	if _, err := s.saveAgent(ctx, Agent{Name: "Agent", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	checks := []admin.HealthCheck{}
	if _, err := s.healthChecks(ctx, &checks); err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if c.Name == "مزود AI للإيجنتس" && c.Status == admin.CheckWarn {
			return
		}
	}
	t.Fatal("missing provider warning", checks)
}
