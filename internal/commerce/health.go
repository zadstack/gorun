package commerce

import (
	"context"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/i18n"
)

const (
	healthTTL          = 5 * time.Minute
	healthCheckTimeout = 5 * time.Second
)

// healthCache remembers billing-server reachability for healthTTL so the
// System Health page does not call every server on each view.
type healthCache struct {
	mu      sync.Mutex
	entries map[string]healthEntry
	now     func() time.Time
}

type healthEntry struct {
	at  time.Time
	err error
}

func newHealthCache() *healthCache {
	return &healthCache{entries: map[string]healthEntry{}, now: time.Now}
}

func (c *healthCache) put(id string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = healthEntry{at: c.now(), err: err}
}

func (c *healthCache) get(id string) (error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || c.now().Sub(e.at) > healthTTL {
		return nil, false
	}
	return e.err, true
}

func (c *healthCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// healthChecks contributes to the system.health.checks filter: Stripe key,
// webhook secret and each billing server's reachability (GET /catalog,
// cached 5 minutes).
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
		add(t("commerce.health.stripe"), admin.CheckOK, t("commerce.health.configured"))
	} else {
		add(t("commerce.health.stripe"), admin.CheckWarn, t("commerce.health.stripe_missing"))
	}
	if s.webhookSecret(ctx) != "" {
		add(t("commerce.health.webhook"), admin.CheckOK, s.baseURL(ctx)+"/webhooks/stripe")
	} else {
		add(t("commerce.health.webhook"), admin.CheckWarn, t("commerce.health.webhook_missing"))
	}
	servers, err := s.Servers(ctx)
	if err != nil {
		return checks, err
	}
	// Uncached servers are checked concurrently, 5s each.
	errs := make([]error, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		if cerr, cached := s.health.get(srv.ID); cached {
			errs[i] = cerr
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.server(ctx, srv.ID)
			if err == nil {
				cctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
				_, err = s.fetchCatalog(cctx, r)
				cancel()
			}
			errs[i] = err
			s.health.put(srv.ID, err)
		}()
	}
	wg.Wait()
	for i, srv := range servers {
		name := t("commerce.health.server", srv.Name)
		if errs[i] != nil {
			add(name, admin.CheckFail, truncate(errs[i].Error(), 200))
		} else {
			add(name, admin.CheckOK, t("commerce.health.reachable"))
		}
	}
	return checks, nil
}
