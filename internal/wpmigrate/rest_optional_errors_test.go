package wpmigrate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func optionalErrorFixture(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/wp-json/wp/v2/users/me":
		w.Write([]byte(`{"id":1}`))
	case "/wp-json/":
		w.Write([]byte(`{"namespaces":["wp/v2"]}`))
	case "/wp-json/wp/v2/types":
		w.Write([]byte(`{"post":{"rest_base":"posts","slug":"post"}}`))
	case "/wp-json/wp/v2/taxonomies":
		w.Write([]byte(`{}`))
	case "/wp-json/wp/v2/posts":
		w.Header().Set("X-WP-TotalPages", "1")
		w.Header().Set("X-WP-Total", "1")
		w.Write([]byte(`[{"id":1,"status":"draft"}]`))
	default:
		w.WriteHeader(404)
	}
}

func TestContentPropagatesSiteAuthenticationFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/settings") {
			w.WriteHeader(401)
			w.Write([]byte(`{"code":"incorrect_password"}`))
			return
		}
		optionalErrorFixture(w, r)
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "fixture", "secret", srv.Client())
	items, err := newRESTSource(c).Content(context.Background(), []string{"post"})
	if !errors.Is(err, ErrAuthFailed) || len(items) != 0 {
		t.Fatalf("site authentication failure lost: %v items=%d", err, len(items))
	}
}

func TestDiscoveryPropagatesMediaAuthenticationFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/media") {
			w.WriteHeader(401)
			w.Write([]byte(`{"code":"incorrect_password"}`))
			return
		}
		optionalErrorFixture(w, r)
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "fixture", "secret", srv.Client())
	rep, err := discoverREST(context.Background(), c)
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("media authentication failure lost after later successful profile: %v", err)
	}
	if rep == nil || rep.Auth.OK || rep.Auth.Error != "auth_failed" {
		t.Fatalf("failed discovery retained valid auth: %+v", rep)
	}
}

func TestSelectedMissingContentTypeFailsInsteadOfCompleting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(optionalErrorFixture))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "", "", srv.Client())
	list, err := newRESTSource(c).Content(context.Background(), []string{"fixture_note"})
	if !errors.Is(err, ErrUnavailable) || len(list) != 0 {
		t.Fatalf("selected CPT silently omitted: %v items=%d", err, len(list))
	}
}

func TestDeniedProfileIsUnsupportedAndNeverSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"code":"rest_forbidden"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "fixture", "secret", srv.Client())
	s := newRESTSource(c)
	_, err := s.Users(context.Background())
	if !errors.Is(err, ErrAuthUnavailable) || errors.Is(err, ErrAuthFailed) || isForbidden(err) {
		t.Fatalf("profile denial lost/mislabeled: %v", err)
	}
	e := newEnv(t)
	if got := e.svc.unavailable(e.ctx(), &migrationRow{}, "users", err); !errors.Is(got, ErrAuthUnavailable) {
		t.Fatalf("unsupported authenticated profile skipped: %v", got)
	}
	rep, err := discoverREST(context.Background(), c)
	if !errors.Is(err, ErrAuthUnavailable) || rep == nil || rep.Auth.OK || rep.Auth.Error != "auth_unavailable" {
		t.Fatalf("profile denied discovery: %+v %v", rep, err)
	}
}

func TestSitePropagatesPermissionRecheckTransportFailure(t *testing.T) {
	checks := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/users/me") {
			checks++
			if checks > 1 {
				w.WriteHeader(500)
				w.Write([]byte(`{"code":"fixture_unavailable"}`))
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, "/settings") {
			w.WriteHeader(403)
			w.Write([]byte(`{"code":"rest_forbidden"}`))
			return
		}
		optionalErrorFixture(w, r)
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "fixture", "secret", srv.Client())
	c.retries = 1
	_, err := newRESTSource(c).Site(context.Background())
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 500 || errors.Is(err, ErrAuthFailed) || checks != 2 {
		t.Fatalf("profile transport error lost/mislabeled: %v checks=%d", err, checks)
	}
}
