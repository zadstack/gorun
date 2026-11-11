package site_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

var perRequest = regexp.MustCompile(`(nonce|value|content)="[A-Za-z0-9_\-+/=&#;]{16,}"`)

type previewLinks interface {
	CreatePreviewLink(ctx context.Context, id, ttl string) (*content.PreviewLink, string, error)
	RevokePreviewLink(ctx context.Context, id, linkID string) error
}

func anonGet(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path) // no cookie jar: a visitor without an account
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestPreviewLinkRendersDraftForAnonymousVisitor(t *testing.T) {
	e := bootReal(t)
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	draft, err := cs.Create(ctx, content.Input{Type: "post", Slug: "secret-draft", Title: "Unreleased launch", Body: "<p>Embargoed text</p>", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	links, ok := cs.(previewLinks)
	if !ok {
		t.Fatal("content service lacks preview links")
	}
	link, token, err := links.CreatePreviewLink(ctx, draft.ID, "24h")
	if err != nil {
		t.Fatal(err)
	}

	// The draft is not public at its own URL.
	if resp, body := anonGet(t, e.srv, "/blog/secret-draft"); resp.StatusCode != http.StatusNotFound || strings.Contains(body, "Embargoed text") {
		t.Fatalf("draft public: %d", resp.StatusCode)
	}

	resp, body := anonGet(t, e.srv, content.PreviewPathPrefix+token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Unreleased launch") || !strings.Contains(body, "Embargoed text") || !strings.Contains(body, "data-gorun-preview") {
		t.Fatal("preview misses the item or the ribbon")
	}
	if i, j := strings.Index(body, "<body"), strings.Index(body, "data-gorun-preview"); i < 0 || j < i {
		t.Fatal("ribbon outside <body>")
	}
	for k, want := range map[string]string{"X-Robots-Tag": "noindex, nofollow", "Cache-Control": "private, no-store", "Referrer-Policy": "no-referrer"} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	// Unknown, revoked and malformed tokens: the same 404, no oracle.
	unknown := strings.Repeat("A", 43)
	if err := links.RevokePreviewLink(ctx, draft.ID, link.ID); err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, p := range []string{content.PreviewPathPrefix + unknown, content.PreviewPathPrefix + token} {
		resp, body := anonGet(t, e.srv, p)
		if resp.StatusCode != http.StatusNotFound || strings.Contains(body, "Embargoed text") {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
		if resp.Header.Get("X-Robots-Tag") != "noindex, nofollow" {
			t.Errorf("%s: 404 without noindex", p)
		}
		// Per-request nonces and CSRF tokens aside, the pages are identical.
		bodies = append(bodies, perRequest.ReplaceAllString(body, `$1="*"`))
	}
	if bodies[0] != bodies[1] {
		t.Error("unknown and revoked tokens answer differently")
	}
	if resp, _ := anonGet(t, e.srv, content.PreviewPathPrefix+"not-a-token"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("malformed token %d", resp.StatusCode)
	}
}
