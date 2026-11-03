package admin

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
)

const layoutAdvisorMetadataLimit = 4 << 10
const layoutAdvisorOutputTokens = 64

type builderLayoutAdvisor struct{ h *handlers }

func (a *builderLayoutAdvisor) DescribeLayout(ctx context.Context) (string, string, error) {
	destination, binding, err := WorkAgentReviewBinding(ctx, a.h.d, "", "")
	if err != nil {
		return "", "", err
	}
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Host == "" {
		return "", "", errAINotConfigured
	}
	return parsed.Host, binding, nil
}
func (a *builderLayoutAdvisor) SuggestLayout(ctx context.Context, binding, input string) (string, error) {
	user := auth.UserFromContext(ctx)
	if user == nil || !a.h.aiLimit.allow(user.ID) || len(input) > layoutAdvisorMetadataLimit {
		return "", errors.New("layout advisor unavailable")
	}
	config, current, err := resolvedReviewConfig(ctx, a.h.d, "", "")
	if err != nil {
		return "", err
	}
	if binding == "" || !hmac.Equal([]byte(binding), []byte(current)) {
		return "", errors.New("layout advisor configuration changed")
	}
	a.h.d.Audit.Record(ctx, "builder.layout_ai.request", "component", "", map[string]any{"metadata_only": true})
	output, err := aiCompleteBounded(ctx, config, "Choose exactly one candidate_ids value from the supplied layout metadata. Return only that ID, no JSON, explanation or other text. You are choosing presentation only; never add content, URLs, classes or actions.", input, layoutAdvisorOutputTokens)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}
