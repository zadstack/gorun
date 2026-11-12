package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// After GORUN_SECRET_KEY is rotated the stored TOTP secret no longer
// decrypts. An app code must then fail like a wrong code (not a server
// error), the operator is told once per user, and recovery codes still work.
func TestTwoFactorUnreadableSecretAfterKeyRotation(t *testing.T) {
	s, svc, c := newTFStore(t)
	var logs bytes.Buffer
	s.log = slog.New(slog.NewTextHandler(&logs, nil))
	ctx := context.Background()
	u := mustUser(t, svc, "rotated@example.com", "editor")
	secret, err := s.begin(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := s.confirm(ctx, u.ID, totpCode(secret, c.now()))
	if err != nil {
		t.Fatal(err)
	}

	rotated := bytes.Repeat([]byte{7}, 32)
	s.key = rotated
	c.add(30 * time.Second)
	for range 3 {
		if _, err := s.verify(ctx, u.ID, totpCode(secret, c.now())); !errors.Is(err, errTwoFactorCode) {
			t.Fatalf("app code with an unreadable secret: %v, want errTwoFactorCode", err)
		}
	}
	out := logs.String()
	if n := strings.Count(out, "two-factor secret cannot be decrypted"); n != 1 {
		t.Fatalf("operator error logged %d times, want once:\n%s", n, out)
	}
	if !strings.Contains(out, u.ID) || !strings.Contains(out, "GORUN_SECRET_KEY") {
		t.Fatalf("operator error lacks the user or the cause:\n%s", out)
	}
	var enc string
	s.db.QueryRow(`SELECT secret_enc FROM user_totp WHERE user_id = ?`, u.ID).Scan(&enc)
	if strings.Contains(out, enc) || strings.Contains(out, totpEncoding.EncodeToString(secret)) {
		t.Fatal("secret material reached the log")
	}

	// Recovery codes do not depend on the key.
	if m, err := s.verify(ctx, u.ID, codes[0]); err != nil || m != "recovery" {
		t.Fatalf("recovery code after key rotation: %s %v", m, err)
	}
}
