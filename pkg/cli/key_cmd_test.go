package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestKeyHelpArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--help"},
		{"-h"},
		{"help"},
		{"create", "--help"},
		{"create", "--name", "x", "--help"},
	} {
		if !keyHelpArgs(args) {
			t.Fatalf("keyHelpArgs(%v) = false", args)
		}
	}
	if keyHelpArgs([]string{"create", "--name", "x"}) {
		t.Fatal("keyHelpArgs must ignore normal flags")
	}
}

func TestFormatKeyValidationUnknownScope(t *testing.T) {
	got := formatKeyValidation(map[string]string{"scopes": "auth.error.scope_invalid"}, []string{"content.write"})
	if !strings.Contains(got, "content.write") || !strings.Contains(got, "content.edit") || strings.Contains(got, "map[") {
		t.Fatalf("formatKeyValidation: %q", got)
	}
}

func TestKeyCreateHelpNoBoot(t *testing.T) {
	kernel.LogOutput = io.Discard
	var out bytes.Buffer
	err := keyCmd([]string{"create", "--help"}, &out)
	if err == nil || !strings.Contains(err.Error(), "usage: gorun key") {
		t.Fatalf("create --help: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("help must not print JSON: %q", out.String())
	}
}

func TestKeyCreateUnknownScopeMessage(t *testing.T) {
	kernel.LogOutput = io.Discard
	dir := t.TempDir()
	ctx := context.Background()
	cfg, err := config.Load([]string{"--data", dir})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(ctx, cfg, Modules(cfg), kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := users.Create(ctx, "key-soak@example.com", "Owner", "correct-horse-battery-staple", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	app.Close()

	var out bytes.Buffer
	err = keyCmd([]string{
		"create", "--name", "bad", "--user", "key-soak@example.com",
		"--scopes", "content.write", "--kind", "api", "--data", dir,
	}, &out)
	if err == nil || !strings.Contains(err.Error(), "unknown permission scope") || !strings.Contains(err.Error(), "content.write") {
		t.Fatalf("unknown scope: %v", err)
	}
	if strings.Contains(err.Error(), "map[") || strings.Contains(err.Error(), "auth.error.scope_invalid") {
		t.Fatalf("must not dump i18n map: %v", err)
	}
}
