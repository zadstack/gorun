package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mcp"
	publicsite "github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/theme"
)

func permissionSite(t *testing.T) (*site, content.Service) {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{"theme.json": `{"name":"Permission fixture"}`, "templates/index.html": `HOME {{with .Content}}{{.Title}} {{safe .Body}}{{end}}`, "templates/single.html": `{{with .Content}}{{.Title}} {{safe .Body}}{{end}}`, "templates/page.html": `{{with .Content}}{{.Title}} {{safe .Body}}{{end}}`} {
		path := filepath.Join(root, "default", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	app := apitest.Boot(t, content.New(), theme.New(theme.WithThemesDir(root)), publicsite.New(), admin.New(), mcp.New())
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	s := &site{t: t, app: app, srv: srv, c: newClient()}
	cs, ok := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	if !ok {
		t.Fatal("missing content service")
	}
	return s, cs
}

func TestStaffCannotReadOthersUnpublishedContent(t *testing.T) {
	for _, role := range []string{"author", "contributor"} {
		t.Run(role, func(t *testing.T) {
			s, cs := permissionSite(t)
			s.login("writer@example.com", role, "en")
			other := s.createUser("editor@example.com", "editor", "en")
			for _, status := range []content.Status{"draft", "private"} {
				item, err := cs.Create(auth.WithUser(context.Background(), other), content.Input{Type: "post", Slug: "secret-" + string(status), Title: "Other private title", Body: "<p>CONFIDENTIAL BODY</p>", Status: status})
				if err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{"/api/v1/content/" + item.ID, "/api/v1/content/" + item.ID + "/revisions", "/api/v1/content?status=" + string(status), "/post/" + item.Slug} {
					t.Run(string(status)+path, func(t *testing.T) {
						resp, body := s.do("GET", path, nil)
						if strings.Contains(body, "CONFIDENTIAL BODY") {
							t.Fatalf("%s exposes another user's %s body (HTTP %d)", path, status, resp.StatusCode)
						}
					})
				}
			}
		})
	}
}

func TestNonPublicTypeCannotLeakThroughFrontPage(t *testing.T) {
	s, cs := permissionSite(t)
	ctx := context.Background()
	if err := cs.SaveType(ctx, content.Type{Slug: "invoice", Public: false, Name: map[string]string{"en": "Invoice"}}); err != nil {
		t.Fatal(err)
	}
	item, err := cs.Create(ctx, content.Input{Type: "invoice", Title: "Internal invoice", Body: "<p>CONFIDENTIAL INVOICE</p>", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.app.Deps.Settings.Set(ctx, "site.front_page", item.ID); err != nil {
		t.Fatal(err)
	}
	resp, body := s.do("GET", "/", nil)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "CONFIDENTIAL INVOICE") {
		t.Fatalf("non-public type exposed by front-page setting (HTTP %d): %s", resp.StatusCode, body)
	}
}

func TestContentPrivacyPreservesPublishedAndOwnDraftReads(t *testing.T) {
	for _, role := range []string{"author", "contributor"} {
		t.Run(role, func(t *testing.T) {
			s, cs := permissionSite(t)
			current := s.login("writer@example.com", role, "en")
			other := s.createUser("editor@example.com", "editor", "en")
			ctx := context.Background()
			published, err := cs.Create(auth.WithUser(ctx, other), content.Input{Type: "post", Slug: "published", Title: "Published", Body: "<p>PUBLIC BODY</p>", Status: "published"})
			if err != nil {
				t.Fatal(err)
			}
			own, err := cs.Create(auth.WithUser(ctx, current), content.Input{Type: "post", Slug: "own-draft", Title: "Mine", Body: "<p>OWN DRAFT BODY</p>"})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				item   *content.Item
				marker string
			}{{published, "PUBLIC BODY"}, {own, "OWN DRAFT BODY"}} {
				for _, path := range []string{"/post/" + tc.item.Slug, "/api/v1/content/" + tc.item.ID, "/api/v1/content?status=" + string(tc.item.Status)} {
					resp, body := s.do("GET", path, nil)
					if resp.StatusCode != http.StatusOK || !strings.Contains(body, tc.marker) {
						t.Fatalf("allowed %s read blocked (HTTP %d)", path, resp.StatusCode)
					}
				}
			}
		})
	}
}

func TestPublishedContentDoesNotExposePrivateRevisionsOrTranslationMembers(t *testing.T) {
	s, cs := permissionSite(t)
	s.login("writer@example.com", "author", "en")
	other := s.createUser("editor@example.com", "editor", "en")
	ctx := auth.WithUser(context.Background(), other)
	item, err := cs.Create(ctx, content.Input{Type: "post", Slug: "translated", Title: "Article", Body: "<p>SECRET EARLIER DRAFT</p>", Locale: "ar"})
	if err != nil {
		t.Fatal(err)
	}
	in := content.InputFromItem(item)
	in.Status, in.Body = "published", "<p>PUBLIC BODY</p>"
	item, err = cs.Update(ctx, item.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	translated, err := cs.CreateTranslation(ctx, item.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	in = content.InputFromItem(translated)
	in.Body = "<p>SECRET TRANSLATION DRAFT</p>"
	if _, err = cs.Update(ctx, translated.ID, in); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/content/" + item.ID + "/revisions", "/api/v1/content/" + item.ID + "/translations"} {
		resp, body := s.do("GET", path, nil)
		if strings.Contains(body, "SECRET EARLIER DRAFT") || strings.Contains(body, "SECRET TRANSLATION DRAFT") {
			t.Fatalf("%s leaked history/group members (HTTP %d)", path, resp.StatusCode)
		}
	}
}

type permissionBearer struct{ token string }

func (b permissionBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestLowRoleKeysRespectContentPrivacy(t *testing.T) {
	for _, role := range []string{"author", "contributor"} {
		t.Run(role, func(t *testing.T) {
			s, cs := permissionSite(t)
			owner := s.createUser("writer@example.com", role, "en")
			editor := s.createUser("editor@example.com", "editor", "en")
			ctx := context.Background()
			keys, _ := kernel.Get[auth.KeyService](s.app.Deps, kernel.ServiceAPIKeys)
			token, _, err := keys.Create(ctx, "restricted", owner.ID, []string{"content.view"}, 10000, "")
			if err != nil {
				t.Fatal(err)
			}
			private, err := cs.Create(auth.WithUser(ctx, editor), content.Input{Type: "post", Title: "Private", Body: "<p>KEY MUST NOT READ</p>"})
			if err != nil {
				t.Fatal(err)
			}
			published, err := cs.Create(auth.WithUser(ctx, editor), content.Input{Type: "post", Title: "Public", Body: "<p>KEY PUBLIC BODY</p>", Status: "published"})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/v1/content/" + private.ID, "/api/v1/content?status=draft&author_id=" + editor.ID} {
				resp, body := s.do("GET", path, nil, "Authorization", "Bearer "+token)
				if strings.Contains(body, "KEY MUST NOT READ") {
					t.Fatalf("REST key leaked %s (HTTP %d)", path, resp.StatusCode)
				}
			}
			own, err := cs.Create(auth.WithUser(ctx, owner), content.Input{Type: "post", Title: "Own key draft", Body: "<p>KEY OWN DRAFT</p>"})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				item   *content.Item
				marker string
			}{{published, "KEY PUBLIC BODY"}, {own, "KEY OWN DRAFT"}} {
				for _, path := range []string{"/api/v1/content/" + tc.item.ID} {
					resp, body := s.do("GET", path, nil, "Authorization", "Bearer "+token)
					if resp.StatusCode != http.StatusOK || !strings.Contains(body, tc.marker) {
						t.Fatalf("allowed REST key read blocked: %s HTTP %d", path, resp.StatusCode)
					}
				}
			}
			tr := &sdk.StreamableClientTransport{Endpoint: s.srv.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1, HTTPClient: &http.Client{Transport: permissionBearer{token: token}}}
			client, err := sdk.NewClient(&sdk.Implementation{Name: "privacy-test", Version: "v0"}, nil).Connect(ctx, tr, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			for _, call := range []struct {
				name  string
				args  map[string]any
				allow bool
			}{{"get_content", map[string]any{"id": private.ID}, false}, {"list_content", map[string]any{"status": "draft", "author_id": editor.ID}, false}, {"get_content", map[string]any{"id": published.ID}, true}, {"get_content", map[string]any{"id": own.ID}, true}} {
				result, err := client.CallTool(ctx, &sdk.CallToolParams{Name: call.name, Arguments: call.args})
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), "KEY MUST NOT READ") {
					t.Fatalf("MCP key leaked %s", call.name)
				}
				if call.allow && (result.IsError || (!strings.Contains(string(raw), "KEY PUBLIC BODY") && !strings.Contains(string(raw), "KEY OWN DRAFT"))) {
					t.Fatalf("MCP key cannot read published content: %s", raw)
				}
			}
		})
	}
}
