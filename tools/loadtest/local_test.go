package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalEnvProdIncludesSecretAndNonLoopbackBase(t *testing.T) {
	env := localEnv("/data", "/themes", prodBaseURLHost, "127.0.0.1:9", "prod", "dGVzdA==")
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"GORUN_ENV=prod",
		"GORUN_BASE_URL=" + prodBaseURLHost,
		"GORUN_SECRET_KEY=dGVzdA==",
		"GORUN_ADDR=127.0.0.1:9",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "GORUN_ENV=dev") {
		t.Fatal("prod env leaked a second GORUN_ENV=dev")
	}
}

func TestLocalEnvDevOmitsSecret(t *testing.T) {
	env := localEnv("/data", "/themes", "http://127.0.0.1:9", "127.0.0.1:9", "dev", "")
	for _, e := range env {
		if strings.HasPrefix(e, "GORUN_SECRET_KEY=") {
			t.Fatalf("dev CLI env must not set secret: %s", e)
		}
		if e == "GORUN_ENV=prod" {
			t.Fatal("dev env set to prod")
		}
	}
}

func TestReadSecretKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	if err := os.WriteFile(path, []byte("abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readSecretKey(dir)
	if err != nil || got != "abc123" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := readSecretKey(t.TempDir()); err == nil {
		t.Fatal("missing secret.key must error")
	}
}
