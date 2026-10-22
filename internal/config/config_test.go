package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain points the data dir at a temp dir so dev-mode secret generation
// never writes into the source tree.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gorun-config-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("GORUN_DATA", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestLoadPrecedence(t *testing.T) {
	t.Setenv("GORUN_ADDR", ":9000")
	c, _ := Load([]string{"--addr", ":9001"})
	if c.Addr != ":9001" {
		t.Fatal(c.Addr)
	}
}

func TestLoadEnvOverTOMLOverDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_DATA", dir)
	toml := "addr = \":7000\"\ntheme = \"example\"\n[stripe]\nsecret_key = \"sk_test_x\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gorun.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":7000" || c.Theme != "example" || c.Stripe.SecretKey != "sk_test_x" {
		t.Fatalf("toml not applied: %+v", c)
	}
	t.Setenv("GORUN_ADDR", ":7001")
	c, _ = Load(nil)
	if c.Addr != ":7001" {
		t.Fatalf("env should beat toml: %s", c.Addr)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("GORUN_DATA", t.TempDir())
	c, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != "dev" || c.Addr != "127.0.0.1:8080" || c.Theme != "default" {
		t.Fatalf("%+v", c)
	}
}

func TestLoadRejectsUnknownTOMLKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_DATA", dir)
	os.WriteFile(filepath.Join(dir, "gorun.toml"), []byte("adr = \":1\"\n"), 0o600)
	if _, err := Load(nil); err == nil {
		t.Fatal("want error for unknown key")
	}
}

func TestDevSecretGeneratedAndPersisted(t *testing.T) {
	t.Setenv("GORUN_DATA", t.TempDir())
	a, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Load(nil)
	if len(a.SecretKey) != 32 || string(a.SecretKey) != string(b.SecretKey) {
		t.Fatalf("secret not persisted: %d bytes", len(a.SecretKey))
	}
}

func TestProdRequiresSecret(t *testing.T) {
	t.Setenv("GORUN_DATA", t.TempDir())
	t.Setenv("GORUN_ENV", "prod")
	if _, err := Load(nil); err == nil {
		t.Fatal("want error without secret in prod")
	}
	t.Setenv("GORUN_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16)))
	if _, err := Load(nil); err == nil {
		t.Fatal("want error for short secret")
	}
	t.Setenv("GORUN_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("GORUN_BASE_URL", "https://example.com")
	if _, err := Load(nil); err != nil {
		t.Fatal(err)
	}
}

// M1: prod fails fast without a public base URL (no localhost default).
func TestProdRequiresPublicBaseURL(t *testing.T) {
	t.Setenv("GORUN_DATA", t.TempDir())
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	for _, bad := range []string{"", "http://localhost:8080", "https://app.localhost", "http://127.0.0.1", "http://[::1]:8080", "http://0.0.0.0", "example.com", "ftp://example.com"} {
		t.Setenv("GORUN_BASE_URL", bad)
		if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "GORUN_BASE_URL") {
			t.Errorf("%q: want a clear GORUN_BASE_URL error, got %v", bad, err)
		}
	}
	t.Setenv("GORUN_BASE_URL", "https://example.org/")
	c, err := Load(nil)
	if err != nil || c.BaseURL != "https://example.org" {
		t.Fatalf("public URL: %q %v", c.BaseURL, err)
	}
	// dev keeps the localhost default
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_BASE_URL", "")
	if c, err := Load(nil); err != nil || c.BaseURL != "http://localhost:8080" {
		t.Fatalf("dev default: %q %v", c.BaseURL, err)
	}
}

func TestSetupTokenSourcesAndMinLength(t *testing.T) {
	t.Setenv("GORUN_SETUP_TOKEN", "env-token-0123456789")
	c, err := Load(nil)
	if err != nil || c.SetupToken != "env-token-0123456789" {
		t.Fatalf("env: %q %v", c.SetupToken, err)
	}
	c, err = Load([]string{"--setup-token", "flag-token-0123456789"})
	if err != nil || c.SetupToken != "flag-token-0123456789" {
		t.Fatalf("flag: %q %v", c.SetupToken, err)
	}
	t.Setenv("GORUN_SETUP_TOKEN", "short")
	if _, err := Load(nil); err == nil {
		t.Fatal("a short setup token must be refused")
	}
}

func TestLoadS3FromTOMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_DATA", dir)
	toml := "[s3]\nendpoint = \"https://acc.r2.cloudflarestorage.com\"\nbucket = \"media\"\naccess_key = \"ak\"\nsecret_key = \"sk\"\npublic_url = \"https://cdn.example.com\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gorun.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.S3.Endpoint != "https://acc.r2.cloudflarestorage.com" || c.S3.Bucket != "media" || c.S3.AccessKey != "ak" ||
		c.S3.SecretKey != "sk" || c.S3.PublicURL != "https://cdn.example.com" || c.S3.Region != "" {
		t.Fatalf("toml: %+v", c.S3)
	}
	t.Setenv("GORUN_S3_SECRET_KEY", "from-env")
	t.Setenv("GORUN_S3_REGION", "eu-west-1")
	c, _ = Load(nil)
	if c.S3.SecretKey != "from-env" || c.S3.Region != "eu-west-1" {
		t.Fatalf("env: %+v", c.S3)
	}
}

func TestOperatorUpdateEnvironment(t *testing.T) {
	t.Setenv("GORUN_DATA", t.TempDir())
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_UPDATE_COOLIFY_URL", "https://coolify.example")
	t.Setenv("GORUN_UPDATE_COOLIFY_TOKEN", "synthetic-test-token")
	t.Setenv("GORUN_UPDATE_COOLIFY_RESOURCE", "site_123")
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateURL != "https://coolify.example" || cfg.UpdateToken != "synthetic-test-token" || cfg.UpdateResource != "site_123" {
		t.Fatal("update environment was not loaded")
	}
}

func TestStagedUpdateConfigurationFromEnvironmentAndFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_DATA", dir)
	t.Setenv("GORUN_ENV", "dev")
	if err := os.WriteFile(filepath.Join(dir, "gorun.toml"), []byte("[update]\nsource_url = \"https://releases.example/index.json\"\nartifact = \"linux-amd64\"\nhealth_links = [\"https://cms.example/\"]\nallow_downgrade = true\nactivation_timeout = \"20m\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Update.SourceURL != "https://releases.example/index.json" || cfg.Update.Artifact != "linux-amd64" || len(cfg.Update.HealthLinks) != 1 || !cfg.Update.AllowDowngrade || cfg.Update.ActivationTimeout != 20*time.Minute || cfg.Update.AllowMissingDigest {
		t.Fatalf("file config not loaded: %+v", cfg.Update)
	}
	t.Setenv("GORUN_UPDATE_SOURCE_URL", "https://other.example/index.json")
	t.Setenv("GORUN_UPDATE_SOURCE_TOKEN", "synthetic-token")
	t.Setenv("GORUN_UPDATE_HEALTH_LINKS", "https://a.example/, https://b.example/media/x.png")
	t.Setenv("GORUN_UPDATE_ALLOW_DOWNGRADE", "false")
	t.Setenv("GORUN_UPDATE_ALLOW_MISSING_DIGEST", "true")
	t.Setenv("GORUN_UPDATE_ACTIVATION_TIMEOUT", "5m")
	cfg, err = Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Update.SourceURL != "https://other.example/index.json" || cfg.Update.SourceToken != "synthetic-token" || len(cfg.Update.HealthLinks) != 2 || cfg.Update.AllowDowngrade || !cfg.Update.AllowMissingDigest || cfg.Update.ActivationTimeout != 5*time.Minute {
		t.Fatalf("environment did not override the file: %+v", cfg.Update)
	}
	t.Setenv("GORUN_UPDATE_ACTIVATION_TIMEOUT", "soon")
	if _, err := Load(nil); err == nil {
		t.Fatal("invalid activation timeout accepted")
	}
}
