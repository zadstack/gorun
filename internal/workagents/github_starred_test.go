package workagents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func starredResponse(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func starredFixture(start, count int) string {
	rows := []starredRecord{}
	for i := 0; i < count; i++ {
		rows = append(rows, starredRecord{StarredAt: "2026-10-14T00:00:00Z", Repo: starredRepository{ID: int64(start + i), FullName: fmt.Sprintf("owner/repo-%d", start+i), Stars: 12, HTMLURL: "https://evil.invalid/"}})
	}
	raw, _ := json.Marshal(rows)
	return string(raw)
}
func TestGitHubStarredPagesTimestampsDedupAndBoundedRegistryRun(t *testing.T) {
	s, ctx := setup(t, nil)
	calls := 0
	s.githubClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.github.com" || r.URL.Path != "/users/octocat/starred" || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Accept") != "application/vnd.github.star+json" || r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("page") != fmt.Sprint(calls) {
			t.Fatal(r.URL, r.Header)
		}
		if calls == 1 {
			return starredResponse(starredFixture(1, 100), 200), nil
		}
		return starredResponse(starredFixture(100, 2), 200), nil
	})}
	a, e := s.saveAgent(ctx, Agent{Name: "User starred", Enabled: true, ExecutionKind: "github_starred"})
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.run(ctx, a.ID, `{"username":"octocat"}`, "user-starred-dedup")
	if e != nil {
		t.Fatal(e)
	}
	payload, _ := json.Marshal(map[string]string{"run_id": id})
	if e = s.execute(context.Background(), payload); e != nil {
		t.Fatal(e)
	}
	runs, e := s.runs(ctx)
	if e != nil || len(runs) != 1 || runs[0].Status != "done" {
		t.Fatal(runs, e)
	}
	var out starredResult
	if e = json.Unmarshal([]byte(runs[0].Result), &out); e != nil {
		t.Fatal(e)
	}
	if calls != 2 || out.Pages != 2 || out.Truncated || len(out.Items) != 101 || out.Items[0].StarredAt != "2026-10-14T00:00:00Z" || out.Items[0].Repo.HTMLURL != "https://github.com/owner/repo-1" {
		t.Fatal(calls, out)
	}
	if e = s.execute(context.Background(), payload); e != nil || calls != 2 {
		t.Fatal("completed run replayed", e, calls)
	}
}
func TestGitHubStarredRejectsMalformedInputsResponsesAndRedirects(t *testing.T) {
	s, _ := setup(t, nil)
	calls := 0
	s.githubClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { calls++; return starredResponse("[]", 200), nil })}
	for _, input := range []string{`{"username":"https://evil.invalid"}`, `{"username":"../private"}`, `{"username":"a--b"}`, `{"username":"-a"}`, `{"username":"a-"}`, `{"username":"a","url":"https://evil.invalid"}`, `{"username":"a","token":"secret"}`, `{"username":"a"} {}`} {
		if _, e := s.githubStarred(context.Background(), input); e == nil {
			t.Fatal("invalid input", input)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
	for _, body := range []string{`null`, `{}`, `[{"repo":{"id":1,"full_name":"owner/repo"}}]`, `[{"starred_at":"2026-10-14T00:00:00Z","repo":{"id":1,"full_name":"../secret"}}]`, strings.Repeat("x", starredMaxPageBytes+1), starredFixture(1, 101)} {
		s.githubClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return starredResponse(body, 200), nil })}
		if _, e := s.githubStarred(context.Background(), `{"username":"octocat"}`); e != toolError("github_invalid_response") {
			t.Fatalf("malformed response: %v", e)
		}
	}
	calls = 0
	s.githubClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := starredResponse("", 302)
		resp.Header.Set("Location", "https://evil.invalid/private")
		return resp, nil
	})}
	if _, e := s.githubStarred(context.Background(), `{"username":"octocat"}`); e != toolError("github_upstream_failed") || calls != 1 {
		t.Fatal("redirect followed", e, calls)
	}
	for _, status := range []int{403, 429} {
		s.githubClient = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return starredResponse("", status), nil })}
		if _, e := s.githubStarred(context.Background(), `{"username":"octocat"}`); e != toolError("github_rate_limited") {
			t.Fatal(e)
		}
	}
}
func TestGitHubStarredTruncationIsExplicitAndNoForeignLinkTraversal(t *testing.T) {
	s, _ := setup(t, nil)
	calls := 0
	s.githubClient = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		r := starredResponse(starredFixture(1+(calls-1)*100, 100), 200)
		r.Header.Set("Link", `<http://127.0.0.1/private>; rel="next"`)
		return r, nil
	})}
	raw, e := s.githubStarred(context.Background(), `{"username":"octocat"}`)
	if e != nil {
		t.Fatal(e)
	}
	var out starredResult
	json.Unmarshal([]byte(raw), &out)
	if calls != 3 || len(out.Items) != 300 || !out.Truncated {
		t.Fatal(calls, len(out.Items), out.Truncated)
	}
}
func TestGitHubStarredMigrationPreservesExistingKindsAndReferences(t *testing.T) {
	d, e := db.Open(filepath.Join(t.TempDir(), "old.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	if e = db.Migrate(context.Background(), d, "auth", auth.New().Migrations()); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"0001_workagents.sql", "0002_execution_kind.sql", "0003_owner_permissions.sql"} {
		raw, e := files.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = d.Exec(string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	_, e = d.Exec(`INSERT INTO users(id,email,role,created_at,updated_at)VALUES('owner','migration@example.test','admin','','');INSERT INTO workagents_agents(id,owner_id,name,created_at,updated_at,execution_kind)VALUES('a','owner','a','','','github_stars'),('b','owner','b','','','content_draft');INSERT INTO workagents_workflows(id,owner_id,agent_id,name,created_at,updated_at)VALUES('wf','owner','a','wf','','');INSERT INTO workagents_runs(id,owner_id,agent_id,created_at)VALUES('run','owner','a','');`)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := files.ReadFile("migrations/0004_github_starred.sql")
	if _, e = d.Exec(string(raw)); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = d.QueryRow(`SELECT count(*) FROM workagents_agents WHERE (id='a' AND execution_kind='github_stars') OR (id='b' AND execution_kind='content_draft')`).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	if _, e = d.Exec(`UPDATE workagents_agents SET execution_kind='github_starred' WHERE id='a'`); e != nil {
		t.Fatal(e)
	}
	if e = d.QueryRow(`SELECT (SELECT count(*) FROM workagents_workflows)+(SELECT count(*) FROM workagents_runs)`).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	rows, e := d.Query(`PRAGMA foreign_key_check`)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key changed")
	}
}
