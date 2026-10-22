package workagents

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestDesignReviewAPIRejectsAnonymousMalformedAndUnapprovedRequests(t *testing.T) {
	s, ctx := setup(t, nil)
	s.d.API = kernel.NewAPIRegistry()
	h := &handlers{s: s}
	h.designReviewAPI(s.d)
	router := chi.NewRouter()
	for _, op := range s.d.API.Operations() {
		router.Method(op.Method, op.Path, op.Handler)
	}
	rec := apiCall(router, context.Background(), http.MethodGet, "/v1/workagents/design-reviews", "")
	if rec.Code != 401 {
		t.Fatalf("anonymous review access: %d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/v1/workagents/design-reviews", "/v1/workagents/design-reviews/preview"} {
		rec = apiCall(router, ctx, http.MethodPost, path, `{"unexpected":true}`)
		if rec.Code != 422 {
			t.Fatalf("malformed request accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec = apiCall(router, ctx, http.MethodPost, "/v1/workagents/design-reviews", `{"preview_id":"missing","request_key":"a","approved":false}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "explicit approval") {
		t.Fatalf("unapproved enqueue accepted: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := s.d.DB.QueryRow(`SELECT count(*) FROM workagents_design_reviews`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("invalid requests created reviews: %d %v", n, err)
	}
}
