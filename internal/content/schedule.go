package content

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

const defaultTrashDays = 30

// publishDue publishes every scheduled item whose publish_at is due
// (cron "content.publish_due", every 30s), emitting content.published.
func (s *service) publishDue(ctx context.Context, now time.Time) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM content WHERE status = 'scheduled' AND publish_at <= ? ORDER BY publish_at`,
		db.FormatTime(now))
	if err != nil {
		return fmt.Errorf("content: due list: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// One bad row must not block the tick: log and continue.
	for _, id := range ids {
		it, err := s.Get(ctx, id)
		if err == nil {
			err = s.setLive(ctx, it, "content.publish_scheduled", true)
		}
		switch {
		case errors.Is(err, errNotScheduled):
			s.log.Info("content: scheduled item changed before publishing", "id", id)
		case err != nil:
			s.log.Error("content: scheduled publish failed", "id", id, "err", err)
		}
	}
	return nil
}

// purgeTrash hard-deletes items trashed more than content.trash_days (30)
// days before now (cron "content.purge_trash", hourly).
func (s *service) purgeTrash(ctx context.Context, now time.Time) error {
	days := defaultTrashDays
	if s.settings != nil {
		var v int
		if s.settings.Get(ctx, "content.trash_days", &v) == nil && v > 0 {
			days = v
		}
	}
	cutoff := db.FormatTime(now.Add(-time.Duration(days) * 24 * time.Hour))
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM content WHERE status = 'trash' AND deleted_at <= ?`, cutoff)
	if err != nil {
		return fmt.Errorf("content: purge list: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.Delete(ctx, id, true); err != nil {
			return err
		}
	}
	return nil
}
