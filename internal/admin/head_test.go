package admin_test

import (
	"net/http"
	"testing"
)

// HEAD is answered like GET (status and headers, no body) on every GET
// route: uptime probes and link checkers use it.
func TestHeadAnswersGetRoutes(t *testing.T) {
	s := boot(t)
	for path, want := range map[string]int{"/admin/setup": http.StatusOK, "/healthz": http.StatusOK, "/readyz": http.StatusOK,
		"/admin/login": http.StatusSeeOther, "/admin/content": http.StatusSeeOther, "/admin/no-such-page": http.StatusSeeOther} {
		get, _ := s.do("GET", path, nil)
		head, headBody := s.do("HEAD", path, nil)
		if head.StatusCode != want || get.StatusCode != want || headBody != "" {
			t.Errorf("%s: HEAD %d GET %d (want %d), HEAD body %d bytes", path, head.StatusCode, get.StatusCode, want, len(headBody))
		}
		if head.Header.Get("Content-Type") != get.Header.Get("Content-Type") || head.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: HEAD headers differ from GET: %v", path, head.Header)
		}
	}
	if resp, _ := s.do("HEAD", "/admin/logout", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST-only routes still refuse HEAD: %d", resp.StatusCode)
	}
}
