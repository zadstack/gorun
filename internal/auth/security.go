package auth

import (
	"context"
	"database/sql"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

// Suppress repeated audit rows without changing existing credential throttling.
type securitySamples struct {
	mu     sync.Mutex
	minute int64
	total  int
	hits   map[string]int
}

func (s *securitySamples) allow(ip string, perIP, total int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	minute := time.Now().Unix() / 60
	if minute != s.minute {
		s.minute = minute
		s.total = 0
		s.hits = map[string]int{}
	}
	if s.hits == nil {
		s.hits = map[string]int{}
	}
	if s.total >= total || s.hits[ip] >= perIP || len(s.hits) >= 4096 {
		return false
	}
	s.total++
	s.hits[ip]++
	return true
}
func attemptedIdentity(email string) string {
	var out strings.Builder
	for _, r := range normalizeEmail(email) {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if out.Len()+len(string(r)) > 254 {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}
func (s *service) failedLogin(ctx context.Context, id, email, reason string) {
	if !s.securityAudit.allow(audit.IPFromContext(ctx), 30, 120) {
		return
	}
	s.record(ctx, "user.login_failed", id, map[string]string{"email": attemptedIdentity(email), "reason": reason, "ip_source": httpx.ClientIPSource(ctx)})
}
func publicBlockIP(raw string) (netip.Addr, bool) {
	ip, e := netip.ParseAddr(raw)
	if e != nil || ip.Zone() != "" {
		return netip.Addr{}, false
	}
	ip = ip.Unmap()
	return ip, ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}
func blockableIP(r *http.Request, raw string) bool {
	ip, ok := publicBlockIP(raw)
	return ok && ip.String() != httpx.ClientIP(r) && !httpx.TrustedProxyAddress(r, ip)
}
func (h *handlers) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin/security/") {
			r.Body = http.MaxBytesReader(w, r.Body, 16384)
		}
		ip := httpx.ClientIP(r)
		var expires string
		e := h.deps.DB.QueryRowContext(r.Context(), `SELECT expires_at FROM auth_ip_blocks WHERE ip=? AND expires_at>?`, ip, db.Now()).Scan(&expires)
		if e == sql.ErrNoRows {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if e != nil {
			h.deps.Log.Error("security: IP block lookup failed")
			http.Error(w, "Service temporarily unavailable", 503)
			return
		}
		if h.blockedAudit.allow(ip, 1, 60) {
			h.deps.Audit.Record(audit.WithIP(r.Context(), ip), "security.ip_blocked", "ip", ip, map[string]string{"reason": "temporary_block", "ip_source": httpx.ClientIPSource(r.Context())})
		}
		http.Error(w, "Access temporarily restricted / الوصول مقيد مؤقتًا", 403)
	})
}
