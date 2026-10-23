package commerce

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/observe"
)

// ObservationKind is the background capability that reads each billing
// server's catalog. System Health reads the saved observation; it never
// calls a server while rendering.
const ObservationKind = "commerce.billing_server"

const serverObservationTimeout = 5 * time.Second

// registerObservation declares the capability: keys are server ids, the
// revision covers the connection details, and the check is the read-only
// catalog fetch. A missing observe service (tests booting commerce alone)
// leaves the health line at "not observed".
func (s *service) registerObservation(obs *observe.Service) {
	s.observe = obs
	if obs == nil {
		return
	}
	obs.Register(observe.Spec{Kind: ObservationKind, Timeout: serverObservationTimeout, MaxAttempts: 2, MaxInFlight: 3,
		Keys: func(ctx context.Context) ([]string, error) {
			servers, err := s.Servers(ctx)
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(servers))
			for _, srv := range servers {
				ids = append(ids, srv.ID)
			}
			return ids, nil
		},
		Revision: func(ctx context.Context, id string) (string, error) {
			r, err := s.server(ctx, id)
			if errors.Is(err, ErrNotFound) {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			sum := sha256.Sum256([]byte(r.BaseURL + "\x00" + r.ClientID + "\x00" + r.secretEnc + "\x00" + r.Protocol))
			return hex.EncodeToString(sum[:8]), nil
		},
		Run: func(ctx context.Context, id, _ string) (observe.Result, error) {
			r, err := s.server(ctx, id)
			if err != nil {
				return observe.Result{}, err
			}
			cat, err := s.fetchCatalog(ctx, r)
			if err != nil {
				return observe.Result{}, errors.New(truncate(err.Error(), 200))
			}
			return observe.Result{Scope: "connectivity", Data: map[string]any{"plans": len(cat.Plans)}}, nil
		},
		Remediation: "Check the billing server address, client id and secret, then run the server test.",
	})
}

// refreshServer queues a background read after a server was saved or
// tested; it never blocks the caller.
func (s *service) refreshServer(ctx context.Context, id string) {
	if s.observe == nil {
		return
	}
	if _, err := s.observe.Refresh(ctx, ObservationKind, id); err != nil && !errors.Is(err, observe.ErrNotFound) {
		s.log.Warn("commerce: queue server observation", "server", id, "err", err)
	}
}

// healthChecks contributes to the system.health.checks filter: Stripe key
// presence, webhook secret presence, and each billing server's saved
// reachability observation with its age. A stored Stripe key or webhook
// secret is CheckWarn — configuration only; this page never proves
// checkout or delivery, and never contacts a server.
func (s *service) healthChecks(ctx context.Context, v any) (any, error) {
	checks, ok := v.(*[]admin.HealthCheck)
	if !ok || checks == nil {
		return v, nil
	}
	loc := i18n.Locale(ctx)
	t := func(k string, args ...any) string { return s.d.I18n.T(loc, k, args...) }
	add := func(name, status, detail string) {
		*checks = append(*checks, admin.HealthCheck{Name: name, Status: status, Detail: detail})
	}
	mode := s.stripeMode(ctx)
	st := s.stripeStatus(ctx, mode)
	modeLabel := t("commerce.settings.mode_live")
	if mode == "test" {
		modeLabel = t("commerce.settings.mode_test")
	}
	switch {
	case !st.KeyConfigured:
		add(t("commerce.health.stripe"), admin.CheckWarn, modeLabel+" · "+t("commerce.health.stripe_missing"))
	case st.Auth.State == authAuthenticated:
		add(t("commerce.health.stripe"), admin.CheckOK, modeLabel+" · "+t("commerce.health.authenticated", formatAge(ageSeconds(s.now(), st.Auth.CheckedAt))))
	case st.Auth.State == authMismatch:
		add(t("commerce.health.stripe"), admin.CheckFail, modeLabel+" · "+t("commerce.health.auth_mismatch"))
	case st.Auth.State == authFailed:
		add(t("commerce.health.stripe"), admin.CheckFail, modeLabel+" · "+t("commerce.health.auth_failed"))
	case st.Auth.State == authStale:
		add(t("commerce.health.stripe"), admin.CheckWarn, modeLabel+" · "+t("commerce.health.auth_stale", formatAge(ageSeconds(s.now(), st.Auth.CheckedAt))))
	default:
		add(t("commerce.health.stripe"), admin.CheckWarn, modeLabel+" · "+t("commerce.health.configured"))
	}
	switch {
	case !st.WebhookConfigured:
		add(t("commerce.health.webhook"), admin.CheckWarn, modeLabel+" · "+t("commerce.health.webhook_missing"))
	case st.Delivery.Verified:
		add(t("commerce.health.webhook"), admin.CheckOK, modeLabel+" · "+t("commerce.health.webhook_verified", formatAge(ageSeconds(s.now(), st.Delivery.LastAt))))
	default:
		add(t("commerce.health.webhook"), admin.CheckWarn, modeLabel+" · "+t("commerce.health.webhook_set", s.baseURL(ctx)+"/webhooks/stripe/"+mode))
	}
	servers, err := s.Servers(ctx)
	if err != nil {
		return checks, err
	}
	for _, srv := range servers {
		name := t("commerce.health.server", srv.Name)
		if s.observe == nil {
			add(name, admin.CheckWarn, t("commerce.health.not_observed"))
			continue
		}
		o, err := s.observe.Get(ctx, ObservationKind, srv.ID)
		if err != nil {
			add(name, admin.CheckWarn, t("commerce.health.not_observed"))
			continue
		}
		detail := ""
		if o.Saved != nil {
			detail = t("commerce.health.observed", formatAge(o.Saved.AgeSeconds))
		}
		switch o.Status {
		case "fresh":
			add(name, admin.CheckOK, t("commerce.health.reachable")+" · "+detail)
		case "stale":
			add(name, admin.CheckWarn, t("commerce.health.stale")+" · "+detail)
		case "failed":
			msg := t("commerce.health.unreachable")
			if o.Attempt != nil && o.Attempt.Error != "" {
				msg += ": " + o.Attempt.Error
			}
			if detail != "" {
				msg += " · " + detail
			}
			add(name, admin.CheckFail, msg)
		case "waiting", "running":
			add(name, admin.CheckWarn, t("commerce.health.checking")+" · "+detail)
		default:
			add(name, admin.CheckWarn, t("commerce.health.not_observed"))
		}
	}
	return checks, nil
}

// ageSeconds is how long ago the stored timestamp was (0 when unparsable).
func ageSeconds(now time.Time, stored string) int64 {
	ts, err := db.ParseTime(stored)
	if err != nil || ts.IsZero() {
		return 0
	}
	return max(int64(now.Sub(ts).Seconds()), 0)
}

func formatAge(seconds int64) string {
	switch {
	case seconds < 60:
		return itoa(seconds) + "s"
	case seconds < 3600:
		return itoa(seconds/60) + "m"
	case seconds < 86400:
		return itoa(seconds/3600) + "h"
	}
	return itoa(seconds/86400) + "d"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// requirements reports the store's configuration to the capability report
// for the active mode, as three separate states: configured (both secrets
// present), authenticated (the key passed a read-only Stripe check) and
// webhook delivery verified (a signed event from Stripe reached this mode's
// endpoint). Key presence or prefix is never reported as verification.
func (s *service) requirements(ctx context.Context, v any) (any, error) {
	list, ok := v.(*[]admin.ModuleRequirement)
	if !ok || list == nil {
		return v, nil
	}
	mode := s.stripeMode(ctx)
	st := s.stripeStatus(ctx, mode)
	req := admin.ModuleRequirement{Module: "commerce", Configured: true}
	switch {
	case !st.KeyConfigured:
		req.Configured, req.Reason, req.Remediation = false, "stripe_secret_key_missing", "Set the "+mode+" Stripe secret key under Commerce → Store settings (or STRIPE_SECRET_KEY for live) before selling."
	case !st.WebhookConfigured:
		req.Configured, req.Reason, req.Remediation = false, "stripe_webhook_secret_missing", "Set the "+mode+" webhook signing secret and point a Stripe endpoint at /webhooks/stripe/"+mode+"."
	case st.Auth.State == authMismatch:
		req.Configured, req.Reason, req.Remediation = false, "stripe_key_mode_mismatch", "The "+mode+" slot holds a key Stripe reports as the other mode; replace it under Commerce → Store settings."
	case st.Auth.State != authAuthenticated:
		req.Reason, req.Remediation = "configured_not_authenticated", "Run “Verify key” for "+mode+" under Commerce → Store settings; presence of a key proves nothing."
	case !st.Delivery.Verified:
		req.Reason, req.Remediation = "authenticated_delivery_unverified", "No signed event from Stripe has reached /webhooks/stripe/"+mode+" yet: send one from the Stripe Dashboard or complete a "+mode+" checkout."
	default:
		req.Reason, req.Remediation = "verified", "Key authenticated and webhook delivery observed for "+mode+" mode; checkout itself is proven only by a real order."
	}
	*list = append(*list, req)
	return list, nil
}
