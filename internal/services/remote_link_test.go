package services

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/media"
)

// linkPath is the path and query of an absolute secret link.
func linkPath(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.RequestURI()
}

// storedLinks is every stored secret link, raw.
func storedLinks(t *testing.T, r *Registry) map[string]string {
	t.Helper()
	all, err := r.d.Settings.All(context.Background(), linkPrefix)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range all {
		out[k] = string(v)
	}
	return out
}

// On a site with no storage destination, a remote set of the location
// alone saves nothing and answers needs_secret; the link holds the pending
// values (never the token), and redeeming it with the keys binds them all.
func TestStorageViaRemoteIssuesLinkAndRedeemApplies(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Storage(d))
	values := map[string]string{"provider": "r2", "account": testR2Account, "bucket": "site-media", "purpose": "media"}
	res, fields, err := r.SetRemote(ctx, "storage", values, "mcp")
	if err != nil || len(fields) > 0 {
		t.Fatalf("set remote: %v %v", fields, err)
	}
	if res.NeedsSecret == nil || res.Status.State != StateOff {
		t.Fatalf("result: %+v", res)
	}
	if id := d.Settings.GetString(ctx, "storage.active.media", ""); id != "" {
		t.Fatalf("bound before the keys came: %q", id)
	}
	tok := linkToken(t, linkPath(t, res.NeedsSecret.Link), "storage")
	links := storedLinks(t, r)
	if len(links) != 1 {
		t.Fatalf("links = %v", links)
	}
	for k, v := range links {
		if strings.Contains(k, tok) || strings.Contains(v, tok) || !strings.Contains(v, "site-media") || !strings.Contains(v, `"provider":"r2"`) {
			t.Fatalf("stored link %s = %s", k, v)
		}
	}

	st, fields, err := r.RedeemSecret(ctx, tok, "storage", testAccessKey+":"+testSecretKey, "admin")
	if err != nil || len(fields) > 0 {
		t.Fatalf("redeem: %v %v", fields, err)
	}
	if st.State != StateConfigured || st.Provider != "r2" || st.Fields["bucket"] != "site-media" || !st.SecretSet {
		t.Fatalf("status after redeem: %+v", st)
	}
	id := d.Settings.GetString(ctx, "storage.active.media", "")
	dest, err := media.LoadDestination(ctx, d.Settings, id)
	if err != nil {
		t.Fatalf("destination %q: %v", id, err)
	}
	cfg, err := media.DestinationConfig(dest, d.Config.SecretKey)
	if err != nil || cfg.Bucket != "site-media" || cfg.AccessKey != testAccessKey || cfg.SecretKey != testSecretKey {
		t.Fatalf("destination: %+v %v", cfg, err)
	}
	if len(storedLinks(t, r)) != 0 {
		t.Fatal("used link still stored")
	}

	// A link is only issued for values that would be accepted with a secret.
	_, fields, err = r.SetRemote(ctx, "storage", map[string]string{"provider": "r2", "account": testR2Account, "bucket": "Not A Bucket!", "purpose": "media"}, "mcp")
	if err != nil || fields["bucket"] == "" {
		t.Fatalf("bad bucket: %v %v", fields, err)
	}
	if len(storedLinks(t, r)) != 0 {
		t.Fatal("a link was made for values the bind refuses")
	}
}

// Mail on resend without its key: a remote set answers needs_secret and
// the link stores the key.
func TestMailResendViaRemoteNeedsSecretThenRedeems(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Mail(d))
	res, fields, err := r.SetRemote(ctx, "mail", map[string]string{"provider": "resend", "from": "site@example.com"}, "api")
	if err != nil || len(fields) > 0 {
		t.Fatalf("set remote: %v %v", fields, err)
	}
	if res.NeedsSecret == nil {
		t.Fatalf("no needs_secret: %+v", res)
	}
	tok := linkToken(t, linkPath(t, res.NeedsSecret.Link), "mail")
	st, fields, err := r.RedeemSecret(ctx, tok, "mail", "re_test_key_0001", "admin")
	if err != nil || len(fields) > 0 {
		t.Fatalf("redeem: %v %v", fields, err)
	}
	if st.Provider != "resend" || !st.SecretSet || st.Fields["from"] != "site@example.com" {
		t.Fatalf("status after redeem: %+v", st)
	}
	if d.Settings.GetString(ctx, "mail.password", "") != "" {
		t.Fatal("the key landed in the smtp password")
	}
}

// A link is bound to the provider it was made for: after the provider
// changes, redeeming it is ErrLinkGone and no secret is saved.
func TestLinkRefusedAfterProviderChange(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, Mail(d))
	res, fields, err := r.SetRemote(ctx, "mail", map[string]string{"provider": "resend", "from": "site@example.com"}, "api")
	if err != nil || len(fields) > 0 || res.NeedsSecret == nil {
		t.Fatalf("set remote: %+v %v %v", res, fields, err)
	}
	tok := linkToken(t, linkPath(t, res.NeedsSecret.Link), "mail")
	if _, fields, err := r.Set(ctx, "mail", map[string]string{"provider": "smtp", "host": "smtp.example.com"}, nil, "cli"); err != nil || len(fields) > 0 {
		t.Fatalf("switch to smtp: %v %v", fields, err)
	}
	if _, _, err := r.RedeemSecret(ctx, tok, "mail", "re_test_key_0001", "admin"); !errors.Is(err, ErrLinkGone) {
		t.Fatalf("redeem after provider change: %v, want ErrLinkGone", err)
	}
	if d.Settings.GetString(ctx, "mail.password", "") != "" || d.Settings.GetString(ctx, "mail.resend_api_key", "") != "" {
		t.Fatal("a secret was saved through a link made for another provider")
	}
}

// Describe never prints the value of a key that is not a declared
// non-secret field.
func TestDescribeHidesUndeclaredKeys(t *testing.T) {
	d := testDeps(t)
	r := NewRegistry(d, Mail(d), Storage(d))
	for name, key := range map[string]string{"mail": "resend_api_key", "storage": "secret_key"} {
		out, err := r.Describe(context.Background(), name, map[string]string{"from": "a@example.com", key: "hunter2-undeclared"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "hunter2") || !strings.Contains(out, key) {
			t.Fatalf("%s describe:\n%s", name, out)
		}
	}
}
