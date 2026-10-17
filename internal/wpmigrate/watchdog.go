package wpmigrate

import (
	"context"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

const (
	watchdogEvery = 5 * time.Minute
	stuckAfter    = 10 * time.Minute
)

// watchdog fails items of a running migration's current step and phase
// that have been pending for over stuckAfter with no queued or running job
// (a lost job would otherwise stall the run forever), then lets the run
// advance. Runs every watchdogEvery (jobs cron).
func (s *service) watchdog(ctx context.Context) error {
	cutoff := db.FormatTime(time.Now().Add(-stuckAfter))
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.migration_id, m.cursor FROM migration_items i
		JOIN migrations m ON m.id = i.migration_id
		WHERE m.status = 'running' AND i.status = 'pending' AND i.phase = m.current_phase AND i.updated_at < ?
		AND i.kind = CASE m.current_step WHEN 'users' THEN 'user' WHEN 'terms' THEN 'term' WHEN 'media' THEN 'media'
			WHEN 'content' THEN 'content' WHEN 'products' THEN 'product' WHEN 'menus' THEN 'menu'
			WHEN 'redirects' THEN 'redirect' WHEN 'rewrite' THEN 'rewrite' ELSE '' END
		AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.kind = 'migration.item' AND j.status IN ('queued', 'running')
			AND json_extract(j.payload_json, '$.item_id') = i.id)`, cutoff)
	if err != nil {
		return err
	}
	type stuck struct {
		item, migration string
		cursor          int
	}
	var list []stuck
	for rows.Next() {
		var st stuck
		if err := rows.Scan(&st.item, &st.migration, &st.cursor); err != nil {
			rows.Close()
			return err
		}
		list = append(list, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	advance := map[string]int{}
	for _, st := range list {
		if _, busy := s.inflight.Load(st.item); busy {
			continue
		}
		res, err := s.db.ExecContext(ctx, `UPDATE migration_items SET status = 'failed', error = 'stuck', updated_at = ?
			WHERE id = ? AND status = 'pending'`, db.Now(), st.item)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			s.logEntry(ctx, st.migration, "error", st.item, "item_stuck", map[string]any{"after": stuckAfter.String()})
			advance[st.migration] = st.cursor
		}
	}
	for mid, cursor := range advance {
		if _, err := s.q.Enqueue(ctx, jobAdvance, advancePayload{MigrationID: mid, Cursor: cursor}); err != nil {
			return err
		}
	}
	return nil
}
