package httpx

import (
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// untrustedForwardInterval bounds how often the untrusted-forwarding warning
// is logged: the first occurrence is logged immediately, later ones at most
// once per interval with the number of requests seen in between.
const untrustedForwardInterval = time.Hour

// forwardWarner logs, rate-limited, that a request carried forwarding headers
// (X-Forwarded-For, Forwarded, X-Forwarded-Proto) from a peer that is not a
// configured trusted proxy. Those headers are ignored, so behind a reverse
// proxy every visitor shares the proxy's address for rate limits and audit
// until GORUN_TRUSTED_PROXIES lists it.
type forwardWarner struct {
	log      *slog.Logger
	now      func() time.Time
	interval time.Duration

	mu         sync.Mutex
	last       time.Time
	logged     bool
	suppressed int
}

func newForwardWarner(log *slog.Logger) *forwardWarner {
	return &forwardWarner{log: log, now: time.Now, interval: untrustedForwardInterval}
}

// hasForwardingHeaders reports whether r carries a header a reverse proxy
// would set to describe the original client.
func hasForwardingHeaders(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" ||
		r.Header.Get("X-Forwarded-Proto") != ""
}

func (w *forwardWarner) observe(r *http.Request) {
	if !hasForwardingHeaders(r) || trustedPeer(r) {
		return
	}
	now := w.now()
	w.mu.Lock()
	if w.logged && now.Sub(w.last) < w.interval {
		w.suppressed++
		w.mu.Unlock()
		return
	}
	suppressed := w.suppressed
	w.logged, w.last, w.suppressed = true, now, 0
	w.mu.Unlock()
	peer := peerAddress(r)
	w.log.Warn("httpx: forwarding headers from an untrusted peer were ignored; "+
		"if GoRun runs behind a reverse proxy, list it in GORUN_TRUSTED_PROXIES (or trusted_proxies in gorun.toml), "+
		"otherwise every visitor shares the proxy's address for login rate limits and audit",
		"peer", peer.String(), "similar_requests_since_last_warning", suppressed,
		"next_warning_after", w.interval.String())
}

// WarnUntrustedForwarding returns middleware that logs (at most once per hour,
// first occurrence immediately) when forwarding headers arrive from a peer
// outside the trusted proxies. It must run after RealIPWithTrustedProxies,
// which puts the trusted networks in the request context. It never changes
// the request.
func WarnUntrustedForwarding(log *slog.Logger) func(http.Handler) http.Handler {
	w := newForwardWarner(log)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			w.observe(r)
			next.ServeHTTP(rw, r)
		})
	}
}
