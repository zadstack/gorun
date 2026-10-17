package config

import "testing"

func TestDevLoopbackListenerProvidesUsableBaseURL(t *testing.T) {
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_BASE_URL", "")
	for _, listener := range []string{"127.0.0.1:9123", "localhost:9123", "[::1]:9123"} {
		cfg, err := Load([]string{"--addr", listener, "--data", t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BaseURL != "http://"+listener {
			t.Fatalf("listener %s has unusable base URL %q", listener, cfg.BaseURL)
		}
	}
}

func TestExplicitBaseURLWinsOverDevLoopbackListener(t *testing.T) {
	t.Setenv("GORUN_ENV", "dev")
	const base = "https://site.example"
	cfg, err := Load([]string{"--addr", "127.0.0.1:9123", "--base-url", base, "--data", t.TempDir()})
	if err != nil || cfg.BaseURL != base {
		t.Fatalf("explicit base changed: %q, %v", cfg.BaseURL, err)
	}
}
