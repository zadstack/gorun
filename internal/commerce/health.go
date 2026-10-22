package commerce

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/zadstack/gorun/internal/admin"
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
	if s.stripeSecretKey(ctx) != "" {
		add(t("commerce.health.stripe"), admin.CheckWarn, t("commerce.health.configured"))
	} else {
		add(t("commerce.health.stripe"), admin.CheckWarn, t("commerce.health.stripe_missing"))
	}
	if s.webhookSecret(ctx) != "" {
		add(t("commerce.health.webhook"), admin.CheckWarn, t("commerce.health.webhook_set", s.baseURL(ctx)+"/webhooks/stripe"))
	} else {
		add(t("commerce.health.webhook"), admin.CheckWarn, t("commerce.health.webhook_missing"))
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

// requirements reports the store's configuration to the capability report:
// a Stripe secret key and webhook secret are required for live checkout.
// Presence is configuration only; delivery is never claimed here.
func (s *service) requirements(ctx context.Context, v any) (any, error) {
	list, ok := v.(*[]admin.ModuleRequirement)
	if !ok || list == nil {
		return v, nil
	}
	req := admin.ModuleRequirement{Module: "commerce", Configured: true, Reason: "configured_not_verified", Remediation: "Checkout and webhook delivery are verified only by a real Stripe test-mode order; see docs/COMMERCE.md."}
	switch {
	case s.stripeSecretKey(ctx) == "":
		req.Configured, req.Reason, req.Remediation = false, "stripe_secret_key_missing", "Set STRIPE_SECRET_KEY (or the Stripe key under Commerce → Launch store) before selling."
	case s.webhookSecret(ctx) == "":
		req.Configured, req.Reason, req.Remediation = false, "stripe_webhook_secret_missing", "Set STRIPE_WEBHOOK_SECRET and point the Stripe webhook at /webhooks/stripe."
	}
	*list = append(*list, req)
	return list, nil
}
