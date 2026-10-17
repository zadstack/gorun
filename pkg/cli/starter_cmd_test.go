package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStarterContentAndLoginLink(t *testing.T) {
	dir := t.TempDir()
	themes, _ := filepath.Abs("../../themes")
	t.Setenv("GORUN_ENV", "dev")
	t.Setenv("GORUN_THEMES", themes)
	data := []string{"--data", dir}

	var out bytes.Buffer
	if err := starterCmd(data, &out); err != nil {
		t.Fatalf("starter: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := infoCmd(data, &out); err != nil {
		t.Fatal(err)
	}
	var info siteInfo
	json.Unmarshal(out.Bytes(), &info)
	if info.Content["post"]["published"] != 5 || info.Content["page"]["published"] != 4 {
		t.Fatalf("starter content %v", info.Content)
	}
	// A site with content is left alone.
	out.Reset()
	if err := starterCmd(data, &out); err != nil || !strings.Contains(out.String(), "nothing imported") {
		t.Fatalf("second run: %v %s", err, out.String())
	}

	if err := userCreate(append([]string{"--email", "a@example.test", "--password", "a-long-test-password", "--admin"}, data...)); err != nil {
		t.Fatal(err)
	}
	if err := settingsCmd([]string{"set", "site.base_url", "https://shop.example.test", "--data", dir}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := userLoginLink(append([]string{"--email", "a@example.test"}, data...), &out); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^https://shop\.example\.test/auth/magic/[A-Za-z0-9_-]{20,}\n$`).MatchString(out.String()) {
		t.Fatalf("link %q", out.String())
	}
	if err := userLoginLink(append([]string{"--email", "a@example.test", "--ttl", "1h"}, data...), &out); err == nil {
		t.Fatal("a long-lived link was made")
	}
	if err := userLoginLink(append([]string{"--email", "nobody@example.test"}, data...), &out); err == nil {
		t.Fatal("link for an unknown user")
	}
}
