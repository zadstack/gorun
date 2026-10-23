package admin_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
)

func TestDesignReviewConsentPinsResolvedDestinationModelAndCredential(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	provider := newFakeAI(t, 200, openAIReply(`{"findings":[],"unexpected":"`+testAIKey+`"}`), nil)
	ctx := context.Background()
	configureAI(t, s, url.Values{"provider": {"openai"}, "model": {"review-model"}, "base_url": {provider.srv.URL + "/v1"}, "api_key": {testAIKey}})
	destination, binding, err := admin.WorkAgentReviewBinding(ctx, s.app.Deps, "", "")
	if err != nil || destination != provider.srv.URL+"/v1/chat/completions" || strings.Contains(binding, testAIKey) {
		t.Fatal(destination, binding, err)
	}
	request := admin.ReviewCompletionRequest{Binding: binding, System: "Review only", Input: "Public test copy"}
	configureAI(t, s, url.Values{"provider": {"openai"}, "model": {"changed-model"}, "base_url": {provider.srv.URL + "/v1"}, "api_key": {testAIKey}})
	if _, err = admin.CompleteWorkAgentReview(ctx, s.app.Deps, request); err == nil || provider.calls.Load() != 0 {
		t.Fatal("stale consent sent", err, provider.calls.Load())
	}
	configureAI(t, s, url.Values{"provider": {"openai"}, "model": {"review-model"}, "base_url": {provider.srv.URL + "/v1"}, "api_key": {testAIKey}})
	if _, err = admin.CompleteWorkAgentReview(ctx, s.app.Deps, request); err == nil || provider.calls.Load() != 0 {
		t.Fatal("restored configuration revived old consent", err, provider.calls.Load())
	}
	_, request.Binding, err = admin.WorkAgentReviewBinding(ctx, s.app.Deps, "", "")
	if err != nil {
		t.Fatal(err)
	}
	output, err := admin.CompleteWorkAgentReview(ctx, s.app.Deps, request)
	if err != nil || provider.calls.Load() != 1 || strings.Contains(output, testAIKey) {
		t.Fatal(err, provider.calls.Load())
	}
	configureAI(t, s, url.Values{"provider": {"openai"}, "model": {"changed-model"}, "base_url": {provider.srv.URL + "/v1"}, "api_key": {"replacement-test-key"}})
	if _, err = admin.CompleteWorkAgentReview(ctx, s.app.Deps, request); err == nil || provider.calls.Load() != 1 {
		t.Fatal("credential change sent", err, provider.calls.Load())
	}
}
