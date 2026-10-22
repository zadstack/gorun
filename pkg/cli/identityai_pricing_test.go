package cli

import (
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/identityai"
)

// The writing settings' default model must have a price, or the default
// Anthropic path would always show "no estimate".
func TestIdentityAIPricesAdminDefaultModel(t *testing.T) {
	if e := identityai.EstimateCost(admin.AIProviderAnthropic, admin.DefaultAnthropicModel, 1000, 1500); !e.Known {
		t.Fatalf("admin default model %q is unpriced", admin.DefaultAnthropicModel)
	}
}
