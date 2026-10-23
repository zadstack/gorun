package admin

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/observe"
)

func TestOnboardingSavedEvidenceUsesCurrentRevisionWithoutChecks(t *testing.T) {
	h := edgeTestHandlers(t)
	ctx := context.Background()
	h.users, _ = kernel.Get[auth.Service](h.d, kernel.ServiceAuth)
	h.mail = mailer.New(h.d.Settings, h.d.Log).WithKey(h.d.Config.SecretKey)
	h.observe = observe.New(h.d.DB, h.d.Jobs, h.d.Log)
	h.registerCapabilityObservations()
	user, err := h.users.Create(ctx, "evidence@example.invalid", "Evidence", "local-fixture-password", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.d.Settings.Set(ctx, "mail.host", "smtp.example.invalid"); err != nil {
		t.Fatal(err)
	}
	secret, err := h.encryptConnectionSecrets(connectionSecrets{Token: "private-profile-token"})
	if err != nil {
		t.Fatal(err)
	}
	profile := connectionRecord{ID: db.NewID(), Version: "version-one", Name: "Research profile", Kind: "ai", Provider: "openai", Endpoint: "https://api.example.invalid/v1", Model: "fixture-model", Secret: secret}
	if err := h.d.Settings.Set(ctx, connectionPrefix+profile.ID, profile); err != nil {
		t.Fatal(err)
	}
	mailRevision, err := h.mailRevision(ctx, "user:"+user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct{ kind, key, revision, scope string }{{kindOperationMail, "user:" + user.ID, mailRevision, "operation:send"}, {kindConnection, profile.ID, profile.Version, "connectivity"}, {kindOperationAI, profile.ID, profile.Version, "operation:complete"}} {
		if err := h.observe.Record(ctx, record.kind, record.key, record.revision, observe.Result{Scope: record.scope}, nil, time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest("GET", "/admin/platform-setup", nil).WithContext(auth.WithUser(ctx, user))
	state, err := h.onboardingState(request.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(view onboardingView, title string) dashboardSetupItem {
		t.Helper()
		for _, row := range view.Items {
			if row.TitleKey == title || row.Title == title {
				return row
			}
		}
		t.Fatalf("missing row %s", title)
		return dashboardSetupItem{}
	}
	mailRow := lookup(state, "onboarding.email")
	profileRow := lookup(state, profile.Name)
	if mailRow.State != "tested" || len(mailRow.Evidence) != 1 || mailRow.Evidence[0].CheckedAt == "" {
		t.Fatalf("missing recorded delivery evidence: %+v", mailRow)
	}
	if profileRow.State != "tested" || len(profileRow.Evidence) != 2 {
		t.Fatalf("missing separate profile checks: %+v", profileRow)
	}
	if err := h.d.Settings.Set(ctx, "mail.host", "changed.example.invalid"); err != nil {
		t.Fatal(err)
	}
	profile.Version = "version-two"
	if err := h.d.Settings.Set(ctx, connectionPrefix+profile.ID, profile); err != nil {
		t.Fatal(err)
	}
	state, err = h.onboardingState(request.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	mailRow = lookup(state, "onboarding.email")
	profileRow = lookup(state, profile.Name)
	if mailRow.State == "tested" || mailRow.Evidence[0].StatusKey != "onboarding.check.stale" {
		t.Fatalf("stale mail promoted: %+v", mailRow)
	}
	if profileRow.State == "tested" || profileRow.State == "connected" || profileRow.Evidence[0].StatusKey != "onboarding.check.stale" {
		t.Fatalf("stale profile promoted: %+v", profileRow)
	}
	var pending int
	if err := h.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE status IN ('pending','running')`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatal("read queued external checks")
	}
}
