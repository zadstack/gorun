package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWebhookCreateHelpNoConfigParse(t *testing.T) {
	var out bytes.Buffer
	err := webhookCmd([]string{"create", "--help"}, &out)
	if err == nil || !strings.Contains(err.Error(), "usage: gorun webhook") {
		t.Fatalf("create --help: %v", err)
	}
	if strings.Contains(err.Error(), "flag: help requested") || strings.Contains(err.Error(), "Usage of gorun") {
		t.Fatalf("must not hit config flag help: %v", err)
	}
}
