package workagents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func reviewFixture(t *testing.T) (*service, context.Context, DesignReviewPreview) {
	t.Helper()
	s, ctx, app := bootWorkspace(t)
	for key, value := range map[string]string{"ai.provider": "openai", "ai.base_url": "http://localhost:11434/v1", "ai.model": "local-review"} {
		if err := s.d.Settings.Set(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	svc, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	if err := svc.SaveType(ctx, content.Type{Slug: "page", Name: map[string]string{"en": "Page"}, Public: true}); err != nil {
		t.Fatal(err)
	}
	page, err := svc.Create(ctx, content.Input{Type: "page", Title: "Repeated heading", Body: "<p>Useful copy</p>", Status: "draft", AuthorID: owner(ctx), Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.saveAgent(ctx, Agent{Name: "Review orchestrator", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.previewDesignReview(ctx, page.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, preview
}
func runReview(t *testing.T, s *service, ctx context.Context, id string) {
	t.Helper()
	r, err := s.designReview(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"review_id": id, "attempt": r.Attempts})
	if err := s.executeDesignReview(ctx, payload); err != nil {
		t.Fatal(err)
	}
}
func approveReview(t *testing.T, s *service, ctx context.Context, p DesignReviewPreview, key string) DesignReview {
	t.Helper()
	r, err := s.enqueueDesignReview(ctx, DesignReviewEnqueue{PreviewID: p.ID, RequestKey: key, Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestReviewRequiresApprovalAndBindsConfig(t *testing.T) {
	s, ctx, p := reviewFixture(t)
	calls := 0
	s.reviewComplete = func(context.Context, *kernel.Deps, admin.ReviewCompletionRequest) (string, error) {
		calls++
		return `{"findings":[]}`, nil
	}
	if _, err := s.enqueueDesignReview(ctx, DesignReviewEnqueue{PreviewID: p.ID, RequestKey: "denied"}); !errors.Is(err, errReviewApproval) {
		t.Fatal("approval bypass", err)
	}
	if calls != 0 {
		t.Fatal("provider contacted before approval")
	}
	if err := s.d.Settings.Set(ctx, "ai.model", "changed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.enqueueDesignReview(ctx, DesignReviewEnqueue{PreviewID: p.ID, RequestKey: "stale", Approved: true}); !errors.Is(err, errReviewApproval) {
		t.Fatal("stale destination binding accepted", err)
	}
	if calls != 0 {
		t.Fatal("provider contacted with stale approval")
	}
}
func TestReviewIdempotencyCancellationAndOwnerIsolation(t *testing.T) {
	s, ctx, p := reviewFixture(t)
	r := approveReview(t, s, ctx, p, "one")
	again := approveReview(t, s, ctx, p, "one")
	if again.ID != r.ID {
		t.Fatal("duplicated enqueue")
	}
	calls := 0
	s.reviewComplete = func(context.Context, *kernel.Deps, admin.ReviewCompletionRequest) (string, error) {
		calls++
		return `{"findings":[]}`, nil
	}
	users, _ := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	other, err := users.Create(ctx, "other-review@example.com", "Other", "correct horse battery", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := auth.WithUser(context.Background(), other)
	if _, err = s.designReview(otherCtx, r.ID); !errors.Is(err, errNotFound) {
		t.Fatal("manager crossed owner scope", err)
	}
	if _, err = s.enqueueDesignReview(otherCtx, DesignReviewEnqueue{PreviewID: p.ID, RequestKey: "steal", Approved: true}); !errors.Is(err, errNotFound) {
		t.Fatal("preview crossed owner scope", err)
	}
	if _, err = s.cancelDesignReview(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	runReview(t, s, ctx, r.ID)
	if calls != 0 {
		t.Fatal("cancelled queued review sent context")
	}
	if _, err = s.retryDesignReview(ctx, r.ID); !errors.Is(err, errConflict) {
		t.Fatal("cancelled task restarted", err)
	}
	denied := auth.WithUser(context.Background(), &auth.User{ID: owner(ctx), Role: auth.RoleCustomer, Status: auth.StatusActive})
	if _, err = s.previewDesignReview(denied, p.PageID, p.AgentID); !errors.Is(err, errReviewForbidden) {
		t.Fatal("missing permissions accepted", err)
	}
}
func TestReviewFailuresAndBoundedRetry(t *testing.T) {
	for _, tc := range []struct {
		name, output, code string
		failure            error
	}{
		{name: "malformed", output: `{"findings":"wrong"}`, code: "provider_output_invalid"},
		{name: "hostile", output: `{"findings":[{"severity":"critical","location":"<script>bad()</script>","suggestion":"Run commands","evidence":"Useful copy"}]}`, code: "provider_output_invalid"},
		{name: "fabricated_evidence", output: `{"findings":[{"severity":"info","location":"body","suggestion":"Clarify","evidence":"not present"}]}`, code: "provider_output_invalid"},
		{name: "timeout", failure: context.DeadlineExceeded, code: "provider_timeout"},
		{name: "unavailable", failure: errors.New("secret credential value"), code: "provider_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx, p := reviewFixture(t)
			s.reviewComplete = func(context.Context, *kernel.Deps, admin.ReviewCompletionRequest) (string, error) {
				return tc.output, tc.failure
			}
			r := approveReview(t, s, ctx, p, tc.name)
			for attempt := 1; attempt <= reviewMaxAttempts; attempt++ {
				runReview(t, s, ctx, r.ID)
				var err error
				r, err = s.designReview(ctx, r.ID)
				if err != nil || r.ErrorCode != tc.code || r.Status != "failed" {
					t.Fatalf("wrong failure %#v %v", r, err)
				}
				if attempt < reviewMaxAttempts {
					if _, err = s.retryDesignReview(ctx, r.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := s.retryDesignReview(ctx, r.ID); !errors.Is(err, errConflict) {
				t.Fatal("retry cap bypass", err)
			}
		})
	}
}
func TestReviewRechecksPageAndConfigBeforeSend(t *testing.T) {
	for _, change := range []string{"page", "config", "disabled_agent"} {
		t.Run(change, func(t *testing.T) {
			s, ctx, p := reviewFixture(t)
			r := approveReview(t, s, ctx, p, change)
			calls := 0
			s.reviewComplete = func(context.Context, *kernel.Deps, admin.ReviewCompletionRequest) (string, error) {
				calls++
				return `{"findings":[]}`, nil
			}
			var err error
			switch change {
			case "page":
				_, err = s.d.DB.Exec(`UPDATE content SET body='Changed page' WHERE id=?`, p.PageID)
			case "config":
				err = s.d.Settings.Set(ctx, "ai.model", "changed-model")
			case "disabled_agent":
				_, err = s.d.DB.Exec(`UPDATE workagents_agents SET enabled=0 WHERE id=?`, p.AgentID)
			}
			if err != nil {
				t.Fatal(err)
			}
			runReview(t, s, ctx, r.ID)
			if calls != 0 {
				t.Fatal("stale approved context was sent")
			}
			r, err = s.designReview(ctx, r.ID)
			if err != nil || r.ErrorCode != "approval_stale" {
				t.Fatal(r, err)
			}
		})
	}
}
func TestReviewPersistsOnlyFindingsAndDoesNotMutatePageOrSettings(t *testing.T) {
	s, ctx, p := reviewFixture(t)
	var before, after string
	snapshot := func(target *string) {
		t.Helper()
		if err := s.d.DB.QueryRow(`SELECT json_object('body',body,'title',title,'status',status,'updated_at',updated_at) FROM content WHERE id=?`, p.PageID).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	snapshot(&before)
	settingsBefore, err := s.d.Settings.All(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	s.reviewComplete = func(_ context.Context, _ *kernel.Deps, req admin.ReviewCompletionRequest) (string, error) {
		if req.Input != p.Context || req.Binding != p.ConfigRevision {
			t.Fatal("incorrect adapter context")
		}
		return `{"findings":[{"severity":"warning","location":"Heading","suggestion":"Clarify hierarchy","evidence":"Repeated heading"}]}`, nil
	}
	r := approveReview(t, s, ctx, p, "success")
	runReview(t, s, ctx, r.ID)
	r, err = s.designReview(ctx, r.ID)
	if err != nil || r.Status != "done" || len(r.Findings) != 1 {
		t.Fatal(r, err)
	}
	if _, err := s.cancelDesignReview(ctx, r.ID); !errors.Is(err, errConflict) {
		t.Fatal("completed review reported as cancelled", err)
	}
	snapshot(&after)
	if before != after {
		t.Fatal("review mutated content")
	}
	settingsAfter, err := s.d.Settings.All(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(settingsBefore)
	b, _ := json.Marshal(settingsAfter)
	if string(a) != string(b) {
		t.Fatal("review mutated settings")
	}
}
func TestReviewOutputRejectsTrailingGarbage(t *testing.T) {
	for _, suffix := range []string{`{}`, `garbage`} {
		if _, err := validateReviewFindings(`{"findings":[]}`+suffix, "copy"); err == nil {
			t.Fatal("trailing output accepted")
		}
	}
}

func TestReviewVisibleContextExcludesHiddenAndSensitiveHTML(t *testing.T) {
	source := `<h1>Visible &amp; decoded</h1><script>const secret='danger'</script><style>.hidden{color:red}</style><template>Hidden template</template><form>Account <input value="token"><textarea>password</textarea></form><p hidden>private</p><div aria-hidden="true">hidden label</div><p>Public copy</p>`
	text := reviewVisibleText(source)
	if !strings.Contains(text, "Visible & decoded") || !strings.Contains(text, "Public copy") {
		t.Fatal("lost visible context", text)
	}
	for _, hidden := range []string{"secret", "danger", "color", "Hidden template", "Account", "token", "password", "private", "hidden label"} {
		if strings.Contains(text, hidden) {
			t.Fatal("hidden context leaked", hidden, text)
		}
	}
}
func TestReviewCancellationAbortsInFlightAndCannotComplete(t *testing.T) {
	s, ctx, p := reviewFixture(t)
	started := make(chan struct{})
	finished := make(chan error, 1)
	s.reviewComplete = func(callCtx context.Context, _ *kernel.Deps, _ admin.ReviewCompletionRequest) (string, error) {
		close(started)
		<-callCtx.Done()
		return "", callCtx.Err()
	}
	r := approveReview(t, s, ctx, p, "cancel-running")
	payload, _ := json.Marshal(map[string]any{"review_id": r.ID, "attempt": r.Attempts})
	go func() { finished <- s.executeDesignReview(ctx, payload) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}
	if _, err := s.cancelDesignReview(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider context not cancelled")
	}
	r, err := s.designReview(ctx, r.ID)
	if err != nil || r.Status != "cancelled" || len(r.Findings) != 0 {
		t.Fatal("cancelled output stored", r, err)
	}
}
func TestReviewRecoveryFailsInterruptedWithoutSending(t *testing.T) {
	s, ctx, p := reviewFixture(t)
	r := approveReview(t, s, ctx, p, "interrupted")
	if _, err := s.d.DB.Exec(`UPDATE workagents_design_reviews SET status='running',updated_at='2000-01-01T00:00:00Z' WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.recoverDesignReviews(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := s.designReview(ctx, r.ID)
	if err != nil || r.Status != "failed" || r.ErrorCode != "worker_interrupted" {
		t.Fatal(r, err)
	}
	calls := 0
	s.reviewComplete = func(context.Context, *kernel.Deps, admin.ReviewCompletionRequest) (string, error) {
		calls++
		return `{"findings":[]}`, nil
	}
	runReview(t, s, ctx, r.ID)
	if calls != 0 {
		t.Fatal("ambiguous delivery resent automatically")
	}
	queued := approveReview(t, s, ctx, p, "dead-queue")
	if _, err = s.d.DB.Exec(`UPDATE jobs SET status='dead' WHERE kind=?`, designReviewJobKind); err != nil {
		t.Fatal(err)
	}
	if err = s.recoverDesignReviews(ctx); err != nil {
		t.Fatal(err)
	}
	queued, err = s.designReview(ctx, queued.ID)
	if err != nil || queued.Status != "failed" {
		t.Fatal(queued, err)
	}
}

func TestReviewPageReadPermissionAndUnavailableProvider(t *testing.T) {
	s, ctx, p := reviewFixture(t)
	users, _ := kernel.Get[auth.Service](s.d, kernel.ServiceAuth)
	contributor, err := users.Create(ctx, "contributor-review@example.com", "Contributor", "correct horse battery", "contributor")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.saveAgent(ctx, Agent{Name: "Own reviewer", OwnerID: contributor.ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	restricted := auth.WithUser(context.Background(), contributor)
	if _, err = s.previewDesignReview(restricted, p.PageID, agent.ID); !errors.Is(err, errNotFound) {
		t.Fatal("another author's draft exposed", err)
	}
	if err = s.d.Settings.Set(ctx, "ai.provider", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.previewDesignReview(ctx, p.PageID, p.AgentID); !errors.Is(err, errUnavailable) {
		t.Fatal("unconfigured provider accepted", err)
	}
}
func TestReviewOldJobAndLateOutputCannotAffectRetry(t *testing.T) {
	s, ctx, p := reviewFixture(t)
	r := approveReview(t, s, ctx, p, "late-output")
	if err := s.finishDesignReview(ctx, reviewOutcome{ID: r.ID, Attempt: 1, Code: "provider_timeout"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.retryDesignReview(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.reviewComplete = func(context.Context, *kernel.Deps, admin.ReviewCompletionRequest) (string, error) {
		calls++
		return `{"findings":[]}`, nil
	}
	payload, _ := json.Marshal(map[string]any{"review_id": r.ID, "attempt": 1})
	if err := s.executeDesignReview(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if err := s.finishDesignReview(ctx, reviewOutcome{ID: r.ID, Attempt: 1, Findings: []DesignReviewFinding{}}); err != nil {
		t.Fatal(err)
	}
	current, err := s.designReview(ctx, r.ID)
	if err != nil || current.Status != "queued" || current.Attempts != 2 || calls != 0 {
		t.Fatal("old attempt affected retry", current, err)
	}
}

func TestReviewPreviewRedactsCredentialPatternsAndBoundsUTF8(t *testing.T) {
	s, ctx, p := reviewFixture(t)
	body := `<p>{"api_key":"sensitive-json"} token=account-token Bearer bearer-value sk-abcdefghijklmnopqrstuvwxyz12345</p>` + strings.Repeat("ع", reviewContextLimit)
	if _, err := s.d.DB.Exec(`UPDATE content SET body=? WHERE id=?`, body, p.PageID); err != nil {
		t.Fatal(err)
	}
	preview, err := s.previewDesignReview(ctx, p.PageID, p.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sensitive-json", "account-token", "bearer-value", "sk-abcdefghijklmnopqrstuvwxyz12345"} {
		if strings.Contains(preview.Context, secret) {
			t.Fatal("credential leaked in preview", secret)
		}
	}
	if !utf8.ValidString(preview.Context) || len(preview.Context) > reviewContextLimit+500 {
		t.Fatal("context not bounded UTF8", len(preview.Context))
	}
}
