package content_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func resolver(t *testing.T, app *kernel.App) content.PreviewResolver {
	t.Helper()
	r, ok := kernel.Get[content.PreviewResolver](app.Deps, content.ServicePreview)
	if !ok {
		t.Fatal("preview resolver not registered")
	}
	return r
}

// The editor creates a link (JSON for scripts), only the token's SHA-256 is
// stored, create and revoke are audited, and a revoked token stops resolving.
func TestPreviewLinkAdminCreateListRevoke(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Draft to share", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/content/post/" + it.ID
	_, page := s.do("GET", path, nil, nil)
	if !strings.Contains(page, `formaction="`+path+`/preview-links"`) || strings.Contains(page, "data-preview-link=") {
		t.Fatal("editor misses the preview link panel")
	}
	resp, body := s.do("POST", path+"/preview-links", url.Values{"_csrf": {s.csrf(page)}, "preview_ttl": {"1h"}}, jsonHeaders)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", resp.StatusCode, body)
	}
	var env struct {
		Data content.PreviewLinkCreated `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(env.Data.URL[strings.Index(env.Data.URL, content.PreviewPathPrefix):], content.PreviewPathPrefix)
	if len(token) != 43 {
		t.Fatalf("token %q from %s", token, env.Data.URL)
	}
	var stored, created, expires string
	if err := s.app.Deps.DB.QueryRow(`SELECT token_hash, created_at, expires_at FROM content_preview_links WHERE id = ?`, env.Data.ID).Scan(&stored, &created, &expires); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(token))
	if stored != hex.EncodeToString(sum[:]) {
		t.Fatal("stored value is not the token's SHA-256")
	}
	var leaks int
	s.app.Deps.DB.QueryRow(`SELECT COUNT(*) FROM content_preview_links WHERE token_hash LIKE ? OR id LIKE ?`, "%"+token+"%", "%"+token+"%").Scan(&leaks)
	s.app.Deps.DB.QueryRow(`SELECT COUNT(*) + ? FROM audit_log WHERE meta_json LIKE ?`, leaks, "%"+token+"%").Scan(&leaks)
	if leaks != 0 {
		t.Fatal("token stored in clear")
	}
	c0, _ := time.Parse(time.RFC3339, created)
	e0, _ := time.Parse(time.RFC3339, expires)
	if e0.Sub(c0) != time.Hour {
		t.Fatalf("expiry %s created %s", expires, created)
	}
	got, err := resolver(t, s.app).ResolvePreview(ctx, token)
	if err != nil || got.ID != it.ID {
		t.Fatalf("resolve %v", err)
	}
	_, page = s.do("GET", path, nil, nil)
	if !strings.Contains(page, `data-preview-link="`+env.Data.ID+`"`) {
		t.Fatal("active link not listed")
	}
	// No-JS revoke redirects back to the editor.
	resp, _ = s.do("POST", path+"/preview-links/"+env.Data.ID+"/revoke", url.Values{"_csrf": {s.csrf(page)}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke %d", resp.StatusCode)
	}
	if _, err := resolver(t, s.app).ResolvePreview(ctx, token); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("revoked token resolves: %v", err)
	}
	for _, action := range []string{"content.preview_link_create", "content.preview_link_revoke"} {
		var n int
		s.app.Deps.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND target_id = ?`, action, it.ID).Scan(&n)
		if n != 1 {
			t.Errorf("audit %s: %d", action, n)
		}
	}
	// Invalid lifetimes are refused; the no-JS create shows the link once.
	resp, body = s.do("POST", path+"/preview-links", url.Values{"_csrf": {s.csrf(page)}, "preview_ttl": {"30d"}}, jsonHeaders)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad ttl %d %s", resp.StatusCode, body)
	}
	resp, body = s.do("POST", path+"/preview-links", url.Values{"_csrf": {s.csrf(page)}}, nil)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(body, `data-preview-result`) || !strings.Contains(body, content.PreviewPathPrefix) {
		t.Fatalf("no-JS create %d", resp.StatusCode)
	}
}

func TestPreviewLinkPermissionsAndAPI(t *testing.T) {
	s := loggedIn(t, "author")
	cs := contentService(t, s.app)
	ctx := context.Background()
	other := mustUser(t, s.app, "other@example.com", "editor")
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Someone else's draft", AuthorID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/content/post/new", nil, nil)
	resp, _ := s.do("POST", "/admin/content/post/"+it.ID+"/preview-links", url.Values{"_csrf": {s.csrf(page)}}, jsonHeaders)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("author on foreign item %d", resp.StatusCode)
	}
	var n int
	s.app.Deps.DB.QueryRow(`SELECT COUNT(*) FROM content_preview_links`).Scan(&n)
	if n != 0 {
		t.Fatalf("links stored: %d", n)
	}

	// REST: create (default 24 h), list without tokens, revoke.
	c := newAPIClient(t, s.app, other)
	code, env := c.do("POST", "/v1/content/"+it.ID+"/preview-links", map[string]string{"ttl": "7d"})
	if code != http.StatusCreated || !env.OK {
		t.Fatalf("api create %d %+v", code, env)
	}
	var created content.PreviewLinkCreated
	json.Unmarshal(env.Data, &created)
	if !strings.Contains(created.URL, content.PreviewPathPrefix) {
		t.Fatalf("url %q", created.URL)
	}
	code, env = c.do("GET", "/v1/content/"+it.ID+"/preview-links", nil)
	if code != 200 || strings.Contains(string(env.Data), "url") || !strings.Contains(string(env.Data), created.ID) {
		t.Fatalf("api list %d %s", code, env.Data)
	}
	if code, _ = c.do("DELETE", "/v1/content/"+it.ID+"/preview-links/"+created.ID, nil); code != 200 {
		t.Fatalf("api revoke %d", code)
	}
	if code, _ = c.do("GET", "/v1/content/"+it.ID+"/preview-links", nil); code != 200 {
		t.Fatalf("api list after revoke %d", code)
	}
	if code, env = c.do("DELETE", "/v1/content/"+it.ID+"/preview-links/nope", nil); code != 404 {
		t.Fatalf("api revoke unknown %d", code)
	}
}
