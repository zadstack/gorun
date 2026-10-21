package admin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateEdgeOriginRejectsPrivateAndUnqualifiedHosts(t *testing.T) {
	for _, raw := range []string{
		"http://edge.example",
		"https://user:secret@edge.example",
		"https://edge.example/admin",
		"https://edge.example?x=1",
		"https://localhost",
		"https://127.0.0.1",
		"https://10.1.2.3",
		"https://192.168.1.9",
		"https://169.254.169.254",
		"https://edge.local",
		"https://metadata.internal",
		"edge.example",
		"https://example",
	} {
		if _, err := validateEdgeOrigin(raw); err == nil {
			t.Fatalf("%q was accepted", raw)
		}
	}
	got, err := validateEdgeOrigin("https://Edge.Example/")
	if err != nil || got != "https://edge.example" {
		t.Fatalf("normalized origin: %q %v", got, err)
	}
}

func TestEdgeWhoamiAcceptsPrimaryAndRejectsRedirects(t *testing.T) {
	const key = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/manager" || r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"data":{"site_id":"demo","manager_id":"mgr-1","label":"GoRun","primary":true}}`)
	}))
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	info, err := edgeWhoami(context.Background(), client, srv.URL, key)
	if err != nil || info.SiteID != "demo" || info.ManagerID != "mgr-1" || info.Label != "GoRun" {
		t.Fatalf("whoami: %+v %v", info, err)
	}
	if _, err := edgeWhoami(context.Background(), client, srv.URL, key+"nope"); err == nil {
		t.Fatal("wrong key was accepted")
	}
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/secret", http.StatusFound)
	}))
	t.Cleanup(redirected.Close)
	other := redirected.Client()
	other.CheckRedirect = client.CheckRedirect
	if _, err := edgeWhoami(context.Background(), other, redirected.URL, key); err == nil {
		t.Fatal("redirect was accepted")
	}
	if strings.Contains(redirected.URL, key) {
		t.Fatal("test leaked the key into the origin")
	}
}
