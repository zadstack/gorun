package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// I10: deploy files stay safe — no localhost base URL default, the setup
// token and private-net switch are passed, e2e/ is not in the image, the
// documented backup needs no shell, and the e2e server env is explicit.
func TestDeployFilesSafety(t *testing.T) {
	compose := repoFile(t, "deploy/docker-compose.yml")
	for _, want := range []string{"GORUN_BASE_URL: ${GORUN_BASE_URL:?", "GORUN_SETUP_TOKEN:", "GORUN_ALLOW_PRIVATE_NET:"} {
		if !strings.Contains(compose, want) {
			t.Errorf("docker-compose.yml lacks %q", want)
		}
	}
	if strings.Contains(compose, "localhost") {
		t.Error("docker-compose.yml must not default the base URL to localhost")
	}
	if ignore := repoFile(t, ".dockerignore"); !strings.Contains("\n"+ignore+"\n", "\ne2e/\n") {
		t.Error(".dockerignore must exclude e2e/")
	}
	coolify := repoFile(t, "deploy/coolify.md")
	if !strings.Contains(coolify, "docker exec <container> /app/gorun backup") || strings.Contains(coolify, "sqlite3") {
		t.Error("coolify.md backup must use the binary (no shell, no sqlite3)")
	}
	if !strings.Contains(coolify, "GORUN_SETUP_TOKEN") || !strings.Contains(coolify, "/app/gorun user create --admin") {
		t.Error("coolify.md first run must document the setup token or user create --admin")
	}
	setup := repoFile(t, "e2e/global-setup.ts")
	for _, want := range []string{"k.startsWith('GORUN_')", "k.startsWith('STRIPE_')", "stripeKey.startsWith('sk_test_')"} {
		if !strings.Contains(setup, want) {
			t.Errorf("e2e/global-setup.ts lacks %q", want)
		}
	}
	if strings.Contains(setup, "process.env.STRIPE_WEBHOOK_SECRET") {
		t.Error("e2e must use its local webhook secret constant, not the shell's")
	}
	if strings.Contains(setup, "...process.env,\n    GORUN_DATA") {
		t.Error("e2e server env must not spread process.env")
	}
}

// T17: the Arabic README keeps code blocks Latin-only and has parity
// pointers (gorun.toml keys, ARCHITECTURE link) with the English half.
func TestReadmeArabicParity(t *testing.T) {
	readme := repoFile(t, "README.md")
	i := strings.Index(readme, "## English")
	if i < 0 {
		t.Fatal("no English section")
	}
	ar := readme[:i]
	inBlock := false
	for n, line := range strings.Split(ar, "\n") {
		if strings.HasPrefix(line, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock {
			for _, r := range line {
				if r >= 0x0600 && r <= 0x06FF {
					t.Errorf("README.md:%d: Arabic inside a code block: %s", n+1, line)
					break
				}
			}
		}
	}
	for _, want := range []string{"`gorun.toml`", "`setup_token`", "docs/ARCHITECTURE.md)", "GORUN_ENV=prod"} {
		if !strings.Contains(ar, want) {
			t.Errorf("Arabic README lacks %s", want)
		}
	}
	if !strings.Contains(repoFile(t, "CHANGELOG.md"), "## [Unreleased]") {
		t.Error("CHANGELOG.md lacks an Unreleased section")
	}
}
