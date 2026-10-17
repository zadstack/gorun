package httpx

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSafeClientBlocksPrivateUnlessAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("hi")) }))
	defer srv.Close()

	_, err := NewSafeClient(false).Get(srv.URL) // 127.0.0.1
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("loopback should be blocked, got %v", err)
	}
	_, err = NewSafeClient(false).Get("http://10.0.0.1:9/")
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("10.0.0.1 should be blocked, got %v", err)
	}
	resp, err := NewSafeClient(true).Get(srv.URL)
	if err != nil {
		t.Fatalf("allowed client: %v", err)
	}
	resp.Body.Close()
}

func TestBlockedIPRanges(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "172.16.5.4", "192.168.1.1", "169.254.169.254", "0.0.0.0", "100.64.0.1", "::1", "fe80::1", "fd00::1", "224.0.0.1"}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "2606:4700::1111", "8.8.8.8"} {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestSafeClientRefusesCrossHostRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/same" {
			w.Write([]byte("ok"))
			return
		}
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "/same", http.StatusFound)
			return
		}
		http.Redirect(w, r, "http://example.com/", http.StatusFound)
	}))
	defer srv.Close()
	c := NewSafeClient(true)
	resp, err := c.Get(srv.URL + "/hop")
	if err != nil {
		t.Fatalf("same-host redirect should work: %v", err)
	}
	resp.Body.Close()
	if _, err := c.Get(srv.URL + "/away"); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("cross-host redirect should fail, got %v", err)
	}
}

// redirectPair starts dst (answers "dst") and src (redirects every request
// to dst, a different host:port).
func redirectPair(t *testing.T) (src, dst *httptest.Server) {
	t.Helper()
	dst = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("dst")) }))
	src = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dst.URL+"/landed", http.StatusFound)
	}))
	t.Cleanup(func() { src.Close(); dst.Close() })
	return src, dst
}

func TestSafeClientOptsCrossHostModes(t *testing.T) {
	src, _ := redirectPair(t)
	same := NewSafeClientOpts(SafeClientOptions{AllowPrivate: true})
	if _, err := same.Get(src.URL); err == nil || !strings.Contains(err.Error(), "another host") {
		t.Fatalf("default options must refuse cross-host redirects, got %v", err)
	}
	cross := NewSafeClientOpts(SafeClientOptions{AllowPrivate: true, FollowCrossHost: true})
	resp, err := cross.Get(src.URL)
	if err != nil {
		t.Fatalf("FollowCrossHost: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "dst" || resp.Request.URL.Path != "/landed" {
		t.Fatalf("got %q at %s", b, resp.Request.URL)
	}
	// FollowCrossHost does not disable the private-network check.
	if _, err := NewSafeClientOpts(SafeClientOptions{FollowCrossHost: true}).Get(src.URL); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("loopback must stay blocked with FollowCrossHost, got %v", err)
	}
}

func TestSafeClientCrossHostRedirectHopIsDialChecked(t *testing.T) {
	src, dst := redirectPair(t)
	blockedAddr := dst.Listener.Addr().String()
	var mu sync.Mutex
	var dialed []string
	guard := func(address string) error {
		mu.Lock()
		dialed = append(dialed, address)
		mu.Unlock()
		if address == blockedAddr {
			return fmt.Errorf("httpx: connection to %s blocked (private network)", address)
		}
		return nil
	}
	c := newSafeClient(SafeClientOptions{FollowCrossHost: true}, guard)
	_, err := c.Get(src.URL)
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("redirect hop to a blocked address must fail, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != 2 || dialed[0] != src.Listener.Addr().String() || dialed[1] != blockedAddr {
		t.Fatalf("guard must run on the first dial and on the redirect hop: %v", dialed)
	}
}

func TestSafeClientOptsDefaultsAndLimits(t *testing.T) {
	c := NewSafeClientOpts(SafeClientOptions{})
	if c.Timeout != safeClientTimeout {
		t.Fatalf("default timeout %v", c.Timeout)
	}
	if c := NewSafeClientOpts(SafeClientOptions{Timeout: time.Second}); c.Timeout != time.Second {
		t.Fatalf("timeout %v", c.Timeout)
	}
	var hops atomic.Int32
	var loop *httptest.Server
	loop = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		http.Redirect(w, r, loop.URL+"/again", http.StatusFound)
	}))
	defer loop.Close()
	_, err := NewSafeClientOpts(SafeClientOptions{AllowPrivate: true, MaxRedirects: 2}).Get(loop.URL)
	if err == nil || !strings.Contains(err.Error(), "too many redirects") || hops.Load() != 2 {
		t.Fatalf("MaxRedirects=2: hops=%d err=%v", hops.Load(), err)
	}
	ftp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.com/x", http.StatusFound)
	}))
	defer ftp.Close()
	if _, err := NewSafeClientOpts(SafeClientOptions{AllowPrivate: true, FollowCrossHost: true}).Get(ftp.URL); err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("non-http redirect must be refused, got %v", err)
	}
}
