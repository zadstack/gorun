package wpmigrate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestAuthenticatedCollectionNeverFallsBack(t *testing.T) {
	view := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/users/me") {
			w.Write([]byte(`{"id":1}`))
			return
		}
		if r.URL.Query().Get("context") != "edit" {
			view++
			w.Header().Set("X-WP-TotalPages", "1")
			w.Write([]byte(`[{"id":1}]`))
			return
		}
		w.WriteHeader(403)
		w.Write([]byte(`{"code":"rest_forbidden"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "fixture", "secret", srv.Client())
	err := newRESTSource(c).all(context.Background(), "/wp/v2/posts", nil, func(json.RawMessage) error { return nil })
	if err == nil || view != 0 {
		t.Fatalf("denied authenticated collection fell back: err=%v view=%d", err, view)
	}
}

func TestPaginationWalksPermissionFilteredEmptyPage(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-WP-Total", "112")
		w.Header().Set("X-WP-TotalPages", "2")
		if r.URL.Query().Get("page") == "2" {
			w.Write([]byte(`[{"id":1}]`))
		} else {
			w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "", "", srv.Client())
	visible := 0
	total, err := c.All(context.Background(), "/wp/v2/posts", nil, 0, func(json.RawMessage) error { visible++; return nil })
	if err != nil || total != 112 || calls != 2 || visible != 1 {
		t.Fatalf("permission filtered paging total=%d visible=%d calls=%d err=%v", total, visible, calls, err)
	}
}

func TestUnboundedCollectionRejectsUnprovenCompletion(t *testing.T) {
	for _, tt := range []struct {
		name  string
		pages string
		total string
	}{{"missing_pages", "", "2"}, {"hard_bound", "501", "501"}} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.pages != "" {
					w.Header().Set("X-WP-TotalPages", tt.pages)
				}
				w.Header().Set("X-WP-Total", tt.total)
				w.Write([]byte(`[{"id":1}]`))
			}))
			defer srv.Close()
			c, _ := NewClient(srv.URL, "", "", srv.Client())
			if _, err := c.All(context.Background(), "/wp/v2/posts", nil, 0, func(json.RawMessage) error { return nil }); err == nil {
				t.Fatal("unproven full collection succeeded")
			}
			if _, err := c.All(context.Background(), "/wp/v2/posts", nil, 1, func(json.RawMessage) error { return nil }); err != nil {
				t.Fatalf("intentional bounded preview failed: %v", err)
			}
		})
	}
}

func TestCountWithoutHeaderIsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[{"id":1}]`)) }))
	defer srv.Close()
	c, _ := NewClient(srv.URL, "", "", srv.Client())
	if _, err := c.Count(context.Background(), "/wp/v2/posts", nil); err == nil {
		t.Fatal("one sampled row reported as known collection count")
	}
}

func TestAuthenticatedContentStatePolicyAllTypes(t *testing.T) {
	for _, typ := range []string{"post", "page", "fixture_note"} {
		t.Run(typ, func(t *testing.T) {
			seen := url.Values{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/wp-json/wp/v2/users/me":
					w.Write([]byte(`{"id":1}`))
				case "/wp-json/wp/v2/types":
					json.NewEncoder(w).Encode(map[string]any{typ: map[string]any{"rest_base": "items", "slug": typ}})
				case "/wp-json/wp/v2/taxonomies":
					w.Write([]byte(`{}`))
				case "/wp-json/":
					w.Write([]byte(`{"namespaces":["wp/v2"]}`))
				case "/wp-json/wp/v2/items":
					seen = r.URL.Query()
					w.Header().Set("X-WP-TotalPages", "1")
					w.Header().Set("X-WP-Total", strconv.Itoa(1))
					w.Write([]byte(`[{"id":1,"status":"draft"}]`))
				default:
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			c, _ := NewClient(srv.URL, "fixture", "secret", srv.Client())
			_, err := newRESTSource(c).Content(context.Background(), []string{typ})
			if err != nil {
				t.Fatal(err)
			}
			if seen.Get("context") != "edit" || seen.Get("status") != "publish,draft,pending,future,private" {
				t.Fatalf("state policy missing: %v", seen)
			}
		})
	}
}
