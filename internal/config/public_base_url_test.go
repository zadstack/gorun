package config

import (
	"errors"
	"strings"
	"testing"
)

// A production container that cannot start on a missing public base URL must
// name the deployment fix instead of nudging the operator to a trial site.
func TestProdWithoutPublicBaseURLNamesTheDeploymentFix(t *testing.T) {
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_BASE_URL", "")
	t.Setenv("GORUN_TRIAL", "")
	_, err := LoadReadOnly([]string{"--data", t.TempDir()})
	if err == nil {
		t.Fatal("prod without a public base URL must fail")
	}
	if !errors.Is(err, ErrPublicBaseURL) {
		t.Fatalf("error does not classify as a public base URL problem: %v", err)
	}
	message := err.Error()
	for _, want := range []string{"GORUN_BASE_URL", "public https URL", "service domain", "not a production setup"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message does not explain %q: %s", want, message)
		}
	}
	if strings.Contains(message, "To try GoRun first with no configuration") {
		t.Fatalf("message still suggests the trial path as the fix: %s", message)
	}
}

// A trial container is disposable on purpose and keeps working without a base URL.
func TestTrialContainerNeedsNoPublicBaseURL(t *testing.T) {
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_BASE_URL", "")
	t.Setenv("GORUN_TRIAL", "1")
	cfg, err := Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatalf("trial container must start: %v", err)
	}
	if !cfg.Trial || cfg.Env != "dev" {
		t.Fatalf("trial mode not applied: %+v", cfg)
	}
}

// A rejected base URL stays classified so operators can act on it.
func TestProdWithUnusablePublicBaseURLStaysClassified(t *testing.T) {
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_TRIAL", "")
	for _, raw := range []string{"http://localhost:8080", "not-a-url"} {
		t.Setenv("GORUN_BASE_URL", raw)
		_, err := LoadReadOnly([]string{"--data", t.TempDir()})
		if err == nil {
			t.Fatalf("base URL %q accepted in prod", raw)
		}
		if !errors.Is(err, ErrPublicBaseURL) {
			t.Fatalf("base URL %q error is not classified: %v", raw, err)
		}
	}
}
