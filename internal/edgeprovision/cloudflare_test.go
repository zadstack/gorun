package edgeprovision

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testRequest = Request{Label: "Test", Worker: "edge-site-abcdef123456", SiteID: "test-site", Title: "اختبار", Locale: "ar", AccountID: strings.Repeat("a", 32)}
var testResources = Resources{Worker: "edge-site-abcdef123456", D1Name: "edge-db-abcdef123456", R2Bucket: "edge-media-abcdef123456", D1ID: "12345678-1234-1234-1234-123456789abc"}

func fixture(t *testing.T, override func(http.ResponseWriter, *http.Request) bool) (*concreteCloudflareAdapter, *[]string) {
	t.Helper()
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer fake-test-token" {
			t.Error("missing server credential")
		}
		if override != nil && override(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := any(map[string]any{})
		switch {
		case strings.HasSuffix(r.URL.Path, "/workers/subdomain"):
			result = map[string]string{"subdomain": "example-account"}
		case strings.HasSuffix(r.URL.Path, "/workers/scripts"):
			result = []any{}
		case strings.HasSuffix(r.URL.Path, "/d1/database") && r.Method == "GET":
			result = []any{}
		case strings.HasSuffix(r.URL.Path, "/d1/database"):
			result = map[string]string{"uuid": testResources.D1ID}
		case strings.HasSuffix(r.URL.Path, "/r2/buckets") && r.Method == "GET":
			result = map[string]any{"buckets": []any{}}
		case strings.HasSuffix(r.URL.Path, "/r2/buckets"):
			result = map[string]string{"name": testResources.R2Bucket}
		case strings.HasSuffix(r.URL.Path, "/query"):
			result = []any{map[string]bool{"success": true}}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	a := NewAdapter().(*concreteCloudflareAdapter)
	a.base = srv.URL
	return a, &calls
}
func TestProvisionStagesUseOnlyApprovedBindingsAndPrivateResources(t *testing.T) {
	sql := []string{}
	uploads := 0
	a, calls := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/query") {
			var v struct {
				SQL    string `json:"sql"`
				Params []any  `json:"params"`
			}
			json.NewDecoder(r.Body).Decode(&v)
			sql = append(sql, v.SQL)
			if strings.Contains(v.SQL, "INSERT INTO edge_managers") {
				if len(v.Params) != 6 || v.Params[3] != strings.Repeat("a", 64) {
					t.Errorf("manager seed %#v", v.Params)
				}
			}
			return false
		}
		if r.Method == "PUT" {
			uploads++
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				t.Fatal(err)
			}
			var meta struct {
				Main     string              `json:"main_module"`
				Date     string              `json:"compatibility_date"`
				Bindings []map[string]string `json:"bindings"`
			}
			if err := json.Unmarshal([]byte(r.FormValue("metadata")), &meta); err != nil {
				t.Fatal(err)
			}
			if meta.Main != "index.js" || meta.Date != "2026-10-01" || len(meta.Bindings) != 3 {
				t.Fatalf("metadata %#v", meta)
			}
			if meta.Bindings[0]["id"] != testResources.D1ID || meta.Bindings[1]["bucket_name"] != testResources.R2Bucket || meta.Bindings[2]["text"] != testRequest.SiteID {
				t.Fatal("unbound resources")
			}
			if len(r.MultipartForm.File) != 3 {
				t.Fatal("expected three JS modules")
			}
			for name := range r.MultipartForm.File {
				if !strings.HasSuffix(name, ".js") {
					t.Fatal("non-module uploaded")
				}
			}
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/r2/buckets") {
			var v map[string]any
			json.NewDecoder(r.Body).Decode(&v)
			if len(v) != 1 || v["name"] != testResources.R2Bucket {
				t.Fatal("bucket must remain private")
			}
		}
		return false
	})
	c := context.Background()
	i, e := a.Inspect(c, "fake-test-token", testRequest, testResources)
	if e != nil || !i.WorkerAvailable || i.WritePermissionsVerified {
		t.Fatalf("inspection %#v %v", i, e)
	}
	if _, e = a.CreateDB(c, "fake-test-token", testRequest, testResources); e != nil {
		t.Fatal(e)
	}
	if _, e = a.CreateBucket(c, "fake-test-token", testRequest, testResources); e != nil {
		t.Fatal(e)
	}
	if e = a.ApplySchema(c, "fake-test-token", testRequest, testResources, Seed{ManagerID: "manager-test", ManagerKeyHash: strings.Repeat("a", 64), ManagerKeyHint: "xyz1"}); e != nil {
		t.Fatal(e)
	}
	if len(sql) != 5 {
		t.Fatalf("expected three migrations and two seed queries: %d", len(sql))
	}
	for _, s := range sql {
		if strings.Contains(s, "INSERT INTO edge_owners") || strings.Contains(s, "INSERT INTO edge_content") {
			t.Fatal("owner/demo seeding forbidden")
		}
	}
	if e = a.UploadWorker(c, "fake-test-token", testRequest, testResources, ArtifactSHA()); e != nil {
		t.Fatal(e)
	}
	origin, e := a.EnableWorker(c, "fake-test-token", testRequest, testResources)
	if e != nil || origin != "https://edge-site-abcdef123456.example-account.workers.dev" || uploads != 1 {
		t.Fatalf("enable %s %v", origin, e)
	}
	for _, call := range *calls {
		if strings.Contains(call, "custom_domains") || strings.Contains(call, "dns") || strings.Contains(call, "public") {
			t.Fatal("unexpected external side effect")
		}
	}
}
func TestWrongArtifactAndInvalidRequestMakeNoCalls(t *testing.T) {
	a, calls := fixture(t, nil)
	if a.UploadWorker(context.Background(), "fake-test-token", testRequest, testResources, "wrong") == nil {
		t.Fatal("digest accepted")
	}
	q := testRequest
	q.AccountID = "../other"
	if _, e := a.Inspect(context.Background(), "fake-test-token", q, testResources); e == nil {
		t.Fatal("path accepted")
	}
	if len(*calls) != 0 {
		t.Fatal("validation must precede requests")
	}
}
func TestProviderErrorsNeverExposeResponseOrToken(t *testing.T) {
	a, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":10000,"message":"fake-test-token secret@example.org"}]}`)
		return true
	})
	_, e := a.Inspect(context.Background(), "fake-test-token", testRequest, testResources)
	if e == nil || strings.Contains(e.Error(), "secret") || strings.Contains(e.Error(), "fake-test-token") || !strings.Contains(e.Error(), "403") {
		t.Fatalf("unsafe error %v", e)
	}
}
func TestCollisionNeverMutates(t *testing.T) {
	a, calls := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/workers/scripts") {
			fmt.Fprintf(w, `{"success":true,"result":[{"id":%q}]}`, testResources.Worker)
			return true
		}
		return false
	})
	if _, e := a.CreateDB(context.Background(), "fake-test-token", testRequest, testResources); e == nil {
		t.Fatal("collision accepted")
	}
	for _, c := range *calls {
		if !strings.HasPrefix(c, "GET ") {
			t.Fatal("collision mutated resource")
		}
	}
}
func TestIncompleteD1AndLoopingR2FailClosed(t *testing.T) {
	for _, kind := range []string{"d1", "r2"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if kind == "d1" && strings.HasSuffix(r.URL.Path, "/d1/database") {
					fmt.Fprint(w, `{"success":true,"result":[],"result_info":{"total_pages":21}}`)
					return true
				}
				if kind == "r2" && strings.HasSuffix(r.URL.Path, "/r2/buckets") {
					fmt.Fprint(w, `{"success":true,"result":{"buckets":[],"cursor":"loop"}}`)
					return true
				}
				return false
			})
			if _, e := a.Inspect(context.Background(), "fake-test-token", testRequest, testResources); e == nil {
				t.Fatal("incomplete inventory accepted")
			}
		})
	}
}
func TestD1AndR2TraverseToCollision(t *testing.T) {
	a, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/d1/database") {
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprint(w, `{"success":true,"result":[],"result_info":{"total_pages":2}}`)
			} else {
				fmt.Fprintf(w, `{"success":true,"result":[{"name":%q}],"result_info":{"total_pages":2}}`, testResources.D1Name)
			}
			return true
		}
		if strings.HasSuffix(r.URL.Path, "/r2/buckets") {
			if r.URL.Query().Get("cursor") == "" {
				fmt.Fprint(w, `{"success":true,"result":{"buckets":[],"cursor":"next"}}`)
			} else {
				fmt.Fprintf(w, `{"success":true,"result":{"buckets":[{"name":%q}]}}`, testResources.R2Bucket)
			}
			return true
		}
		return false
	})
	i, e := a.Inspect(context.Background(), "fake-test-token", testRequest, testResources)
	if e != nil || i.D1Available || i.BucketAvailable {
		t.Fatalf("missed paginated collision %#v %v", i, e)
	}
}
func TestRedirectNeverLeaksCredential(t *testing.T) {
	leaked := false
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; io.WriteString(w, `{}`) }))
	defer dest.Close()
	a, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
		return true
	})
	if _, e := a.Inspect(context.Background(), "fake-test-token", testRequest, testResources); e == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("redirect reached destination")
	}
}
func TestD1StatementFailureStopsWithoutRetry(t *testing.T) {
	n := 0
	a, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/query") {
			n++
			fmt.Fprint(w, `{"success":true,"result":[{"success":false,"error":"secret"}]}`)
			return true
		}
		return false
	})
	e := a.ApplySchema(context.Background(), "fake-test-token", testRequest, testResources, Seed{ManagerID: "test", ManagerKeyHash: strings.Repeat("b", 64), ManagerKeyHint: "xyz1"})
	if e == nil || n != 1 || strings.Contains(e.Error(), "secret") {
		t.Fatalf("statement failure %d %v", n, e)
	}
}

func TestNullInventoryCannotMeanAvailable(t *testing.T) {
	for _, suffix := range []string{"/workers/scripts", "/d1/database", "/r2/buckets"} {
		t.Run(suffix, func(t *testing.T) {
			a, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if strings.HasSuffix(r.URL.Path, suffix) {
					fmt.Fprint(w, `{"success":true,"result":null}`)
					return true
				}
				return false
			})
			if _, e := a.Inspect(context.Background(), "fake-test-token", testRequest, testResources); e == nil {
				t.Fatal("null inventory interpreted as empty")
			}
		})
	}
}

func TestD1ShortPageStillReadsTotalCountBeforeDeclaringAvailable(t *testing.T) {
	pages := 0
	a, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "/d1/database") {
			return false
		}
		pages++
		name := "other-database"
		if r.URL.Query().Get("page") == "2" {
			name = testResources.D1Name
		}
		fmt.Fprintf(w, `{"success":true,"result":[{"name":%q}],"result_info":{"count":1,"total_count":2,"per_page":1}}`, name)
		return true
	})
	i, e := a.Inspect(context.Background(), "fake-test-token", testRequest, testResources)
	if e != nil || i.D1Available || pages != 2 {
		t.Fatalf("short first page concealed collision: pages=%d available=%v err=%v", pages, i.D1Available, e)
	}
}
func TestD1ContradictoryOrIncompleteTotalsFailClosed(t *testing.T) {
	for _, scenario := range []string{"empty-before-total", "count-mismatch", "more-than-total", "total-changed", "repeat-page", "page-cap"} {
		t.Run(scenario, func(t *testing.T) {
			a, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasSuffix(r.URL.Path, "/d1/database") {
					return false
				}
				page := r.URL.Query().Get("page")
				switch scenario {
				case "empty-before-total":
					fmt.Fprint(w, `{"success":true,"result":[],"result_info":{"total_count":2}}`)
				case "count-mismatch":
					fmt.Fprint(w, `{"success":true,"result":[{"name":"other"}],"result_info":{"count":0,"total_count":1}}`)
				case "more-than-total":
					fmt.Fprint(w, `{"success":true,"result":[{"name":"other"}],"result_info":{"total_count":0}}`)
				case "total-changed":
					total := 2
					if page == "2" {
						total = 3
					}
					fmt.Fprintf(w, `{"success":true,"result":[{"name":"database-%s"}],"result_info":{"total_count":%d}}`, page, total)
				case "repeat-page":
					fmt.Fprint(w, `{"success":true,"result":[{"name":"repeated"}],"result_info":{"total_count":2}}`)
				case "page-cap":
					fmt.Fprintf(w, `{"success":true,"result":[{"name":"database-%s"}],"result_info":{"total_count":21}}`, page)
				}
				return true
			})
			if _, e := a.Inspect(context.Background(), "fake-test-token", testRequest, testResources); e == nil {
				t.Fatal("incomplete or contradictory inventory accepted")
			}
		})
	}
}

func TestDigitWorkerAndShortAccountDNSLabelsRemainUsable(t *testing.T) {
	for _, subdomain := range []string{"a", "9", "ab", "9-account"} {
		t.Run(subdomain, func(t *testing.T) {
			a, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if strings.HasSuffix(r.URL.Path, "/workers/subdomain") {
					fmt.Fprintf(w, `{"success":true,"result":{"subdomain":%q}}`, subdomain)
					return true
				}
				return false
			})
			q, r := testRequest, testResources
			q.Worker = "2026-site-abcdef123456"
			r.Worker = q.Worker
			r.D1Name = q.Worker + "-db"
			r.R2Bucket = q.Worker + "-media"
			i, e := a.Inspect(context.Background(), "fake-test-token", q, r)
			if e != nil || i.WorkersSubdomain != subdomain {
				t.Fatalf("valid DNS labels rejected: %#v %v", i, e)
			}
			origin, e := a.EnableWorker(context.Background(), "fake-test-token", q, r)
			if e != nil || origin != "https://"+q.Worker+"."+subdomain+".workers.dev" {
				t.Fatalf("origin %s %v", origin, e)
			}
		})
	}
}
func TestInvalidAccountLabelsFailBeforeActivation(t *testing.T) {
	for _, subdomain := range []string{"-bad", "bad-", "bad.example", "bad_name", strings.Repeat("a", 64)} {
		t.Run(subdomain, func(t *testing.T) {
			a, calls := fixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if strings.HasSuffix(r.URL.Path, "/workers/subdomain") {
					fmt.Fprintf(w, `{"success":true,"result":{"subdomain":%q}}`, subdomain)
					return true
				}
				return false
			})
			if _, e := a.EnableWorker(context.Background(), "fake-test-token", testRequest, testResources); e == nil {
				t.Fatal("invalid DNS label accepted")
			}
			for _, c := range *calls {
				if strings.HasPrefix(c, "POST ") {
					t.Fatal("invalid hostname activated")
				}
			}
		})
	}
}
