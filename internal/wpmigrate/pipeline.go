package wpmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/jobs"
)

// Job kinds. One migration.step job per step generates the step's items
// and enqueues one migration.item job per pending item of the current
// phase; when the last item of a phase finishes, migration.advance moves to
// the next phase or step. Every transition is a compare-and-swap on
// migrations.cursor, so duplicate jobs are harmless no-ops.
const (
	jobStep    = "migration.step"
	jobItem    = "migration.item"
	jobAdvance = "migration.advance"
)

// steps run in this order (spec §4.7 step 6); stepKind is the item kind of
// each step (validate has no items).
var steps = []string{"users", "terms", "media", "content", "products", "menus", "redirects", "rewrite", "validate"}

var stepKind = map[string]string{"users": "user", "terms": "term", "media": "media", "content": "content",
	"products": "product", "menus": "menu", "redirects": "redirect", "rewrite": "rewrite"}

// enqueuer is the part of *jobs.Queue the pipeline uses.
type enqueuer interface {
	Enqueue(ctx context.Context, kind string, payload any, opts ...jobs.Option) (string, error)
}

type stepPayload struct {
	MigrationID string `json:"migration_id"`
	Step        string `json:"step"`
	Cursor      int    `json:"cursor"`
}

type itemPayload struct {
	MigrationID string `json:"migration_id"`
	ItemID      string `json:"item_id"`
}

type advancePayload struct {
	MigrationID string `json:"migration_id"`
	Cursor      int    `json:"cursor"`
}

// registerJobs binds the pipeline handlers on q (the real queue).
func (s *service) registerJobs(q *jobs.Queue) {
	q.Register(jobStep, s.handleStep, 3)
	q.Register(jobItem, s.handleItem, 3)
	q.Register(jobAdvance, s.handleAdvance, 3)
}

// inlineQueue runs pipeline jobs synchronously (Preview, the import-wxr
// CLI and tests): Drain executes them in FIFO order.
type inlineQueue struct {
	mu       sync.Mutex
	pending  []inlineJob
	handlers map[string]jobs.Handler
}

type inlineJob struct {
	kind    string
	payload json.RawMessage
}

func newInlineQueue() *inlineQueue { return &inlineQueue{handlers: map[string]jobs.Handler{}} }

// bind registers s's handlers, run on a copy of s that enqueues here.
func (q *inlineQueue) bind(s *service) *service {
	cp := s.withQueue(q)
	q.handlers[jobStep] = cp.handleStep
	q.handlers[jobItem] = cp.handleItem
	q.handlers[jobAdvance] = cp.handleAdvance
	return cp
}

func (q *inlineQueue) Enqueue(_ context.Context, kind string, payload any, _ ...jobs.Option) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, inlineJob{kind: kind, payload: b})
	return db.NewID(), nil
}

// drain runs up to limit jobs (all when limit <= 0) and returns how many
// ran. A handler error is returned after logging nothing else.
func (q *inlineQueue) drain(ctx context.Context, limit int) (int, error) {
	n := 0
	for limit <= 0 || n < limit {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.mu.Unlock()
			return n, nil
		}
		j := q.pending[0]
		q.pending = q.pending[1:]
		h := q.handlers[j.kind]
		q.mu.Unlock()
		n++
		if h == nil {
			return n, fmt.Errorf("wpmigrate: no inline handler for %s", j.kind)
		}
		if err := h(ctx, j.payload); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (q *inlineQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// --- control ----------------------------------------------------------------

// Start runs (or re-runs) the import. Items already done whose WordPress
// "modified" checksum is unchanged are skipped.
func (s *service) Start(ctx context.Context, migrationID string) error {
	return s.begin(ctx, migrationID, false)
}

func (s *service) begin(ctx context.Context, migrationID string, dry bool) error {
	m, err := s.getMigration(ctx, migrationID)
	if err != nil {
		return err
	}
	if m.Status == StatusRunning {
		return fmt.Errorf("%w: already running", ErrBadState)
	}
	src, err := s.getSource(ctx, m.SourceID)
	if err != nil {
		return err
	}
	s.tuneLimiter(src.ID, s.rate(m.Options)) // the only place a source's rate changes
	if m.Discovery == nil {
		rep, derr := s.Discover(ctx, src.ID)
		if derr != nil {
			return derr
		}
		m.Discovery = rep
	}
	mp := m.Mapping.normalized(m.Discovery)
	mb, _ := json.Marshal(mp)
	source, err := s.openSourceFor(ctx, m, src)
	if err != nil {
		return err
	}
	site, err := source.Site(ctx)
	if err != nil {
		return err
	}
	if site.URL == "" {
		site.URL = src.URL
	}
	now := db.Now()
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE migrations SET status = 'running', dry_run = ?, run = run + 1, fetched_through = -1,
			cursor = cursor + 1, current_step = ?, current_phase = 0, mapping_json = ?, started_at = ?, finished_at = NULL,
			updated_at = ? WHERE id = ?`, dry, steps[0], string(mb), now, now, m.ID); err != nil {
			return err
		}
		if !dry {
			if _, err := tx.ExecContext(ctx, `UPDATE migration_items SET status = 'pending', would_json = NULL, updated_at = ?
				WHERE migration_id = ? AND status = 'preview'`, now, m.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("wpmigrate: start: %w", err)
	}
	if err := s.setStats(ctx, m.ID, "site", site); err != nil {
		return err
	}
	action := "wpmigrate.start"
	if dry {
		action = "wpmigrate.preview"
	}
	s.audit.Record(ctx, action, "migration", m.ID, map[string]any{"source_id": m.SourceID})
	s.logEntry(ctx, m.ID, "info", "", action, map[string]any{"dry_run": dry})
	return s.enqueueStep(ctx, m.ID, steps[0], m.Cursor+1)
}

func (s *service) enqueueStep(ctx context.Context, mid, step string, cursor int) error {
	_, err := s.q.Enqueue(ctx, jobStep, stepPayload{MigrationID: mid, Step: step, Cursor: cursor})
	return err
}

// Pause stops processing; item jobs already queued leave their items
// pending.
func (s *service) Pause(ctx context.Context, migrationID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE migrations SET status = 'paused', updated_at = ? WHERE id = ? AND status = 'running'`,
		db.Now(), migrationID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.getMigration(ctx, migrationID); err != nil {
			return err
		}
		return fmt.Errorf("%w: not running", ErrBadState)
	}
	s.audit.Record(ctx, "wpmigrate.pause", "migration", migrationID, nil)
	s.logEntry(ctx, migrationID, "info", "", "wpmigrate.pause", nil)
	return nil
}

// Resume continues a paused migration from its current step.
func (s *service) Resume(ctx context.Context, migrationID string) error {
	m, err := s.getMigration(ctx, migrationID)
	if err != nil {
		return err
	}
	if m.Status != StatusPaused {
		return fmt.Errorf("%w: not paused", ErrBadState)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE migrations SET status = 'running', cursor = cursor + 1, updated_at = ? WHERE id = ?`,
		db.Now(), m.ID); err != nil {
		return err
	}
	s.audit.Record(ctx, "wpmigrate.resume", "migration", m.ID, nil)
	s.logEntry(ctx, m.ID, "info", "", "wpmigrate.resume", map[string]any{"step": m.Step})
	return s.enqueueStep(ctx, m.ID, m.Step, m.Cursor+1)
}

// RetryFailed requeues failed items and continues from the earliest step
// that has pending work. Steps already fetched in this run reuse their
// stored payloads; a step whose fetch never completed (paused before it,
// or its fetch failed) fetches again. On a paused migration it is Resume
// plus requeueing the failed items.
func (s *service) RetryFailed(ctx context.Context, migrationID string) error {
	m, err := s.getMigration(ctx, migrationID)
	if err != nil {
		return err
	}
	if m.Status == StatusRunning {
		return fmt.Errorf("%w: running", ErrBadState)
	}
	now := db.Now()
	res, err := s.db.ExecContext(ctx, `UPDATE migration_items SET status = 'pending', error = '', updated_at = ?
		WHERE migration_id = ? AND status = 'failed'`, now, m.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	step := m.Step
	for _, st := range steps {
		if k := stepKind[st]; k != "" && s.countPending(ctx, m.ID, k, -1) > 0 {
			step = st
			break
		}
	}
	if step == "" || (n == 0 && m.Status != StatusFailed && m.Status != StatusPaused) {
		return fmt.Errorf("%w: nothing to retry", ErrBadState)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE migrations SET status = 'running', cursor = cursor + 1,
		current_step = ?, current_phase = 0, finished_at = NULL, updated_at = ? WHERE id = ?`, step, now, m.ID); err != nil {
		return err
	}
	s.audit.Record(ctx, "wpmigrate.retry", "migration", m.ID, map[string]any{"items": n})
	s.logEntry(ctx, m.ID, "info", "", "wpmigrate.retry", map[string]any{"items": n, "step": step})
	return s.enqueueStep(ctx, m.ID, step, m.Cursor+1)
}

// Preview runs the whole pipeline as a dry run, synchronously: items are
// recorded with status preview and a "would" plan; nothing else is
// written.
func (s *service) Preview(ctx context.Context, migrationID string) (*PreviewResult, error) {
	q := newInlineQueue()
	cp := q.bind(s)
	if err := cp.begin(ctx, migrationID, true); err != nil {
		return nil, err
	}
	if _, err := q.drain(ctx, 0); err != nil {
		return nil, err
	}
	return s.previewResult(ctx, migrationID)
}

// --- job handlers -------------------------------------------------------------

func (s *service) handleStep(ctx context.Context, raw json.RawMessage) error {
	var p stepPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	m, err := s.getMigration(ctx, p.MigrationID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.Status != StatusRunning || m.Cursor != p.Cursor || m.Step != p.Step {
		return nil
	}
	if p.Step == "validate" {
		return s.finish(ctx, m)
	}
	if err := s.generate(ctx, m, p.Step); err != nil {
		s.logEntry(ctx, m.ID, "error", "", "step_failed", map[string]any{"step": p.Step, "error": err.Error()})
		_, uerr := s.db.ExecContext(ctx, `UPDATE migrations SET status = 'failed', updated_at = ? WHERE id = ? AND cursor = ?`,
			db.Now(), m.ID, m.Cursor)
		return uerr
	}
	return s.transition(ctx, m)
}

func (s *service) handleAdvance(ctx context.Context, raw json.RawMessage) error {
	var p advancePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	m, err := s.getMigration(ctx, p.MigrationID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.Status != StatusRunning || m.Cursor != p.Cursor {
		return nil
	}
	if k := stepKind[m.Step]; k != "" && s.countPending(ctx, m.ID, k, m.Phase) > 0 {
		return nil
	}
	return s.transition(ctx, m)
}

// transition moves m (at its cursor) to the next phase with pending items
// of the current step, or to the next step.
func (s *service) transition(ctx context.Context, m *migrationRow) error {
	kind := stepKind[m.Step]
	if kind != "" {
		if phase, ok := s.minPendingPhase(ctx, m.ID, kind); ok {
			if !s.cas(ctx, m, m.Step, phase) {
				return nil
			}
			ids, err := s.pendingIDs(ctx, m.ID, kind, phase)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if _, err := s.q.Enqueue(ctx, jobItem, itemPayload{MigrationID: m.ID, ItemID: id}); err != nil {
					return err
				}
			}
			return nil
		}
	}
	i := slices.Index(steps, m.Step)
	next := steps[min(i+1, len(steps)-1)]
	if !s.cas(ctx, m, next, 0) {
		return nil
	}
	return s.enqueueStep(ctx, m.ID, next, m.Cursor+1)
}

// cas advances the cursor if nobody else did.
func (s *service) cas(ctx context.Context, m *migrationRow, step string, phase int) bool {
	res, err := s.db.ExecContext(ctx, `UPDATE migrations SET cursor = cursor + 1, current_step = ?, current_phase = ?, updated_at = ?
		WHERE id = ? AND cursor = ? AND status = 'running'`, step, phase, db.Now(), m.ID, m.Cursor)
	if err != nil {
		s.log.Error("wpmigrate: transition", "err", err)
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// finish ends the run: validation (real runs), then the final status.
func (s *service) finish(ctx context.Context, m *migrationRow) error {
	status := StatusPreviewed
	if !m.DryRun {
		s.applySiteSettings(ctx, m)
		status = StatusCompleted
		if _, err := s.Validate(ctx, m.ID); err != nil {
			s.logEntry(ctx, m.ID, "error", "", "validate_failed", map[string]any{"error": err.Error()})
		} else {
			status = StatusValidated
		}
	}
	now := db.Now()
	// A terminal status stops browser polling; publish it only after the
	// report is stored, and do not overwrite a pause or a newer run.
	res, err := s.db.ExecContext(ctx, `UPDATE migrations SET status = ?, finished_at = ?, updated_at = ? WHERE id = ? AND cursor = ? AND status = 'running'`,
		status, now, now, m.ID, m.Cursor)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return nil
	}
	s.dropRewriters(m.ID)
	if src, err := s.getSource(ctx, m.SourceID); err == nil && src.Kind == "wxr" {
		s.wxr.evict(src.filePath)
	}
	s.logEntry(ctx, m.ID, "info", "", "wpmigrate.finished", map[string]any{"status": status})
	s.audit.Record(ctx, "wpmigrate.finish", "migration", m.ID, map[string]any{"status": status})
	return nil
}

func (s *service) handleItem(ctx context.Context, raw json.RawMessage) error {
	var p itemPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if _, busy := s.inflight.LoadOrStore(p.ItemID, struct{}{}); busy {
		return nil
	}
	defer s.inflight.Delete(p.ItemID)
	m, err := s.getMigration(ctx, p.MigrationID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.Status != StatusRunning {
		return nil
	}
	it, err := s.getItem(ctx, p.ItemID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if it.Status == ItemPending {
		s.process(ctx, m, it)
		if err := s.finishItem(ctx, it); err != nil {
			return err
		}
	}
	if k := stepKind[m.Step]; k == it.Kind && s.countPending(ctx, m.ID, k, m.Phase) == 0 {
		_, err := s.q.Enqueue(ctx, jobAdvance, advancePayload{MigrationID: m.ID, Cursor: m.Cursor})
		return err
	}
	return nil
}

// process imports one item, recording the outcome on it (never returns an
// error: failures are item status "failed" and are retried on demand).
func (s *service) process(ctx context.Context, m *migrationRow, it *itemRow) {
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("panic: %v", p)
			}
		}()
		switch it.Kind {
		case "user":
			err = s.importUser(ctx, m, it)
		case "term":
			err = s.importTerm(ctx, m, it)
		case "media":
			err = s.importMedia(ctx, m, it)
		case "content":
			err = s.importContent(ctx, m, it)
		case "product":
			err = s.importProduct(ctx, m, it)
		case "menu":
			err = s.importMenu(ctx, m, it)
		case "redirect":
			err = s.importRedirect(ctx, m, it)
		case "rewrite":
			err = s.rewriteItem(ctx, m, it)
		default:
			err = fmt.Errorf("unknown item kind %q", it.Kind)
		}
	}()
	switch {
	case err != nil:
		it.Status, it.Error = ItemFailed, err.Error()
		s.logEntry(ctx, m.ID, "error", it.ID, "item_failed", map[string]any{"kind": it.Kind, "wp_id": it.WPID, "error": err.Error()})
	case it.Status == ItemPending:
		if m.DryRun {
			it.Status = ItemPreview
		} else {
			it.Status = ItemDone
		}
	}
	if it.Status == ItemDone && it.Kind == "content" {
		s.hooks.Emit(ctx, "migration.item.imported", map[string]any{"migration_id": m.ID, "kind": it.Kind,
			"wp_id": it.WPID, "local_id": it.LocalID})
	}
}

// --- queries --------------------------------------------------------------------

// countPending counts pending items of kind (any phase when phase < 0).
func (s *service) countPending(ctx context.Context, mid, kind string, phase int) int {
	q := `SELECT count(*) FROM migration_items WHERE migration_id = ? AND kind = ? AND status = 'pending'`
	args := []any{mid, kind}
	if phase >= 0 {
		q += ` AND phase = ?`
		args = append(args, phase)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		s.log.Error("wpmigrate: count pending", "err", err)
	}
	return n
}

func (s *service) minPendingPhase(ctx context.Context, mid, kind string) (int, bool) {
	var phase *int
	if err := s.db.QueryRowContext(ctx, `SELECT min(phase) FROM migration_items WHERE migration_id = ? AND kind = ? AND status = 'pending'`,
		mid, kind).Scan(&phase); err != nil || phase == nil {
		return 0, false
	}
	return *phase, true
}

func (s *service) pendingIDs(ctx context.Context, mid, kind string, phase int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM migration_items WHERE migration_id = ? AND kind = ? AND phase = ?
		AND status = 'pending' ORDER BY rowid`, mid, kind, phase)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// applySiteSettings writes the WordPress site title and tagline (real runs
// only; the front page is set by the content step). A key is written only
// when it is empty or still holds the value a previous run of this source
// wrote, so admin edits are never clobbered. Written values are kept in
// stats_json.settings_written.
func (s *service) applySiteSettings(ctx context.Context, m *migrationRow) {
	var site SiteInfo
	if raw, ok := m.Stats["site"]; !ok || json.Unmarshal(raw, &site) != nil || s.settings == nil {
		return
	}
	written, kept := map[string]string{}, map[string]string{}
	for _, kv := range [][2]string{{"site.title", site.Name}, {"site.tagline", site.Description}} {
		key, val := kv[0], strings.TrimSpace(kv[1])
		if val == "" {
			continue
		}
		cur := s.settings.GetString(ctx, key, "")
		if cur != "" && cur != val && !s.wroteBefore(ctx, m, key, cur) {
			kept[key] = cur
			continue
		}
		if cur != val {
			if err := s.settings.Set(ctx, key, val); err != nil {
				s.logEntry(ctx, m.ID, "error", "", "settings_failed", map[string]any{"key": key, "error": err.Error()})
				continue
			}
		}
		written[key] = val
	}
	if len(written) > 0 {
		if err := s.setStats(ctx, m.ID, "settings_written", written); err != nil {
			s.log.Error("wpmigrate: stats", "err", err)
		}
		s.audit.Record(ctx, "wpmigrate.settings", "migration", m.ID, written)
		s.logEntry(ctx, m.ID, "info", "", "settings_imported", map[string]any{"settings": written})
	}
	if len(kept) > 0 {
		s.logEntry(ctx, m.ID, "info", "", "settings_kept", map[string]any{"settings": kept})
	}
}

// wroteBefore reports whether a migration of m's source wrote val to key.
func (s *service) wroteBefore(ctx context.Context, m *migrationRow, key, val string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM migrations WHERE source_id = ?
		AND json_extract(stats_json, '$.settings_written."'||?||'"') = ?`, m.SourceID, key, val).Scan(&n)
	return n > 0
}
