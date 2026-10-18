package admin_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/jobs"
)

// apiCall routes method+path through the registered API operations (the
// api module does this in production) acting as u.
func apiCall(t *testing.T, s *site, u *auth.User, method, path, body string) (int, map[string]any) {
	t.Helper()
	code, raw := apiRaw(s, u, method, path, body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: invalid JSON %q", method, path, raw)
	}
	return code, out
}

// apiRaw is apiCall without assertions (safe in goroutines).
func apiRaw(s *site, u *auth.User, method, path, body string) (int, []byte) {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), u)))
		})
	})
	for _, op := range s.app.Deps.API.Operations() {
		r.Method(op.Method, "/api"+op.Path, op.Handler)
	}
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestAPISettings(t *testing.T) {
	s := boot(t)
	admin := s.createUser("admin@example.com", "admin", "en")
	code, out := apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"site.base_url":"https://example.com/x"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("bad base url: %d %v", code, out)
	}
	fields := out["error"].(map[string]any)["fields"].(map[string]any)
	if fields["site.base_url"] == nil {
		t.Fatalf("fields: %v", fields)
	}
	code, out = apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"site.title":"API","mail.password":"hunter2hunter2","mail.port":2525,"mail.tls":"tls"}`)
	if code != http.StatusOK {
		t.Fatalf("patch: %d %v", code, out)
	}
	data := out["data"].(map[string]any)
	if data["site.title"] != "API" || data["mail.password_set"] != true || data["mail.port"].(float64) != 2525 {
		t.Fatalf("data: %v", data)
	}
	if _, leaked := data["mail.password"]; leaked {
		t.Fatal("mail.password must never be returned")
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "hunter2") {
		t.Fatal("password leaked")
	}
	if code, _ := apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"nope":1}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown key: %d", code)
	}
	ed := s.createUser("ed@example.com", "editor", "en")
	if code, _ := apiCall(t, s, ed, "GET", "/api/v1/settings", ""); code != http.StatusForbidden {
		t.Fatalf("editor settings: %d", code)
	}
}

func TestAPIUsers(t *testing.T) {
	s := boot(t)
	admin := s.createUser("admin@example.com", "admin", "en")
	code, out := apiCall(t, s, admin, "POST", "/api/v1/users", `{"email":"new@example.com","name":"New","password":"correct horse battery","role":"author","locale":"ar"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, out)
	}
	id := out["data"].(map[string]any)["id"].(string)
	if code, out := apiCall(t, s, admin, "POST", "/api/v1/users", `{"email":"bad","password":"x","role":"author"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create: %d %v", code, out)
	}
	code, out = apiCall(t, s, admin, "GET", "/api/v1/users?role=author", "")
	if code != http.StatusOK || len(out["data"].([]any)) != 1 || out["meta"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}
	code, out = apiCall(t, s, admin, "PATCH", "/api/v1/users/"+id, `{"role":"editor"}`)
	if code != http.StatusOK || out["data"].(map[string]any)["role"] != "editor" {
		t.Fatalf("patch: %d %v", code, out)
	}
	if s.auditCount("user.role_change", id) != 1 {
		t.Fatal("role change not audited")
	}
	if code, _ := apiCall(t, s, admin, "PATCH", "/api/v1/users/"+admin.ID, `{"role":"editor"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("self demote: %d", code)
	}
	if code, _ := apiCall(t, s, admin, "DELETE", "/api/v1/users/"+admin.ID, ""); code != http.StatusConflict {
		t.Fatalf("self delete: %d", code)
	}
	if code, _ := apiCall(t, s, admin, "GET", "/api/v1/users/nope", ""); code != http.StatusNotFound {
		t.Fatalf("missing user: %d", code)
	}
	if code, _ := apiCall(t, s, admin, "DELETE", "/api/v1/users/"+id, ""); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	if _, err := s.users().Get(context.Background(), id); err != auth.ErrNotFound {
		t.Fatal("not deleted")
	}
	ed := s.createUser("ed@example.com", "editor", "en")
	if code, _ := apiCall(t, s, ed, "GET", "/api/v1/users", ""); code != http.StatusForbidden {
		t.Fatalf("editor users: %d", code)
	}
}

func TestAPIJobsAuditHealth(t *testing.T) {
	s := boot(t)
	admin := s.createUser("admin@example.com", "admin", "en")
	ctx := context.Background()
	s.app.Deps.Audit.Record(auth.WithUser(ctx, admin), "test.api", "thing", "1", nil)
	code, out := apiCall(t, s, admin, "GET", "/api/v1/audit?action=test.", "")
	if code != http.StatusOK || len(out["data"].([]any)) != 1 {
		t.Fatalf("audit: %d %v", code, out)
	}
	if code, _ := apiCall(t, s, admin, "GET", "/api/v1/audit?since=nope", ""); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad since: %d", code)
	}
	id, _ := s.app.Deps.Jobs.Enqueue(ctx, "test.api", nil, jobs.RunAt(time.Now().Add(time.Hour)))
	s.app.Deps.DB.Exec(`UPDATE jobs SET status = 'dead' WHERE id = ?`, id)
	code, out = apiCall(t, s, admin, "GET", "/api/v1/jobs?status=dead", "")
	if code != http.StatusOK || len(out["data"].([]any)) != 1 {
		t.Fatalf("jobs: %d %v", code, out)
	}
	if code, _ := apiCall(t, s, admin, "GET", "/api/v1/jobs?status=weird", ""); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad status: %d", code)
	}
	if code, _ := apiCall(t, s, admin, "POST", "/api/v1/jobs/"+id+"/retry", ""); code != http.StatusOK {
		t.Fatalf("retry: %d", code)
	}
	if code, _ := apiCall(t, s, admin, "POST", "/api/v1/jobs/nope/retry", ""); code != http.StatusNotFound {
		t.Fatalf("retry missing: %d", code)
	}
	code, out = apiCall(t, s, admin, "GET", "/api/v1/system/health", "")
	if code != http.StatusOK {
		t.Fatalf("health: %d", code)
	}
	data := out["data"].(map[string]any)
	if data["journal_mode"] != "wal" || data["jobs"] == nil || data["checks"] == nil {
		t.Fatalf("health data: %v", data)
	}
	if _, ok := data["version"].(string); !ok || data["version"] == "" {
		t.Fatalf("health version missing: %v", data["version"])
	}
	raw, _ := json.Marshal(data)
	if strings.Contains(strings.ToLower(string(raw)), "secret") {
		t.Fatalf("health must not expose secrets: %s", raw)
	}
}

func TestAPISettingsPasswordNullClearsAndBadJSON(t *testing.T) {
	s := boot(t)
	admin := s.createUser("admin@example.com", "admin", "en")
	ctx := context.Background()
	if code, out := apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"mail.password":"hunter2hunter2"}`); code != http.StatusOK {
		t.Fatalf("set: %d %v", code, out)
	}
	enc := s.app.Deps.Settings.GetString(ctx, "mail.password", "")
	if enc == "" {
		t.Fatal("password not stored")
	}
	apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"mail.password":""}`)
	if got := s.app.Deps.Settings.GetString(ctx, "mail.password", ""); got != enc {
		t.Fatal(`"" must keep the stored password`)
	}
	code, out := apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"mail.password":null}`)
	if code != http.StatusOK || out["data"].(map[string]any)["mail.password_set"] != false {
		t.Fatalf("null must clear: %d %v", code, out)
	}
	if got := s.app.Deps.Settings.GetString(ctx, "mail.password", "x"); got != "" {
		t.Fatalf("password not cleared: %q", got)
	}
	code, out = apiCall(t, s, admin, "PATCH", "/api/v1/settings", `{"site.title": tru`)
	if code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", code)
	}
	msg := out["error"].(map[string]any)["message"].(string)
	if strings.Contains(msg, "invalid character") || strings.Contains(msg, "unexpected") {
		t.Fatalf("decoder error leaked: %q", msg)
	}
}
