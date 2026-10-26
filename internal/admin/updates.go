package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/observe"
	"github.com/zadstack/gorun/internal/update"
)

// Staged updates: the admin module hosts the lifecycle service, supplies
// the evidence providers (schema, theme, backup, health, links) and exposes
// the page and API. Deployment hosts and credentials come only from server
// configuration (config.Config); request parameters cannot change them.

const (
	releaseObservationKind = "update.release"
	releaseObservationKey  = "latest"
	releaseFreshness       = 6 * time.Hour
	linkCheckTimeout       = 10 * time.Second
)

// buildUpdateService wires config into the lifecycle; a missing or invalid
// release source leaves requests disabled while reads keep working.
func (h *handlers) buildUpdateService(d *kernel.Deps) *update.Service {
	cfg := update.Config{RunningVersion: api.CoreVersion, AllowDowngrade: d.Config.Update.AllowDowngrade, RequireDigest: !d.Config.Update.AllowMissingDigest,
		ActivationTimeout: d.Config.Update.ActivationTimeout, HealthLinks: d.Config.Update.HealthLinks,
		Compatibility: h.updateCompatibility, Backup: h.updateBackup, Health: h.updateHealth, Link: h.updateLink, Schema: h.updateSchema}
	if src, err := newReleaseSource(d.Config); err == nil {
		cfg.Source = src
	} else if d.Config.Update.SourceURL != "" {
		d.Log.Warn("update: release source ignored", "err", err)
	}
	if ad, err := update.NewCoolifyAdapter(d.Config.UpdateURL, d.Config.UpdateToken, d.Config.UpdateResource, nil); err == nil {
		cfg.Adapter = ad
	}
	return update.New(d.DB, d.Jobs, d.Log, cfg)
}

// newReleaseSource validates the operator's release index URL. HTTPS is
// required except for a loopback address, which lets a local supervisor
// publish the index on the same host.
func newReleaseSource(c config.Config) (update.Source, error) {
	raw := strings.TrimSpace(c.Update.SourceURL)
	if raw == "" {
		return nil, update.ErrNotConfigured
	}
	client := &http.Client{Timeout: 20 * time.Second}
	if strings.HasPrefix(raw, "http://127.0.0.1") || strings.HasPrefix(raw, "http://localhost") || strings.HasPrefix(raw, "http://[::1]") {
		return update.NewLoopbackReleaseIndex(raw, c.Update.SourceToken, c.Update.Artifact, client)
	}
	return update.NewReleaseIndex(raw, c.Update.SourceToken, c.Update.Artifact, client)
}

// registerReleaseObservation keeps "latest" as a background observation of
// the trusted source, so pages never fetch the index in a request.
func (h *handlers) registerReleaseObservation() {
	revision := ""
	if h.update.Configured() {
		sum := sha256.Sum256([]byte(h.d.Config.Update.SourceURL + "\x00" + h.d.Config.Update.Artifact))
		revision = hex.EncodeToString(sum[:8])
	}
	h.observe.Register(observe.Spec{Kind: releaseObservationKind, Freshness: releaseFreshness, MaxAttempts: 2, Timeout: 20 * time.Second,
		Keys: func(context.Context) ([]string, error) {
			if revision == "" {
				return nil, nil
			}
			return []string{releaseObservationKey}, nil
		},
		Revision: func(_ context.Context, key string) (string, error) {
			if key != releaseObservationKey {
				return "", nil
			}
			return revision, nil
		},
		Enabled: func(context.Context) bool { return revision != "" },
		Run: func(ctx context.Context, _, _ string) (observe.Result, error) {
			a, err := h.update.Source().Latest(ctx)
			if err != nil {
				return observe.Result{}, err
			}
			return observe.Result{Scope: "release_index", Data: a}, nil
		},
		Remediation: "Check the release source URL and that the index names a latest release.",
	})
}

// --- evidence providers --------------------------------------------------------

func (h *handlers) updateCompatibility(ctx context.Context, target string) (update.Report, error) {
	data := map[string]any{"modules": []string{}, "theme": "", "schema": "unchecked"}
	names := []string{}
	for _, m := range kernel.Modules(h.d) {
		if m.Enabled {
			names = append(names, m.Name)
		}
	}
	data["modules"] = names
	data["theme"] = h.activeTheme(ctx)
	inspect, ok := kernel.Get[kernel.SchemaInspector](h.d, kernel.ServiceSchema)
	if !ok {
		return update.Report{Reason: "schema inspection unavailable", Remediation: "Boot through the kernel so the schema inspector is published.", Data: data}, nil
	}
	got, err := inspect(ctx)
	if err != nil {
		return update.Report{}, err
	}
	data["schema"] = "known"
	if got.Ahead() {
		data["schema"], data["unknown_migrations"] = "ahead", got.Unknown
		return update.Report{Reason: "the database already holds migrations this binary does not know", Remediation: "A newer binary migrated this data. Update to that release or newer; do not activate an older artifact.", Data: data}, nil
	}
	if len(got.Mismatched) > 0 {
		data["mismatched_migrations"] = got.Mismatched
	}
	if data["theme"] == "" {
		// Theme support cannot be proven against the target; record that
		// the active theme is unknown rather than inventing a value.
		data["theme"] = nil
	}
	return update.Report{OK: true, Data: data}, nil
}

func (h *handlers) updateBackup(ctx context.Context) (update.Report, error) {
	backupMu.Lock()
	defer backupMu.Unlock()
	now := time.Now().UTC()
	res, err := backup.CreateWith(h.d.Config.DataDir, h.d.Config.DBPath(), now, backup.Options{SecretKey: h.d.Config.SecretKey})
	if err != nil {
		h.d.Audit.Record(ctx, "system.update.backup_failed", "system", "update", nil)
		return update.Report{}, err
	}
	success, err := verifiedBackup(res, now, h.d.Config.SecretKey)
	if err != nil {
		h.d.Audit.Record(ctx, "system.update.backup_failed", "system", "update", map[string]any{"stamp": res.Stamp})
		return update.Report{Reason: "backup verification failed: " + err.Error(), Remediation: "Inspect the backup under System → Backups; activation is blocked until a verified backup exists.", Data: map[string]any{"stamp": res.Stamp}}, nil
	}
	_ = h.d.Settings.Set(ctx, SettingBackupLastSuccess, success)
	h.d.Audit.Record(ctx, "system.update.backup", "system", "update", map[string]any{"stamp": res.Stamp, "files": res.Count})
	return update.Report{OK: true, Data: map[string]any{"stamp": res.Stamp, "bytes": success.Bytes, "files": res.Count, "verified": true}}, nil
}

func (h *handlers) updateHealth(ctx context.Context) (update.Report, error) {
	rep, err := h.healthReport(ctx)
	if err != nil {
		return update.Report{}, err
	}
	failed := []string{}
	for _, c := range rep.Checks {
		if c.Status == CheckFail {
			failed = append(failed, c.Name)
		}
	}
	data := map[string]any{"version": rep.Version, "checks": len(rep.Checks), "failed_checks": failed, "ready": true}
	if err := h.d.DB.PingContext(ctx); err != nil {
		data["ready"] = false
		return update.Report{Reason: "readiness failed: database unreachable", Remediation: "Inspect the database file and System Health.", Retryable: true, Data: data}, nil
	}
	if len(failed) > 0 {
		return update.Report{Reason: "protected health checks failed: " + strings.Join(failed, ", "), Remediation: "Open System Health on the new process and resolve the failing checks.", Retryable: true, Data: data}, nil
	}
	return update.Report{OK: true, Data: data}, nil
}

func (h *handlers) updateLink(ctx context.Context, link string) error {
	client := httpx.NewSafeClientOpts(httpx.SafeClientOptions{Timeout: linkCheckTimeout, AllowPrivate: h.d.Config.AllowPrivateNet})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return errors.New("invalid link")
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("link could not be fetched")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (h *handlers) updateSchema(ctx context.Context) ([]string, error) {
	rows, err := h.d.DB.QueryContext(ctx, `SELECT module || '/' || name FROM schema_migrations ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- status model --------------------------------------------------------------

// LatestRelease is the saved background observation of the trusted source.
type LatestRelease struct {
	Status     string  `json:"status" enum:"fresh,stale,failed,waiting,running,unknown" doc:"unknown: no source configured or never observed"`
	Version    *string `json:"version" doc:"Null when unknown"`
	Artifact   *string `json:"artifact"`
	ObservedAt *string `json:"observed_at" doc:"When the saved value was read from the source"`
	AgeSeconds *int64  `json:"age_seconds"`
	Source     string  `json:"source"`
	Error      string  `json:"error" doc:"Sanitized reason of the newest failed attempt"`
}

// UpdateStatus is GET /system/update. Latest, accepted, activated and
// requested versions are reported separately and never conflated.
type UpdateStatus struct {
	RunningVersion  string            `json:"running_version" doc:"Serving process identity (build string)"`
	IsRelease       bool              `json:"is_release" doc:"False for development builds, which cannot be compared"`
	AcceptedVersion *string           `json:"accepted_version" doc:"Last target durably accepted by a completed operation; null if none"`
	Latest          LatestRelease     `json:"latest"`
	UpdateAvailable *bool             `json:"update_available" doc:"Null when latest or the running identity is unknown"`
	Configured      bool              `json:"configured" doc:"A trusted release source is configured"`
	Source          string            `json:"source"`
	Adapter         string            `json:"adapter" enum:"manual,coolify"`
	Active          *update.Operation `json:"active" doc:"Pending or running operation; null when idle"`
	Last            *update.Operation `json:"last" doc:"Most recent operation; null when none"`
}

func (h *handlers) updateStatus(ctx context.Context) (UpdateStatus, error) {
	st := UpdateStatus{RunningVersion: api.CoreVersion, Configured: h.update.Configured(), Source: h.update.SourceName(), Adapter: h.update.AdapterName(), Latest: LatestRelease{Status: "unknown"}}
	st.IsRelease = update.IsRelease(api.CoreVersion)
	accepted, err := h.update.Accepted(ctx)
	if err != nil {
		return st, err
	}
	if accepted != "" {
		st.AcceptedVersion = &accepted
	}
	if st.Active, err = h.update.Active(ctx); err != nil {
		return st, err
	}
	if st.Last, err = h.update.Last(ctx); err != nil {
		return st, err
	}
	o, err := h.observe.Get(ctx, releaseObservationKind, releaseObservationKey)
	if err != nil && !errors.Is(err, observe.ErrNotFound) {
		return st, err
	}
	if err == nil {
		st.Latest.Source = h.update.SourceName()
		switch o.Status {
		case "fresh", "stale", "failed", "waiting", "running":
			st.Latest.Status = o.Status
		}
		if o.Attempt != nil && o.Attempt.Status == "failed" {
			st.Latest.Error = o.Attempt.Error
		}
		if o.Saved != nil {
			var a update.Artifact
			if update.DecodeArtifact(o.Saved.Data, &a) && a.Version != "" {
				v, id, at, age := a.Version, a.ID, o.Saved.At, o.Saved.AgeSeconds
				st.Latest.Version, st.Latest.Artifact, st.Latest.ObservedAt, st.Latest.AgeSeconds = &v, &id, &at, &age
				if st.IsRelease && o.Status == "fresh" {
					newer := update.Newer(a.Version, api.CoreVersion)
					st.UpdateAvailable = &newer
				}
			}
		}
	}
	return st, nil
}

// --- page --------------------------------------------------------------------------

type updatesPage struct {
	Status     UpdateStatus
	Operations []update.Operation
	Prefill    string
	CanRequest bool
}

func (h *handlers) updatesPage(w http.ResponseWriter, r *http.Request) {
	st, err := h.updateStatus(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	ops, err := h.update.List(r.Context(), 10)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page := updatesPage{Status: st, Operations: ops, CanRequest: st.Configured && st.Active == nil}
	if st.Latest.Version != nil && st.UpdateAvailable != nil && *st.UpdateAvailable {
		page.Prefill = *st.Latest.Version
	}
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.Render(w, r, "pages/system/updates.html", page)
}

// updateDeploy starts a staged operation from the page; the lifecycle runs
// in the background and the page shows its phases.
func (h *handlers) updateDeploy(w http.ResponseWriter, r *http.Request) {
	if !h.update.Configured() || r.FormValue("confirm") != "yes" {
		http.Error(w, "Update is not configured or confirmed", http.StatusBadRequest)
		return
	}
	req := update.Request{Version: r.FormValue("version"), ExpectedDigest: r.FormValue("expected_digest")}
	if u := auth.UserFromContext(r.Context()); u != nil {
		req.RequestedBy = u.Email
	}
	op, err := h.update.Request(r.Context(), req)
	switch {
	case err == nil:
		h.d.Audit.Record(r.Context(), "system.update.requested", "system", "update", map[string]any{"operation": op.ID, "version": op.RequestedVersion, "running": api.CoreVersion})
		httpx.SetFlash(w, "success", "admin.updates.requested")
	case errors.Is(err, update.ErrInvalidTarget):
		httpx.SetFlash(w, "error", "admin.updates.invalid")
	case isUpdateConflict(err):
		httpx.SetFlash(w, "error", "admin.updates.conflict")
	default:
		h.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/system/updates", http.StatusSeeOther)
}

func isUpdateConflict(err error) bool {
	var c *update.ErrConflict
	return errors.As(err, &c)
}

// refreshLatest queues a background read of the release source.
func (h *handlers) refreshLatest(w http.ResponseWriter, r *http.Request) {
	if _, err := h.observe.Refresh(r.Context(), releaseObservationKind, releaseObservationKey); err != nil && !errors.Is(err, observe.ErrDisabled) {
		h.serverError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.updates.refresh_queued")
	http.Redirect(w, r, "/admin/system/updates", http.StatusSeeOther)
}

// --- API ------------------------------------------------------------------------------

var updateErrors = []kernel.APIErrorResponse{
	{Status: 409, Code: "operation_in_progress", Description: "Another update operation, backup or restore owns the data directory, or a previously accepted deployment is still in progress; fields.operation_id names it"},
	{Status: 422, Code: "validation_failed", Description: "version is not a release tag or expected_digest is not sha256:…"},
	{Status: 503, Code: "update_not_configured", Description: "No trusted release source is configured on the server"},
}

func (h *handlers) updateAPIOperations() {
	idParam := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}
	status := api.Op("GET", "/system/update", "Running, accepted and latest versions with the active and last staged operations (no external calls)",
		[]string{"system"}, []string{PermUpdate}, nil, UpdateStatus{}, h.apiUpdateStatus)
	request := api.Op("POST", "/system/update", "Start a staged update operation; returns 202 with the pending operation, never a completed update",
		[]string{"system"}, []string{PermUpdate}, update.Request{}, update.Operation{}, h.apiUpdateRequest)
	request.Contract = &kernel.APIContract{SuccessStatuses: []int{202}, Errors: updateErrors}
	list := api.Op("GET", "/system/update/operations", "List staged update operations, newest first (phases omitted)",
		[]string{"system"}, []string{PermUpdate}, nil, []update.Operation{}, h.apiUpdateList)
	list.Params = []kernel.APIParam{{Name: "limit", In: "query", Type: "integer", Description: "1–100, default 20"}}
	get := api.Op("GET", "/system/update/operations/{id}", "One staged update operation with every phase, its timing, result and remediation",
		[]string{"system"}, []string{PermUpdate}, nil, update.Operation{}, h.apiUpdateGet)
	get.Params = idParam
	for _, op := range []kernel.APIOperation{status, request, list, get} {
		h.d.API.Add(op)
	}
}

func (h *handlers) apiUpdateStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.updateStatus(r.Context())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, st)
}

func (h *handlers) apiUpdateRequest(w http.ResponseWriter, r *http.Request) {
	var req update.Request
	if err := api.Decode(w, r, &req); err != nil {
		return
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		req.RequestedBy = u.Email
	}
	op, err := h.update.Request(r.Context(), req)
	if err != nil {
		h.updateAPIError(w, r, err)
		return
	}
	h.d.Audit.Record(r.Context(), "system.update.requested", "system", "update", map[string]any{"operation": op.ID, "version": op.RequestedVersion, "running": api.CoreVersion})
	httpx.JSON(w, http.StatusAccepted, op, nil)
}

func (h *handlers) updateAPIError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *update.ErrConflict
	switch {
	case errors.As(err, &conflict):
		api.WriteError(w, http.StatusConflict, "operation_in_progress", conflict.Reason, map[string]string{"operation_id": conflict.OperationID, "holder": conflict.Holder})
	case errors.Is(err, update.ErrInvalidTarget):
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "validation failed", map[string]string{"version": "must be a release tag such as 0.15.0; expected_digest must be sha256:…"})
	case errors.Is(err, update.ErrNotConfigured):
		api.WriteError(w, http.StatusServiceUnavailable, "update_not_configured", "no trusted release source is configured", nil)
	default:
		api.Fail(w, r, err)
	}
}

func (h *handlers) apiUpdateList(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &limit); err != nil || limit < 1 || limit > 100 {
			api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "validation failed", map[string]string{"limit": "1–100"})
			return
		}
	}
	list, err := h.update.List(r.Context(), limit)
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, list)
}

func (h *handlers) apiUpdateGet(w http.ResponseWriter, r *http.Request) {
	op, err := h.update.Get(r.Context(), chiParam(r, "id"))
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, op)
}

// --- lease sharing with backups ----------------------------------------------------

// holdDataLease takes the durable lease for a backup or restore so it
// cannot overlap an update activation or rollback. The returned release
// function is safe to call once the work is finished.
func (h *handlers) holdDataLease(ctx context.Context, holder string) (func(), error) {
	owner := holder + ":" + update.NewOwnerToken()
	if err := h.update.Lease().Acquire(ctx, holder, owner, ""); err != nil {
		return nil, err
	}
	return func() { _ = h.update.Lease().Release(context.WithoutCancel(ctx), owner) }, nil
}

// leaseHeld reports a conflict from the durable lease.
func leaseHeld(err error) bool {
	var held *update.ErrLeaseHeld
	return errors.As(err, &held)
}
