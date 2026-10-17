package auth

import (
	"strings"
	"testing"
)

func TestArgon2HashVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected encoding %q", h)
	}
	if !VerifyPassword(h, "correct horse battery") || VerifyPassword(h, "wrong") {
		t.Fatal("verify mismatch")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("salt must differ per hash")
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=x$a$b", "$argon2i$v=19$m=1,t=1,p=1$AAAA$AAAA"} {
		if VerifyPassword(bad, "x") {
			t.Fatalf("malformed hash %q verified", bad)
		}
	}
}
