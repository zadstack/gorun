package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestUserPasswordResetsAndValidates(t *testing.T) {
	dir := t.TempDir()
	if err := run([]string{"user", "create", "--email", "a@example.test", "--password", "first-password", "--admin", "--data", dir}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := userPassword([]string{"--email", "a@example.test", "--password", "second-password", "--data", dir}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "password updated for a@example.test") {
		t.Fatalf("output: %q", out.String())
	}
	out.Reset()
	if err := userPassword([]string{"--email", "a@example.test", "--password-stdin", "--data", dir}, strings.NewReader("third-password\n"), &out); err != nil {
		t.Fatal(err)
	}

	users, closeDB, err := openUsers(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if _, err := users.CheckPassword(context.Background(), "a@example.test", "third-password"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	if _, err := users.CheckPassword(context.Background(), "a@example.test", "first-password"); err == nil {
		t.Fatal("old password still accepted")
	}

	for name, args := range map[string][]string{
		"short":   {"--email", "a@example.test", "--password", "short", "--data", dir},
		"unknown": {"--email", "nobody@example.test", "--password", "long-enough-pw", "--data", dir},
		"missing": {"--data", dir},
	} {
		if err := userPassword(args, strings.NewReader(""), &out); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
