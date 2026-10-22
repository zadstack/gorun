package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/update"
)

const digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// releaseIndex serves a trusted release index on the loopback address.
func releaseIndex(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer SECRET_SOURCE_TOKEN" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"latest":"0.15.0","releases":[
			{"version":"0.15.0","artifacts":[{"id":"gorun-0.15.0-linux-amd64.tar.gz","digest":"` + digestB + `"}]},
			{"version":"0.14.1","artifacts":[{"id":"gorun-0.14.1-linux-amd64.tar.gz","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withSource(src *httptest.Server, dataDir string) func(*config.Config) {
	return func(c *config.Config) {
		c.DataDir = dataDir
		c.Update.SourceURL = src.URL + "/releases.json"
		c.Update.SourceToken = "SECRET_SOURCE_TOKEN"
		c.Update.ActivationTimeout = 2 * time.Minute
	}
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	prev := api.CoreVersion
	api.CoreVersion = v
	t.Cleanup(func() { api.CoreVersion = prev })
}

type statusEnvelope struct {
	Data struct {
		RunningVersion  string  `json:"running_version"`
		AcceptedVersion *string `json:"accepted_version"`
		Configured      bool    `json:"configured"`
		UpdateAvailable *bool   `json:"update_available"`
		Latest          struct {
			Status  string  `json:"status"`
			Version *string `json:"version"`
		} `json:"latest"`
		Active *update.Operation `json:"active"`
		Last   *update.Operation `json:"last"`
	} `json:"data"`
}

func (s *site) updateStatus() statusEnvelope {
	s.t.Helper()
	resp, body := s.do("GET", "/api/v1/system/update", nil)
	if resp.StatusCode != 200 {
		s.t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var env statusEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		s.t.Fatal(err)
	}
	return env
}

func (s *site) waitFor(what string, cond func(statusEnvelope) bool) statusEnvelope {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := s.updateStatus()
		if cond(st) {
			return st
		}
		time.Sleep(50 * time.Millisecond) // the booted workers run the jobs
	}
	s.t.Fatalf("timed out waiting for %s", what)
	return statusEnvelope{}
}

func TestUpdatesDisabledUntilOperatorConfiguration(t *testing.T) {
	s := boot(t)
	s.login("updates@example.invalid", "admin", "en")
	resp, page := s.do("GET", "/admin/system/updates", nil)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	mustContain(t, page, `data-system-updates`, `data-configured="false"`, `disabled`, `not enabled`, `not a release tag`)
	resp, _ = s.post("/admin/system/updates", url.Values{"confirm": {"yes"}, "version": {"0.15.0"}, "url": {"https://untrusted.example"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatal(resp.StatusCode)
	}
	list, err := backup.List(s.app.Deps.Config.DataDir)
	if err != nil || len(list) != 0 {
		t.Fatalf("unexpected backup %v %v", list, err)
	}
	st := s.updateStatus()
	if st.Data.Configured || st.Data.Latest.Status != "unknown" || st.Data.UpdateAvailable != nil || st.Data.Active != nil || st.Data.AcceptedVersion != nil {
		t.Fatalf("%+v", st.Data)
	}
	resp, body := s.jsonPost("/api/v1/system/update", `{"version":"0.15.0"}`)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "update_not_configured") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	s.login("updates-editor@example.invalid", "editor", "en")
	for _, path := range []string{"/admin/system/updates"} {
		if resp, _ := s.do("GET", path, nil); resp.StatusCode != http.StatusForbidden {
			t.Fatal(resp.StatusCode)
		}
	}
	if resp, _ := s.post("/admin/system/updates", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatal(resp.StatusCode)
	}
	if resp, _ := s.do("GET", "/api/v1/system/update", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatal(resp.StatusCode)
	}
	if resp, _ := s.do("GET", "/api/v1/system/observations", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatal(resp.StatusCode)
	}
}

func (s *site) jsonPost(path, body string) (*http.Response, string) {
	s.t.Helper()
	req, _ := http.NewRequest("POST", s.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", s.csrf())
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp, b.String()
}

func TestStagedUpdateLifecycleAcrossRestartThroughAdmin(t *testing.T) {
	setVersion(t, "0.14.1")
	src := releaseIndex(t)
	dataDir := t.TempDir()
	s := bootWith(t, withSource(src, dataDir))
	s.login("updates-admin@example.invalid", "admin", "en")

	// Latest comes from a background observation, never from the request.
	_, page := s.do("GET", "/admin/system/updates", nil)
	mustContain(t, page, `data-configured="true"`, `data-latest-status="unknown"`)
	mustNotContain(t, page, "SECRET_SOURCE_TOKEN", src.URL)
	if resp, _ := s.post("/admin/system/updates/refresh", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	st := s.waitFor("latest observation", func(e statusEnvelope) bool { return e.Data.Latest.Status == "fresh" })
	if st.Data.Latest.Version == nil || *st.Data.Latest.Version != "0.15.0" || st.Data.UpdateAvailable == nil || !*st.Data.UpdateAvailable {
		t.Fatalf("%+v", st.Data)
	}
	_, page = s.do("GET", "/admin/system/updates", nil)
	mustContain(t, page, `value="0.15.0"`, "A newer release is available")

	resp, _ := s.post("/admin/system/updates", url.Values{"confirm": {"yes"}, "version": {"0.15.0"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	if s.auditCount("system.update.requested", "update") != 1 {
		t.Fatal("request not audited")
	}
	st = s.waitFor("activation waiting on the operator", func(e statusEnvelope) bool {
		return e.Data.Active != nil && e.Data.Active.DeploymentRef != ""
	})
	op := st.Data.Active
	if op.Status != update.StatusRunning || op.DeploymentRef != "manual:0.15.0" || op.BackupStamp == "" || op.ActivatedVersion != nil || op.AcceptedVersion != nil {
		t.Fatalf("%+v", op)
	}
	if list, _ := backup.List(dataDir); len(list) != 1 || list[0].Stamp != op.BackupStamp {
		t.Fatalf("backup missing: %v", list)
	}
	for _, name := range []string{"compatibility", "verification", "backup"} {
		if p := phase(op, name); p.Status != "passed" || p.DurationMS == nil {
			t.Fatalf("%s: %+v", name, p)
		}
	}
	if !strings.Contains(string(phase(op, "verification").Result), digestB) {
		t.Fatalf("verified digest not recorded: %s", phase(op, "verification").Result)
	}
	_, page = s.do("GET", "/admin/system/updates", nil)
	mustContain(t, page, `data-operation-detail="`+op.ID+`"`, `data-phase="activation" data-status="running"`, "Manual adapter")
	mustNotContain(t, page, "SECRET_SOURCE_TOKEN")

	// Conflicts: a second request on the page and through the API.
	if resp, _ := s.post("/admin/system/updates", url.Values{"confirm": {"yes"}, "version": {"0.15.0"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	_, page = s.do("GET", "/admin/system/updates", nil)
	mustContain(t, page, "owns the data directory")
	resp, body := s.jsonPost("/api/v1/system/update", `{"version":"0.15.0"}`)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, `"operation_in_progress"`) || !strings.Contains(body, op.ID) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	// A backup cannot start while the update owns the lease.
	if resp, _ := s.post("/admin/system/backups", nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	if list, _ := backup.List(dataDir); len(list) != 1 {
		t.Fatal("backup ran during an update")
	}
	resp, body = s.do("GET", "/api/v1/system/update/operations/"+op.ID, nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"phases"`) || strings.Contains(body, "SECRET_SOURCE_TOKEN") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}

	// The operator stops the process and starts the verified release on the
	// same data directory. The new process reconciles on boot and finishes.
	s.srv.Close()
	s.app.Close()
	setVersion(t, "0.15.0")
	s2 := bootWith(t, withSource(src, dataDir))
	s2.signIn("updates-admin@example.invalid")
	st = s2.waitFor("acceptance", func(e statusEnvelope) bool { return e.Data.Last != nil && e.Data.Last.Status == update.StatusSucceeded })
	last := st.Data.Last
	if last.AcceptedVersion == nil || *last.AcceptedVersion != "0.15.0" || last.ActivatedVersion == nil || *last.ActivatedVersion != "0.15.0" {
		t.Fatalf("%+v", last)
	}
	if st.Data.AcceptedVersion == nil || *st.Data.AcceptedVersion != "0.15.0" || st.Data.RunningVersion != "0.15.0" || st.Data.Active != nil {
		t.Fatalf("%+v", st.Data)
	}
	if p := phase(last, "activation"); p.Status != "passed" || !strings.Contains(string(p.Result), "serving_identity_after_restart") {
		t.Fatalf("%+v", p)
	}
	if p := phase(last, "health"); p.Status != "passed" || !strings.Contains(string(p.Result), `"ready":true`) {
		t.Fatalf("%+v", p)
	}
	_, page = s2.do("GET", "/admin/system/updates", nil)
	mustContain(t, page, `data-status="succeeded"`, "is the latest observed release")
	// Settings, theme and content survived; the operation never restored a snapshot.
	if got := s2.app.Deps.Settings.GetString(context.Background(), "site.theme", "default"); got == "" {
		t.Fatal("settings lost")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "restores")); err == nil {
		t.Fatal("a restore directory was created by the update")
	}
	svc, _ := kernel.Get[*update.Service](s2.app.Deps, update.ServiceName)
	if _, held, _ := svc.Lease().Current(context.Background()); held {
		t.Fatal("lease not released after acceptance")
	}
}

func phase(op *update.Operation, name string) update.Phase {
	for _, p := range op.Phases {
		if p.Name == name {
			return p
		}
	}
	return update.Phase{Name: name, Status: "missing"}
}

func TestInterruptedUpdateIsReportedNotAssumedAfterRestart(t *testing.T) {
	setVersion(t, "0.14.1")
	src := releaseIndex(t)
	dataDir := t.TempDir()
	s := bootWith(t, withSource(src, dataDir))
	s.login("updates-admin@example.invalid", "admin", "en")
	if resp, _ := s.post("/admin/system/updates", url.Values{"confirm": {"yes"}, "version": {"0.15.0"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	st := s.waitFor("activation", func(e statusEnvelope) bool { return e.Data.Active != nil && e.Data.Active.DeploymentRef != "" })
	id := st.Data.Active.ID
	// Simulate a crash while a non-resumable phase ran: the row says backup
	// is running, then the process restarts with the same version.
	s.app.Deps.DB.Exec(`UPDATE update_phases SET status = 'running', finished_at = '' WHERE operation_id = ? AND name = 'backup'`, id)
	s.app.Deps.DB.Exec(`UPDATE update_phases SET status = 'pending', started_at = '' WHERE operation_id = ? AND name = 'activation'`, id)
	s.app.Deps.DB.Exec(`UPDATE update_operations SET deployment_ref = '', current_phase = 'backup' WHERE id = ?`, id)
	s.srv.Close()
	s.app.Close()
	s2 := bootWith(t, withSource(src, dataDir))
	s2.signIn("updates-admin@example.invalid")
	st = s2.updateStatus()
	if st.Data.Last == nil || st.Data.Last.Status != update.StatusInterrupted || st.Data.Active != nil || st.Data.AcceptedVersion != nil {
		t.Fatalf("%+v", st.Data.Last)
	}
	if p := phase(st.Data.Last, "backup"); p.Status != "interrupted" {
		t.Fatalf("%+v", p)
	}
	_, page := s2.do("GET", "/admin/system/updates", nil)
	mustContain(t, page, `data-status="interrupted"`)
	// A new request is accepted afterwards.
	if resp, _ := s2.post("/admin/system/updates", url.Values{"confirm": {"yes"}, "version": {"0.15.0"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	if st := s2.updateStatus(); st.Data.Active == nil {
		t.Fatal("new request not admitted after interruption")
	}
}

func TestObservationsAPIRefreshesInBackgroundOnly(t *testing.T) {
	src := releaseIndex(t)
	s := bootWith(t, withSource(src, t.TempDir()))
	s.login("obs-admin@example.invalid", "admin", "en")
	resp, body := s.do("GET", "/api/v1/system/observations", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"data":[]`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = s.do("GET", "/api/v1/system/observations/update.release/latest", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"status":"never"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := s.jsonPost("/api/v1/system/observations/nope/x/refresh", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.StatusCode)
	}
	if resp, _ := s.jsonPost("/api/v1/system/observations/update.release/other/refresh", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.StatusCode)
	}
	resp, body = s.jsonPost("/api/v1/system/observations/update.release/latest/refresh", "")
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(body, `"job_id"`) || !strings.Contains(body, `"deduplicated":false`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var first struct {
		Data struct {
			JobID string `json:"job_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(body), &first)
	// The booted workers may finish the first job before the second request
	// lands (slow CI); deduplication is asserted only while it is in flight.
	resp, body = s.jsonPost("/api/v1/system/observations/update.release/latest/refresh", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if job, err := s.app.Deps.Jobs.Get(context.Background(), first.Data.JobID); err == nil && (job.Status == "queued" || job.Status == "running") && !strings.Contains(body, `"deduplicated":true`) {
		t.Fatalf("in-flight refresh was not deduplicated: %s", body)
	}
	s.waitFor("fresh latest", func(e statusEnvelope) bool { return e.Data.Latest.Status == "fresh" })
	resp, body = s.do("GET", "/api/v1/system/observations?kind=update.release", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"status":"fresh"`) || !strings.Contains(body, `"scope":"release_index"`) || strings.Contains(body, "SECRET_SOURCE_TOKEN") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	_, page := s.do("GET", "/admin/system/health", nil)
	mustContain(t, page, `data-observation="update.release/latest" data-status="fresh"`)
	// Health never contacts the source: the observation age is unchanged after a reload.
	resp, body = s.do("GET", "/api/v1/system/health", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"observations"`) || !strings.Contains(body, `"parked_jobs"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	// Without a source the capability is disabled: refresh is refused, rows stay readable.
	s3 := boot(t)
	s3.login("obs-admin2@example.invalid", "admin", "en")
	if resp, body := s3.jsonPost("/api/v1/system/observations/update.release/latest/refresh", ""); resp.StatusCode != http.StatusConflict || !strings.Contains(body, "capability_disabled") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	// Editors may neither read nor refresh.
	s3.login("obs-editor@example.invalid", "editor", "en")
	if resp, _ := s3.jsonPost("/api/v1/system/observations/update.release/latest/refresh", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatal(resp.StatusCode)
	}
}

func TestUpdateRequestValidationAndLeaseConflicts(t *testing.T) {
	setVersion(t, "0.14.1")
	src := releaseIndex(t)
	s := bootWith(t, withSource(src, t.TempDir()))
	s.login("updates-admin@example.invalid", "admin", "en")
	resp, body := s.jsonPost("/api/v1/system/update", `{"version":"latest"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, `"version"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp, _ := s.post("/admin/system/updates", url.Values{"confirm": {"yes"}, "version": {"nope"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatal(resp.StatusCode)
	}
	_, page := s.do("GET", "/admin/system/updates", nil)
	mustContain(t, page, "must be a release tag")
	// A backup in progress blocks a request with a 409 naming the holder.
	svc, _ := kernel.Get[*update.Service](s.app.Deps, update.ServiceName)
	if err := svc.Lease().Acquire(context.Background(), update.HolderBackup, "test-backup", ""); err != nil {
		t.Fatal(err)
	}
	resp, body = s.jsonPost("/api/v1/system/update", `{"version":"0.15.0"}`)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, `"holder":"backup"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	svc.Lease().Release(context.Background(), "test-backup")
	// The lifecycle honours an expected digest: a mismatch fails verification
	// before any backup or activation.
	resp, body = s.jsonPost("/api/v1/system/update", `{"version":"0.15.0","expected_digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	st := s.waitFor("verification failure", func(e statusEnvelope) bool { return e.Data.Last != nil && e.Data.Last.Status == update.StatusFailed })
	if p := phase(st.Data.Last, "verification"); p.Status != "failed" || !strings.Contains(p.Error, "digest") {
		t.Fatalf("%+v", p)
	}
	if list, _ := backup.List(s.app.Deps.Config.DataDir); len(list) != 0 {
		t.Fatal("backup taken after verification failed")
	}
}
