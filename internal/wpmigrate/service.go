package wpmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

// service implements Service on top of the earlier modules' services.
type service struct {
	d         *kernel.Deps
	db        *sql.DB
	log       *slog.Logger
	audit     *audit.Writer
	hooks     *hooks.Registry
	settings  *settings.Store
	cfg       config.Config
	content   content.Service
	tax       taxonomy.Service
	media     media.Service
	menus     menus.Service
	redirects redirects.Service
	users     auth.Service
	linker    *theme.Linker

	q             enqueuer
	http          *http.Client // REST reads and media downloads
	clientBackoff time.Duration
	// check returns the HTTP status of a local path (Validate's render
	// check); nil skips render checks.
	check     func(ctx context.Context, path string) int
	inflight  *sync.Map   // item ids being processed (single process owns the DB)
	contentMu *sync.Mutex // serializes content slug checks + creates
	rwCache   *sync.Map   // migration:cursor → *rewriter
	limiters  *sync.Map   // source id → *rateLimiter (outbound requests)
	wxr       *wxrCache   // parsed WXR exports
}

// newService resolves the services wpmigrate consumes from d.
func newService(d *kernel.Deps) (*service, error) {
	s := &service{d: d, db: d.DB, log: d.Log, audit: d.Audit, hooks: d.Hooks, settings: d.Settings, cfg: d.Config,
		linker: theme.NewLinker(d), q: d.Jobs, inflight: &sync.Map{}, contentMu: &sync.Mutex{}, rwCache: &sync.Map{}, limiters: &sync.Map{}, wxr: newWXRCache(), clientBackoff: -1,
		http: httpx.NewSafeClientOpts(httpx.SafeClientOptions{AllowPrivate: d.Config.AllowPrivateNet, FollowCrossHost: true,
			Timeout: 5 * time.Minute})}
	var ok bool
	missing := func(name string) error { return fmt.Errorf("wpmigrate: %s service not registered", name) }
	if s.content, ok = kernel.Get[content.Service](d, kernel.ServiceContent); !ok {
		return nil, missing(kernel.ServiceContent)
	}
	if s.tax, ok = kernel.Get[taxonomy.Service](d, kernel.ServiceTaxonomy); !ok {
		return nil, missing(kernel.ServiceTaxonomy)
	}
	if s.media, ok = kernel.Get[media.Service](d, kernel.ServiceMedia); !ok {
		return nil, missing(kernel.ServiceMedia)
	}
	if s.menus, ok = kernel.Get[menus.Service](d, kernel.ServiceMenus); !ok {
		return nil, missing(kernel.ServiceMenus)
	}
	if s.redirects, ok = kernel.Get[redirects.Service](d, kernel.ServiceRedirects); !ok {
		return nil, missing(kernel.ServiceRedirects)
	}
	if s.users, ok = kernel.Get[auth.Service](d, kernel.ServiceAuth); !ok {
		return nil, missing(kernel.ServiceAuth)
	}
	s.check = s.httpCheck
	return s, nil
}

// importer resolves the commerce ProductImporter lazily (an adapter
// registers it at integration; tests may register a fake at any time).
func (s *service) importer() (ProductImporter, bool) {
	return kernel.Get[ProductImporter](s.d, ProductImporterService)
}

// withQueue returns a shallow copy of s that enqueues on q.
func (s *service) withQueue(q enqueuer) *service {
	cp := *s
	cp.q = q
	return &cp
}

// baseURL is the public base URL (kernel.BaseURLOf; services built
// without settings in unit tests fall back to the config).
func (s *service) baseURL(ctx context.Context) string {
	return kernel.BaseURLOf(ctx, s.settings, s.cfg.BaseURL)
}

// httpCheck probes our own server. Media has a GET-only route; closing the
// response without reading avoids downloading entire files.
//
// The probe targets the public base URL (site.base_url, else GORUN_BASE_URL),
// as before. It may resolve to a private address only when that URL has the
// same origin as the operator-configured GORUN_BASE_URL / base_url, which names
// this server, or when GORUN_ALLOW_PRIVATE_NET is set. Otherwise anyone who can
// edit settings and run a migration could aim requests at the internal network.
func (s *service) httpCheck(ctx context.Context, path string) int {
	base, allowPrivate := s.checkTarget(ctx)
	if base == "" {
		return 0
	}
	method := http.MethodHead
	if strings.HasPrefix(path, "/media/") {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
	if err != nil {
		return 0
	}
	c := httpx.NewSafeClientOpts(httpx.SafeClientOptions{AllowPrivate: allowPrivate, Timeout: 15 * time.Second})
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

// checkTarget is the base URL httpCheck probes and whether it may resolve to
// a private address (see httpCheck).
func (s *service) checkTarget(ctx context.Context) (string, bool) {
	base := strings.TrimRight(strings.TrimSpace(s.baseURL(ctx)), "/")
	return base, s.cfg.AllowPrivateNet || sameOrigin(base, s.cfg.BaseURL)
}

// sameOrigin reports whether a and b are absolute http(s) URLs with the same
// scheme and host (including port).
func sameOrigin(a, b string) bool {
	ua, err := url.Parse(strings.TrimSpace(a))
	if err != nil || ua.Host == "" {
		return false
	}
	ub, err := url.Parse(strings.TrimSpace(b))
	if err != nil || ub.Host == "" {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host) &&
		(ua.Scheme == "http" || ua.Scheme == "https")
}

func (s *service) locale(ctx context.Context) string {
	if s.settings == nil {
		return i18n.DefaultLocale
	}
	return s.settings.GetString(ctx, "site.locale", i18n.DefaultLocale)
}
