package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The image bakes GORUN_ENV=prod and GORUN_ADDR=:8080; trial mode must boot
// from those alone, and keep its generated key across restarts.
func TestLoadTrial(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_DATA", dir)
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_ADDR", ":8080")
	t.Setenv("GORUN_TRIAL", "1")
	t.Setenv("GORUN_TRUSTED_PROXIES", "10.0.0.0/8")
	c, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Trial || c.Env != "dev" || c.BaseURL != "http://localhost:8080" || c.Addr != ":8080" {
		t.Fatalf("trial config: %+v", c)
	}
	if len(c.TrustedProxies) != 0 {
		t.Fatalf("trial must not trust forwarded headers: %v", c.TrustedProxies)
	}
	again, err := Load(nil)
	if err != nil || string(again.SecretKey) != string(c.SecretKey) {
		t.Fatalf("trial key must persist: %v", err)
	}
}

func TestLoadTrialDefaultsToAllInterfaces(t *testing.T) {
	t.Setenv("GORUN_DATA", t.TempDir())
	t.Setenv("GORUN_TRIAL", "true")
	t.Setenv("GORUN_ADDR", "")
	c, err := Load(nil)
	if err != nil || c.Addr != ":8080" {
		t.Fatalf("addr %q err %v", c.Addr, err)
	}
}

// A site whose key lives elsewhere must not get a fresh key silently: its
// stored secrets would become unreadable.
func TestLoadTrialRefusesExistingSiteWithoutKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, dbFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GORUN_DATA", dir)
	t.Setenv("GORUN_TRIAL", "1")
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "GORUN_SECRET_KEY") {
		t.Fatalf("want a refusal naming GORUN_SECRET_KEY, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, secretFile)); !os.IsNotExist(err) {
		t.Fatalf("no key may be written: %v", err)
	}
	t.Setenv("GORUN_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, secretKeyLen)))
	if _, err := Load(nil); err != nil {
		t.Fatalf("an explicit key is accepted: %v", err)
	}
}

func TestLoadTrialInvalidValue(t *testing.T) {
	t.Setenv("GORUN_DATA", t.TempDir())
	t.Setenv("GORUN_TRIAL", "maybe")
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "GORUN_TRIAL") {
		t.Fatalf("got %v", err)
	}
}

// Off means the production rules still apply.
func TestLoadTrialOffKeepsProdRules(t *testing.T) {
	t.Setenv("GORUN_DATA", t.TempDir())
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_TRIAL", "0")
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "GORUN_BASE_URL") {
		t.Fatalf("got %v", err)
	}
}

// A first run with nothing set points at the trial instead of a dead end.
func TestProdErrorsMentionTrial(t *testing.T) {
	t.Setenv("GORUN_DATA", t.TempDir())
	t.Setenv("GORUN_ENV", "prod")
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "GORUN_TRIAL=1") {
		t.Fatalf("missing base URL: %v", err)
	}
	t.Setenv("GORUN_BASE_URL", "https://example.com")
	if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), "GORUN_TRIAL=1") {
		t.Fatalf("missing key: %v", err)
	}
}
