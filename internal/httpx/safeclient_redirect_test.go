package httpx

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBlockedSpecialRanges(t *testing.T) {
	for _, s := range []string{"64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::1", "2002::1", "192.0.0.8", "198.18.0.1", "198.19.255.254", "240.0.0.1", "255.255.255.254"} {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"192.0.1.1", "198.20.0.1", "223.255.255.1", "64:ff9c::1", "2003::1"} {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func redirectReq(t *testing.T, raw string) *http.Request {
	t.Helper()
	r, err := http.NewRequest("POST", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSafeClientRefusesHTTPSDowngradeRedirect(t *testing.T) {
	c := NewSafeClientOpts(SafeClientOptions{FollowCrossHost: true})
	via := []*http.Request{redirectReq(t, "https://hooks.example.com/in")}
	if err := c.CheckRedirect(redirectReq(t, "http://hooks.example.com/in"), via); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("https→http redirect must be refused, got %v", err)
	}
	if err := c.CheckRedirect(redirectReq(t, "https://hooks.example.com/other"), via); err != nil {
		t.Fatalf("https→https redirect: %v", err)
	}
	plain := []*http.Request{redirectReq(t, "http://hooks.example.com/in")}
	if err := c.CheckRedirect(redirectReq(t, "http://hooks.example.com/other"), plain); err != nil {
		t.Fatalf("http→http redirect: %v", err)
	}
	// A downgrade later in the chain counts too.
	chain := []*http.Request{redirectReq(t, "https://hooks.example.com/a"), redirectReq(t, "https://hooks.example.com/b")}
	if err := c.CheckRedirect(redirectReq(t, "http://hooks.example.com/c"), chain); err == nil {
		t.Fatal("a downgrade on a later hop must be refused")
	}
}

func TestSafeClientNoRedirects(t *testing.T) {
	var landed atomic.Int32
	dst := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { landed.Add(1) }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dst.URL+"/landed", http.StatusTemporaryRedirect)
	}))
	defer src.Close()
	c := NewSafeClientOpts(SafeClientOptions{AllowPrivate: true, NoRedirects: true})
	_, err := c.Post(src.URL, "application/json", strings.NewReader(`{}`))
	if !errors.Is(err, ErrRedirectRefused) {
		t.Fatalf("a redirect must fail with ErrRedirectRefused, got %v", err)
	}
	if landed.Load() != 0 {
		t.Fatal("nothing may be sent to the redirect target")
	}
}
