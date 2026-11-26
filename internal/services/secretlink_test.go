package services

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

// secretFake is fakeService with a secret, stored under fake.secret; the
// secret "bad" is a field error.
type secretFake struct {
	fakeService
	name string
}

func (s secretFake) Name() string {
	if s.name != "" {
		return s.name
	}
	return "fake"
}
func (secretFake) Secret(string) string { return "token" }
func (s secretFake) Apply(ctx context.Context, tx *sql.Tx, _ map[string]string, secret *string) (map[string]string, error) {
	if secret == nil {
		return nil, nil
	}
	if err := s.d.Settings.SetTx(ctx, tx, s.Name()+".secret", *secret); err != nil {
		return nil, err
	}
	if *secret == "bad" {
		return map[string]string{"token": "services.invalid"}, nil
	}
	return nil, nil
}

// linkToken is the t= value of a secret link.
func linkToken(t *testing.T, link, name string) string {
	t.Helper()
	prefix := "/admin/services/" + name + "/secret?t="
	if !strings.HasPrefix(link, prefix) {
		t.Fatalf("link %q, want prefix %q", link, prefix)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	tok := u.Query().Get("t")
	if len(tok) < 43 {
		t.Fatalf("token %q is shorter than 32 bytes in base64url", tok)
	}
	return tok
}

func TestSecretLinkWorksOnce(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, secretFake{fakeService: fakeService{d}})
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	r.setNow(func() time.Time { return now })

	link, exp, err := r.SecretLink(ctx, "fake", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("expires %v, want %v", exp, now.Add(15*time.Minute))
	}
	tok := linkToken(t, link, "fake")
	all, err := d.Settings.All(ctx, "services.secret_link.")
	if err != nil || len(all) != 1 {
		t.Fatalf("stored links %v, %v", all, err)
	}
	for k, v := range all {
		if strings.Contains(k, tok) || strings.Contains(string(v), tok) {
			t.Fatal("the token itself is stored; only its hash may be")
		}
	}

	if _, fields, err := r.RedeemSecret(ctx, tok, "fake", "bad", "admin"); err != nil || fields["token"] == "" {
		t.Fatalf("bad secret: fields %v, err %v", fields, err)
	}
	if _, _, err := r.RedeemSecret(ctx, tok, "fake", "s3cret", "admin"); err != nil {
		t.Fatalf("a refused secret used the link up: %v", err)
	}
	if v := d.Settings.GetString(ctx, "fake.secret", ""); v != "s3cret" {
		t.Fatalf("fake.secret = %q", v)
	}
	if _, _, err := r.RedeemSecret(ctx, tok, "fake", "again", "admin"); !errors.Is(err, ErrLinkGone) {
		t.Fatalf("second use: %v, want ErrLinkGone", err)
	}
	if v := d.Settings.GetString(ctx, "fake.secret", ""); v != "s3cret" {
		t.Fatalf("second use saved %q", v)
	}
	if all, _ := d.Settings.All(ctx, "services.secret_link."); len(all) != 0 {
		t.Fatalf("used link still stored: %v", all)
	}
	if _, _, err := r.RedeemSecret(ctx, "not-a-token", "fake", "x", "admin"); !errors.Is(err, ErrLinkGone) {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestSecretLinkExpiresAfter15Minutes(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, secretFake{fakeService: fakeService{d}})
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	r.setNow(func() time.Time { return now })

	early, _, err := r.SecretLink(ctx, "fake", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	late, _, err := r.SecretLink(ctx, "fake", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(15*time.Minute - time.Second)
	if _, _, err := r.RedeemSecret(ctx, linkToken(t, early, "fake"), "fake", "in-time", "admin"); err != nil {
		t.Fatalf("at 14:59: %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, _, err := r.RedeemSecret(ctx, linkToken(t, late, "fake"), "fake", "too-late", "admin"); !errors.Is(err, ErrLinkGone) {
		t.Fatalf("at 15:01: %v, want ErrLinkGone", err)
	}
	if v := d.Settings.GetString(ctx, "fake.secret", ""); v != "in-time" {
		t.Fatalf("fake.secret = %q", v)
	}
	if all, _ := d.Settings.All(ctx, "services.secret_link."); len(all) != 0 {
		t.Fatalf("expired link not deleted when seen: %v", all)
	}
}

func TestSecretLinkIsForOneService(t *testing.T) {
	d := testDeps(t)
	ctx := context.Background()
	r := NewRegistry(d, secretFake{fakeService: fakeService{d}}, secretFake{fakeService: fakeService{d}, name: "other"})

	link, _, err := r.SecretLink(ctx, "fake", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	tok := linkToken(t, link, "fake")
	if _, _, err := r.RedeemSecret(ctx, tok, "other", "s3cret", "admin"); !errors.Is(err, ErrLinkGone) {
		t.Fatalf("other service: %v, want ErrLinkGone", err)
	}
	if v := d.Settings.GetString(ctx, "other.secret", ""); v != "" {
		t.Fatalf("other.secret saved: %q", v)
	}
	if _, _, err := r.RedeemSecret(ctx, tok, "fake", "s3cret", "admin"); err != nil {
		t.Fatalf("own service after a wrong one: %v", err)
	}

	if _, _, err := r.SecretLink(ctx, "nope", "user-1"); !errors.Is(err, ErrUnknownService) {
		t.Fatalf("unknown service: %v", err)
	}
	if _, _, err := r.SecretLink(ctx, "fake2", "user-1"); !errors.Is(err, ErrUnknownService) {
		t.Fatalf("unknown service: %v", err)
	}
	r2 := NewRegistry(d, fakeService{d})
	if _, _, err := r2.SecretLink(ctx, "fake", "user-1"); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("service without a secret: %v, want ErrNoSecret", err)
	}
}
