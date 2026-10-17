package cli

import (
	"strings"
	"testing"
)

func TestGoRunCanonicalCLIHelp(t *testing.T) {
	if !strings.Contains(usage, "usage: gorun <command>") || !strings.Contains(usage, "GORUN_MCP_KEY") {
		t.Fatal("CLI help must identify GoRun and canonical environment")
	}
}
