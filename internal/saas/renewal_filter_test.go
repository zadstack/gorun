package saas

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/auth"
)

func TestRenewalFilterHTTP(t *testing.T) {
	s, owner, stranger := setup(t)
	dates := map[string]string{"due_soon": "2026-10-17T00:00:00Z", "expired": "2026-10-13T00:00:00Z", "current": "2027-01-01T00:00:00Z", "unscheduled": "", "invalid": "not-a-date"}
	if _, err := s.DB.Exec(`DELETE FROM saas_entitlements`); err != nil {
		t.Fatal(err)
	}
	for state, date := range dates {
		if _, err := s.DB.Exec(`INSERT INTO saas_entitlements(id,public_id,app_id,owner_id,subject_type,external_id,plan_id,status,valid_until,created_at,updated_at) VALUES(?,?,'app','alice','user',?,'pro','suspended',?,'','')`, state, state, state, date); err != nil {
			t.Fatal(err)
		}
	}
	var before int
	if err := s.DB.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{{"", 5}, {"needs_renewal", 2}, {"due_soon", 1}, {"expired", 1}, {"current", 1}, {"unscheduled", 1}, {"invalid", 1}} {
		w := httptest.NewRecorder()
		(&handlers{s}).overview(false)(w, httptest.NewRequest("GET", "/api/v1/saas?renewal_state="+tc.query, nil).WithContext(owner))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal(w.Code, w.Body.String())
		}
		var response struct {
			Data Overview `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data.Entitlements) != tc.want || len(response.Data.Apps) != 1 {
			t.Fatalf("%s: %s", tc.query, w.Body.String())
		}
		for _, ent := range response.Data.Entitlements {
			if ent.Status != "suspended" || (tc.query != "" && tc.query != "needs_renewal" && ent.Renewal.State != tc.query) || (tc.query == "needs_renewal" && ent.Renewal.State != "due_soon" && ent.Renewal.State != "expired") {
				t.Fatalf("wrong filter/state: %+v", ent)
			}
		}
	}
	for _, query := range []string{"renewal_state=bogus", "renewal_state=expired&renewal_state=current"} {
		w := httptest.NewRecorder()
		(&handlers{s}).overview(false)(w, httptest.NewRequest("GET", "/api/v1/saas?"+query, nil).WithContext(owner))
		if w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		ctx    context.Context
		manage bool
		code   int
	}{{stranger, false, 200}, {owner, true, 403}, {context.Background(), false, 403}, {auth.WithUser(owner, &auth.User{ID: "alice", Status: "disabled"}), false, 403}} {
		w := httptest.NewRecorder()
		(&handlers{s}).overview(tc.manage)(w, httptest.NewRequest("GET", "/api/v1/saas?renewal_state=needs_renewal", nil).WithContext(tc.ctx))
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.code == 200 && (strings.Contains(w.Body.String(), "Private app") || !strings.Contains(w.Body.String(), `"entitlements":[]`)) {
			t.Fatal("cross-owner leak", w.Body.String())
		}
	}
	var after int
	if err := s.DB.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("read modified database", before, after, err)
	}
}

func TestRenewalFilterRelatedUsage(t *testing.T) {
	source := Overview{Apps: []App{{ID: "a"}, {ID: "b"}}, Entitlements: []Entitlement{{AppID: "a", ExternalID: "due", Renewal: Renewal{State: "due_soon"}}, {AppID: "a", ExternalID: "later", Renewal: Renewal{State: "current"}}}, Usage: []Usage{{AppID: "a", SubjectID: "due"}, {AppID: "a", SubjectID: "later"}, {AppID: "b", SubjectID: "due"}}}
	got := filterOverviewRenewal(source, "needs_renewal")
	if len(got.Apps) != 1 || got.Apps[0].ID != "a" || len(got.Usage) != 1 || got.Usage[0].SubjectID != "due" {
		t.Fatalf("unrelated records: %+v", got)
	}
	empty := filterOverviewRenewal(source, "expired")
	if len(empty.Apps) != 0 || len(empty.Entitlements) != 0 || len(empty.Usage) != 0 {
		t.Fatal(empty)
	}
}

func TestRenewalFilterAPIContractAndAuthentication(t *testing.T) {
	app := apitest.Boot(t, New())
	client := apitest.NewClient(t, app)
	for _, route := range []string{"/api/v1/saas", "/api/v1/saas/manage"} {
		if w := client.Anonymous().Do("GET", route+"?renewal_state=needs_renewal", nil, nil); w.Code != 401 {
			t.Fatal(w.Code, w.Body.String())
		}
		if w := client.Do("GET", route+"?renewal_state=wrong", nil, nil); w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := client.Anonymous().Do("GET", "/api/v1/openapi.json", nil, nil)
	var spec struct {
		Paths map[string]map[string]struct {
			Parameters []struct{ Name, In, Description string }
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	found := 0
	for path, methods := range spec.Paths {
		if strings.HasSuffix(path, "/v1/saas") || strings.HasSuffix(path, "/v1/saas/manage") {
			for _, p := range methods["get"].Parameters {
				if p.Name == "renewal_state" && p.In == "query" && strings.Contains(p.Description, "needs_renewal") {
					found++
				}
			}
		}
	}
	if found != 2 {
		t.Fatalf("missing query docs on both routes: %d", found)
	}
}
