package wpmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func TestSameSecondContentAndStateChangeUpdatesMappedID(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(true, Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatal(st.Status)
	}
	before := e.item(id, "content", "post", "1154")
	count := e.count("SELECT count(*) FROM content")
	e.wp.override("/wp-json/wp/v2/posts", func(b []byte) []byte {
		var list []map[string]any
		json.Unmarshal(b, &list)
		for _, p := range list {
			if p["id"].(float64) == 1154 {
				p["status"] = "draft"
				p["title"] = map[string]any{"rendered": "Same-second source edit"}
				p["content"] = map[string]any{"rendered": "<p>Same-second body edit</p>"}
			}
		}
		out, _ := json.Marshal(list)
		return out
	})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatal(st.Status)
	}
	after := e.item(id, "content", "post", "1154")
	item, err := e.svc.content.Get(e.ctx(), after.LocalID)
	if err != nil || after.LocalID != before.LocalID || item.Title != "Same-second source edit" || item.Status != content.Status("draft") || e.count("SELECT count(*) FROM content") != count {
		t.Fatalf("same-second edit lost or duplicated: %+v err=%v", item, err)
	}
}

func TestPreviewIncludesNativeSourceStatesAndDates(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(false, Options{})
	before := e.count("SELECT count(*) FROM content")
	mediaBefore := e.count("SELECT count(*) FROM media")
	result, err := e.svc.Preview(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ContentStates) == 0 {
		t.Fatal("preview state plan empty")
	}
	for _, p := range result.ContentStates {
		if p.WPID == "" || p.Type == "" || p.SourceStatus == "" || p.LocalStatus == "" || p.PublishAt == "" {
			t.Fatalf("incomplete state plan: %+v", p)
		}
	}
	if e.count("SELECT count(*) FROM content") != before || e.count("SELECT count(*) FROM media") != mediaBefore {
		t.Fatal("state preview wrote content/media")
	}
}

func TestRevocationHasTypedCauseAndCannotBeUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"code":"incorrect_password"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "fixture", "secret", srv.Client())
	_, err := c.Get(context.Background(), "/wp/v2/users", nil)
	if !errors.Is(err, ErrAuthFailed) || isForbidden(err) {
		t.Fatalf("revocation cause lost: %v", err)
	}
	s := newRESTSource(c)
	s.authOnce.Do(func() { s.authOK = true })
	if _, err := s.Users(context.Background()); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("users wrapper lost revocation: %v", err)
	}
	e := newEnv(t)
	if got := e.svc.unavailable(e.ctx(), &migrationRow{}, "media", err); !errors.Is(got, ErrAuthFailed) {
		t.Fatalf("unavailable swallowed revoked credentials: %v", got)
	}
}

func TestWordPressStatusValidatorDenialRechecksAuthentication(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(fmt.Sprint(revoked), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/users/me") {
					if revoked {
						w.WriteHeader(401)
						w.Write([]byte(`{"code":"incorrect_password"}`))
					} else {
						w.Write([]byte(`{"id":1}`))
					}
					return
				}
				w.WriteHeader(400)
				w.Write([]byte(`{"code":"rest_invalid_param","data":{"details":{"status":{"code":"rest_forbidden_status","data":{"status":403}}}}}`))
			}))
			defer srv.Close()
			c, _ := NewClient(srv.URL, "fixture", "secret", srv.Client())
			_, err := c.Get(context.Background(), "/wp/v2/posts", contentQuery(true))
			if errors.Is(err, ErrAuthFailed) != revoked {
				t.Fatalf("status validator denied collection: revoked=%v err=%v", revoked, err)
			}
			if err == nil {
				t.Fatal("denial disappeared")
			}
		})
	}
}

func TestProfileTransportAndCancellationKeepOriginalCause(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"code":"fixture_unavailable"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "fixture", "secret", srv.Client())
	c.retries = 1
	s := newRESTSource(c)
	s.authed(context.Background())
	var se *StatusError
	if errors.Is(s.authErr, ErrAuthFailed) || !errors.As(s.authErr, &se) || se.Status != 500 {
		t.Fatalf("transport mislabeled as auth: %v", s.authErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s = newRESTSource(c)
	s.authed(ctx)
	if !errors.Is(s.authErr, context.Canceled) || errors.Is(s.authErr, ErrAuthFailed) {
		t.Fatalf("cancellation cause lost: %v", s.authErr)
	}
}
