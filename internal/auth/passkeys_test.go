package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRPFromBaseURL(t *testing.T) {
	for _, tc := range []struct {
		base, rpID, origin string
	}{
		{"https://example.com", "example.com", "https://example.com"},
		{"https://Admin.Example.com:8443/sub/", "admin.example.com", "https://admin.example.com:8443"},
		{"http://localhost:8080", "localhost", "http://localhost:8080"},
		{"http://gorun.localhost", "gorun.localhost", "http://gorun.localhost"},
		{"https://example.com:443", "example.com", "https://example.com"}, // default port is not part of an origin
		{"http://localhost:80", "localhost", "http://localhost"},
		{"https://bücher.example", "xn--bcher-kva.example", "https://xn--bcher-kva.example"}, // browsers send punycode
		{"https://example.com:0443", "example.com", "https://example.com"},                   // ports are serialized canonically
		{"https://r3--sn.example.com", "r3--sn.example.com", "https://r3--sn.example.com"},   // valid ASCII host with "--"
		{"https://example.com:99999", "", ""},
		{"http://example.com", "", ""},          // plain http on a real host: no secure context
		{"https://127.0.0.1:8443", "", ""},      // an IP address is not a valid RP ID
		{"http://127.0.0.1:8080", "", ""},       // the e2e default
		{"https://[::1]", "", ""},               // IPv6 literal
		{"", "", ""},                            // unset
		{"ftp://example.com", "", ""},           // not http(s)
		{"https://user:pw@example.com", "", ""}, // credentials in the URL
	} {
		rpID, origin, err := rpFromBaseURL(tc.base)
		if tc.rpID == "" {
			if err == nil {
				t.Errorf("%q: want unavailable, got %q %q", tc.base, rpID, origin)
			}
			continue
		}
		if err != nil || rpID != tc.rpID || origin != tc.origin {
			t.Errorf("%q: got %q %q %v, want %q %q", tc.base, rpID, origin, err, tc.rpID, tc.origin)
		}
	}
}

func TestCleanPasskeyName(t *testing.T) {
	if n, ok := cleanPasskeyName("  Work laptop "); !ok || n != "Work laptop" {
		t.Fatalf("trim: %q %v", n, ok)
	}
	if n, ok := cleanPasskeyName(""); !ok || n != "" {
		t.Fatal("empty name should mean the default")
	}
	for _, bad := range []string{strings.Repeat("ك", 65), "line\nbreak", "tab\there", "bell\a", "zero\u200bwidth", "bidi\u202eflip", "bom\ufeff", "sep x", "\xff"} {
		if _, ok := cleanPasskeyName(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, ok := cleanPasskeyName(strings.Repeat("ك", 64)); !ok {
		t.Error("64 Arabic letters refused")
	}
}

// Turning off the app for a role that requires two-factor is refused inside
// the DELETE unless a passkey for the current RP ID exists, so a parallel
// passkey removal cannot leave the user without a factor.
func TestRemoveTOTPGuardedForRequiredRoles(t *testing.T) {
	d := newDB(t)
	u := mustUser(t, testService(d), "guard@example.com", "editor")
	s := newTwoFactorStore(testDeps(d))
	ctx := context.Background()
	d.Exec(`INSERT INTO user_totp (user_id, secret_enc, enabled_at, created_at, updated_at) VALUES (?, 'x', 'x', 'x', 'x')`, u.ID)
	d.Exec(`INSERT INTO user_passkeys (id, user_id, rp_id, credential_id, public_key, name, created_at) VALUES ('old', ?, 'old.example', x'01', x'02', 'Old', 'x')`, u.ID)
	if _, err := s.remove(ctx, u.ID, "example.com", true); !errors.Is(err, errLastRequiredFactor) {
		t.Fatalf("only a passkey of another RP ID left: %v", err)
	}
	if _, err := s.remove(ctx, u.ID, "", true); !errors.Is(err, errLastRequiredFactor) {
		t.Fatalf("passkeys unavailable: %v", err)
	}
	d.Exec(`INSERT INTO user_passkeys (id, user_id, rp_id, credential_id, public_key, name, created_at) VALUES ('cur', ?, 'example.com', x'03', x'04', 'Cur', 'x')`, u.ID)
	if removed, err := s.remove(ctx, u.ID, "example.com", true); err != nil || !removed {
		t.Fatalf("with a usable passkey: %v %v", removed, err)
	}
}

func TestChallengeHashIsNotTheChallenge(t *testing.T) {
	h := challengeHash("abc")
	if len(h) != 64 || strings.Contains(h, "abc") || h == challengeHash("abd") {
		t.Fatalf("hash %q", h)
	}
}
