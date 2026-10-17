package admin_test

import (
	"testing"

	"github.com/zadstack/gorun/internal/admin"
)

func TestOpenAIRequiresExplicitEndpoint(t *testing.T) {
	for _, base := range []string{"", "   "} {
		values, fields := admin.AIConfig("openai", base, "model", "", false, make([]byte, 32))
		if fields["base_url"] == "" {
			t.Fatal("OpenAI configuration accepted an unspecified destination")
		}
		if values["ai.base_url"] != "" {
			t.Fatal("an unspecified destination was replaced with a third-party endpoint")
		}
	}
}
