package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestAICLI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_ENV", "dev")
	run := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		err := aiCmd(append(args, "--data", dir), strings.NewReader(stdin), &out)
		return strings.TrimSpace(out.String()), err
	}
	get := func(key string) string {
		var out bytes.Buffer
		if err := settingsCmd([]string{"get", key, "--data", dir}, &out); err != nil {
			return ""
		}
		return strings.TrimSpace(out.String())
	}
	for _, bad := range [][]string{
		{"set", "--provider", "openai"},                                    // no model
		{"set", "--provider", "openai", "--model", "m"},                    // no destination
		{"set", "--provider", "anthropic", "--model", "claude-sonnet-5-5"}, // no key
		{"set", "--provider", "openai", "--model", "m", "--base-url", "http://evil.example/v1"},
		{"set", "--provider", "nope", "--model", "m"},
		{"frob"},
	} {
		if _, err := run("", bad...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if get("ai.provider") != "" {
		t.Fatal("a refused configuration was stored")
	}
	out, err := run("sk-test-123\n", "set", "--provider", "anthropic", "--model", "claude-sonnet-5-5", "--api-key-stdin")
	if err != nil || !strings.Contains(out, "anthropic claude-sonnet-5-5") {
		t.Fatalf("set: %q %v", out, err)
	}
	if get("ai.provider") != `"anthropic"` || get("ai.api_key") != `"(set, hidden)"` {
		t.Fatalf("stored: %s %s", get("ai.provider"), get("ai.api_key"))
	}
	// A local endpoint must be selected explicitly; the stored key is retained.
	if _, err := run("", "set", "--provider", "openai", "--model", "qwen3", "--base-url", "http://localhost:11434/v1"); err != nil {
		t.Fatal(err)
	}
	if get("ai.base_url") != `"http://localhost:11434/v1"` || get("ai.api_key") != `"(set, hidden)"` {
		t.Fatalf("openai: %s key %s", get("ai.base_url"), get("ai.api_key"))
	}
	if err := settingsCmd([]string{"set", "ai.provider", "openai", "--data", dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("settings set ai.* bypasses the validation")
	}
	if out, err := run("", "off"); err != nil || get("ai.provider") != `""` {
		t.Fatalf("off: %q %v %s", out, err, get("ai.provider"))
	}
}
