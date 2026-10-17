package auth

import (
	"github.com/zadstack/gorun/internal/httpx"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(CSRFToken(r.Context()))) })
}

func TestCSRFRejectsMissingTokenAcceptsHeaderAndForm(t *testing.T) {
	h := CSRFMiddleware()(okHandler())
	withSession := func(r *http.Request) *http.Request {
		return r.WithContext(withPrincipal(r.Context(), &principal{kind: "user", user: &User{ID: "u", Status: "active"}, csrf: "sess-token"}))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withSession(httptest.NewRequest("POST", "/admin/x", nil)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing token: %d", rec.Code)
	}
	r := withSession(httptest.NewRequest("POST", "/admin/x", nil))
	r.Header.Set("X-CSRF-Token", "sess-token")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("header token: %d", rec.Code)
	}
	r = withSession(httptest.NewRequest("POST", "/admin/x", strings.NewReader(url.Values{"_csrf": {"sess-token"}}.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("form token: %d", rec.Code)
	}
	r = withSession(httptest.NewRequest("POST", "/admin/x", nil))
	r.Header.Set("X-CSRF-Token", "wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", rec.Code)
	}
}

func TestCSRFAnonymousDoubleSubmit(t *testing.T) {
	h := CSRFMiddleware()(okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/login", nil))
	cookie := rec.Result().Cookies()
	token := rec.Body.String()
	if len(cookie) != 1 || cookie[0].Name != "gp_csrf" || token == "" || cookie[0].Value != token || !cookie[0].HttpOnly {
		t.Fatalf("anonymous token cookie not issued: %+v %q", cookie, token)
	}
	r := httptest.NewRequest("POST", "/admin/login", strings.NewReader(url.Values{"_csrf": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("double submit: %d", rec.Code)
	}
	r = httptest.NewRequest("POST", "/admin/login", strings.NewReader(url.Values{"_csrf": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r) // no cookie
	if rec.Code != http.StatusForbidden {
		t.Fatalf("token without cookie: %d", rec.Code)
	}
}

// failReader fails the test if the middleware reads the body.
type failReader struct{ t *testing.T }

func (f failReader) Read([]byte) (int, error) {
	f.t.Error("CSRF check read a multipart body")
	return 0, io.EOF
}

func TestCSRFNeverParsesMultipartWithoutHeader(t *testing.T) {
	h := CSRFMiddleware()(okHandler())
	r := httptest.NewRequest("POST", "/admin/media", failReader{t})
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	r.AddCookie(&http.Cookie{Name: csrfCookie, Value: strings.Repeat("a", 43)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("multipart without header: %d", rec.Code)
	}
}

func TestCSRFCapsURLEncodedBody(t *testing.T) {
	h := CSRFMiddleware()(okHandler())
	token := strings.Repeat("a", 43)
	body := "pad=" + strings.Repeat("x", 2<<20) + "&_csrf=" + token
	r := httptest.NewRequest("POST", "/admin/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("oversized form body must not be parsed: %d", rec.Code)
	}
}

func TestCSRFCookieHTTPSUsesTrustedProxy(t *testing.T) {
	for _, tc := range []struct {
		peer, proto string
		secure      bool
	}{{"198.51.100.1:1234", "https", false}, {"127.0.0.1:1234", "https, http", true}} {
		r := httptest.NewRequest("GET", "/admin/login", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-Proto", tc.proto)
		r = r.WithContext(httpx.WithTrustedProxies(r.Context(), []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}))
		rec := httptest.NewRecorder()
		CSRFMiddleware()(okHandler()).ServeHTTP(rec, r)
		if got := rec.Result().Cookies()[0].Secure; got != tc.secure {
			t.Fatalf("peer %s proto %s secure=%v", tc.peer, tc.proto, got)
		}
	}
}

func TestCSRFCookieUsesConfiguredHTTPSBaseURL(t *testing.T) {
	d := testDeps(newDB(t))
	d.Config.BaseURL = "https://example.com"
	rec := httptest.NewRecorder()
	h := SessionMiddleware(d)(CSRFMiddleware()(okHandler()))
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/login", nil))
	if !rec.Result().Cookies()[0].Secure {
		t.Fatal("configured HTTPS must secure CSRF cookie like session cookie")
	}
}
