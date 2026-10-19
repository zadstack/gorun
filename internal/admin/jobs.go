package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
)

const payloadPreviewMax = 2000

// jobStatuses are the filters of the jobs page. They match jobs.Stats:
// "queued" = never attempted, "retrying" = queued after a failure (counted
// as Failed by Stats; "failed" is accepted as an alias).
var jobStatuses = []string{"queued", "retrying", "running", "dead", "done"}

// normalizeJobStatus maps the "failed" alias and rejects unknown values.
func normalizeJobStatus(s string) (string, bool) {
	if s == "failed" {
		return "retrying", true
	}
	return s, s == "" || slices.Contains(jobStatuses, s)
}

func jobsWhere(status string) (string, []any) {
	switch status {
	case "":
		return "1=1", nil
	case "queued":
		return "status = 'queued' AND attempts = 0", nil
	case "retrying":
		return "status = 'queued' AND attempts > 0", nil
	default:
		return "status = ?", []any{status}
	}
}

const jobColumns = `id, kind, payload_json, status, run_at, attempts, max_attempts, last_error, locked_by, locked_at, created_at, updated_at, idempotency_key`

// listJobs pages the jobs table (jobs.Queue.List has no offset).
func listJobs(ctx context.Context, d *sql.DB, status string, page, perPage int) ([]jobs.Job, int, error) {
	where, args := jobsWhere(status)
	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("admin: count jobs: %w", err)
	}
	list, err := queryJobs(ctx, d, `SELECT `+jobColumns+` FROM jobs WHERE `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, perPage, (page-1)*perPage)...)
	return list, total, err
}

// failedJobs returns the newest failed (awaiting retry) and dead jobs.
func failedJobs(ctx context.Context, d *sql.DB, limit int) ([]jobs.Job, error) {
	return queryJobs(ctx, d, `SELECT `+jobColumns+` FROM jobs WHERE status = 'dead' OR (status = 'queued' AND attempts > 0)
		ORDER BY updated_at DESC, id DESC LIMIT ?`, limit)
}

func queryJobs(ctx context.Context, d *sql.DB, q string, args ...any) ([]jobs.Job, error) {
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("admin: list jobs: %w", err)
	}
	defer rows.Close()
	out := []jobs.Job{}
	for rows.Next() {
		var j jobs.Job
		var payload string
		var lockedBy, lockedAt, key sql.NullString
		if err := rows.Scan(&j.ID, &j.Kind, &payload, &j.Status, &j.RunAt, &j.Attempts, &j.MaxAttempts,
			&j.LastError, &lockedBy, &lockedAt, &j.CreatedAt, &j.UpdatedAt, &key); err != nil {
			return nil, fmt.Errorf("admin: scan job: %w", err)
		}
		j.Payload = json.RawMessage(payload)
		j.LockedBy, j.LockedAt, j.IdempotencyKey = lockedBy.String, lockedAt.String, key.String
		out = append(out, j)
	}
	return out, rows.Err()
}

// jobView adds display fields to a job.
type jobView struct {
	jobs.Job
	LabelKey  string
	Preview   string
	Retryable bool
}

func viewJobs(list []jobs.Job) []jobView {
	out := make([]jobView, len(list))
	for i, j := range list {
		out[i] = jobView{Job: j, LabelKey: operatorJobLabel(j.Kind), Preview: prettyJSON(j.Payload, payloadPreviewMax), Retryable: j.Status == "dead" || (j.Status == "queued" && j.Attempts > 0)}
	}
	return out
}

// prettyJSON indents raw JSON and truncates it to max runes.
func prettyJSON(raw []byte, max int) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		buf.Reset()
		buf.Write(raw)
	}
	s := buf.String()
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

type jobsPage struct {
	Jobs     []jobView
	Status   string
	Statuses []string
	Stats    jobs.Stats
	Pager    pager
}

func (h *handlers) jobsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	status, ok := normalizeJobStatus(q.Get("status"))
	if !ok {
		status = ""
	}
	page, perPage := pageParams(q)
	list, total, err := listJobs(ctx, h.d.DB, status, page, perPage)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	stats, err := h.d.Jobs.Stats(ctx)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.tpl.Render(w, r, "pages/system/jobs.html", jobsPage{Jobs: viewJobs(list), Status: status, Statuses: jobStatuses, Stats: stats,
		Pager: newPager("/admin/system/jobs", q, page, perPage, total)})
}

// retryJob requeues a job and audits it.
func (h *handlers) retryJob(ctx context.Context, id string) error {
	if err := h.d.Jobs.Retry(ctx, id); err != nil {
		return err
	}
	h.d.Audit.Record(ctx, "jobs.retry", "job", id, nil)
	return nil
}

func (h *handlers) jobRetry(w http.ResponseWriter, r *http.Request) {
	err := h.retryJob(r.Context(), urlID(r))
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		httpx.SetFlash(w, "error", "admin.jobs.retry_missing")
	case err != nil:
		h.serverError(w, r, err)
		return
	default:
		httpx.SetFlash(w, "success", "admin.jobs.retried")
	}
	back := r.FormValue("back")
	if !strings.HasPrefix(back, "/admin/system/") || strings.HasPrefix(back, "//") || strings.ContainsAny(back, "\\\r\n") {
		back = "/admin/system/jobs"
	}
	redirect(w, r, back)
}

// Retention settings of the jobs.prune cron (whole days).
const (
	SettingRetainDoneDays  = "jobs.retain_done_days"
	SettingRetainDeadDays  = "jobs.retain_dead_days"
	SettingRetainKeyedDays = "jobs.retain_keyed_days"
)

// retention reads the jobs.retain_* settings (positive whole days), falling
// back to jobs.DefaultRetention per key.
func retention(ctx context.Context, d *kernel.Deps) jobs.Retention {
	days := func(key string, def time.Duration) time.Duration {
		var n int
		if err := d.Settings.Get(ctx, key, &n); err != nil || n < 1 {
			return def
		}
		return time.Duration(n) * 24 * time.Hour
	}
	r := jobs.DefaultRetention
	return jobs.Retention{Done: days(SettingRetainDoneDays, r.Done), Dead: days(SettingRetainDeadDays, r.Dead),
		Keyed: days(SettingRetainKeyedDays, r.Keyed)}
}

// pruneJobs is the hourly jobs.prune cron: it deletes finished jobs past
// their retention so the table does not grow forever.
func (h *handlers) pruneJobs(ctx context.Context) error {
	return pruneJobsAt(ctx, h.d, time.Now())
}

func pruneJobsAt(ctx context.Context, d *kernel.Deps, now time.Time) error {
	n, err := d.Jobs.Prune(ctx, now, retention(ctx, d))
	if err != nil {
		return err
	}
	if n > 0 {
		d.Log.Info("jobs: pruned finished jobs", "rows", n)
	}
	return nil
}

func operatorJobLabel(kind string) string {
	switch kind {
	case "hooks.event":
		return "operator.jobs.hooks"
	case "mail.send":
		return "operator.jobs.mail"
	case "canvas.generate", "canvas.tools", "studio.generate":
		return "operator.jobs.canvas"
	default:
		return ""
	}
}
