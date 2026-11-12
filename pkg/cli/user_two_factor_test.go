package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The only administrator lost their authenticator and recovery codes: the
// operator removes the factor from the server's shell, and the reset is
// audited as done from the CLI.
func TestUserTwoFactorResetRecoversTheOnlyAdmin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	if err := userCreate([]string{"--email", "only@example.test", "--password", "a-long-test-password", "--admin", "--data", dir}); err != nil {
		t.Fatal(err)
	}
	d, err := openDB([]string{"--data", dir})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var userID string
	if err := d.QueryRow(`SELECT id FROM users WHERE email = 'only@example.test'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	const now = "2026-11-12T00:00:00Z"
	for _, q := range []string{
		`INSERT INTO user_totp (user_id, secret_enc, enabled_at, created_at, updated_at) VALUES (?, 'sealed', '` + now + `', '` + now + `', '` + now + `')`,
		`INSERT INTO user_recovery_codes (id, user_id, code_hash, created_at) VALUES ('rc1', ?, 'hash', '` + now + `')`,
		`INSERT INTO login_challenges (token_hash, user_id, method, next, expires_at, created_at) VALUES ('pending', ?, 'user.login', '/admin', '2099-01-01T00:00:00Z', '` + now + `')`,
	} {
		if _, err := d.Exec(q, userID); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := run([]string{"user", "two-factor-reset", "--email", "only@example.test", "--data", dir}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// Second run through the function itself: the factor is already gone.
	if err := userTwoFactorReset([]string{"--email", "only@example.test", "--data", dir}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "only@example.test had no two-factor authentication") {
		t.Fatalf("second reset output: %q", out.String())
	}
	for table, q := range map[string]string{
		"user_totp":           `SELECT count(*) FROM user_totp WHERE user_id = ?`,
		"user_recovery_codes": `SELECT count(*) FROM user_recovery_codes WHERE user_id = ?`,
		"login_challenges":    `SELECT count(*) FROM login_challenges WHERE user_id = ?`,
	} {
		var n int
		if err := d.QueryRow(q, userID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s left %d rows (%v)", table, n, err)
		}
	}
	var n int
	var meta string
	if err := d.QueryRow(`SELECT count(*), coalesce(max(meta_json), '') FROM audit_log WHERE action = 'user.2fa_reset' AND target_id = ? AND actor_kind = 'system'`, userID).Scan(&n, &meta); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !strings.Contains(meta, `"via":"cli"`) {
		t.Fatalf("audit: %d rows, meta %s; want one CLI reset", n, meta)
	}
}

func TestUserTwoFactorResetOutputAndErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	if err := userCreate([]string{"--email", "a@example.test", "--password", "a-long-test-password", "--admin", "--data", dir}); err != nil {
		t.Fatal(err)
	}
	d, err := openDB([]string{"--data", dir})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO user_totp (user_id, secret_enc, enabled_at, created_at, updated_at)
		SELECT id, 'sealed', 'x', 'x', 'x' FROM users WHERE email = 'a@example.test'`); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := userTwoFactorReset([]string{"--email", "a@example.test", "--data", dir}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "two-factor authentication removed for a@example.test") || !strings.Contains(got, "sessions were kept") {
		t.Fatalf("output: %q", got)
	}
	for name, args := range map[string][]string{
		"missing email": {"--data", dir},
		"unknown user":  {"--email", "nobody@example.test", "--data", dir},
	} {
		if err := userTwoFactorReset(args, &out); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
