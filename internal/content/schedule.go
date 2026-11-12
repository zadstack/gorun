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
	// Like publishDue: one item that fails to delete (a locked database, a
	// hook error) must not keep every other expired item in the trash.
	for _, id := range ids {
		if err := s.Delete(ctx, id, true); err != nil {
			s.log.Error("content: purge trash failed", "id", id, "err", err)
		}
	}
	return nil
}

const (
	// previewLinkRetention keeps expired and revoked preview links (and
	// their audit trail by id) for this long before they are deleted.
	previewLinkRetention = 30 * 24 * time.Hour
	// autosaveRetention bounds how long an untouched autosave is kept.
	autosaveRetention = 30 * 24 * time.Hour
	// staleAutosaveRetention bounds an autosave whose item was saved since
	// its base version. The editor still offers it for a while (the restore
	// banner marks it as older than the item), so it is not deleted at once.
	staleAutosaveRetention = 7 * 24 * time.Hour
)

// cleanupStale deletes rows nothing needs any more (cron "content.cleanup",
// daily): preview links that expired or were revoked more than 30 days ago,
// autosaves not touched for 30 days, and autosaves older than 7 days whose
// item was saved since their base version. It logs what it removed.
func (s *service) cleanupStale(ctx context.Context, now time.Time) error {
	linkCutoff := db.FormatTime(now.Add(-previewLinkRetention))
	res, err := s.db.ExecContext(ctx, `DELETE FROM content_preview_links
		WHERE expires_at <= ? OR (revoked_at IS NOT NULL AND revoked_at <= ?)`, linkCutoff, linkCutoff)
	if err != nil {
		return fmt.Errorf("content: clean preview links: %w", err)
	}
	links, _ := res.RowsAffected()
	res, err = s.db.ExecContext(ctx, `DELETE FROM content_autosaves
		WHERE saved_at <= ?
		   OR (saved_at <= ? AND EXISTS (SELECT 1 FROM content c
		       WHERE c.id = content_autosaves.content_id AND c.updated_at <> content_autosaves.base_updated_at))`,
		db.FormatTime(now.Add(-autosaveRetention)), db.FormatTime(now.Add(-staleAutosaveRetention)))
	if err != nil {
		return fmt.Errorf("content: clean autosaves: %w", err)
	}
	autosaves, _ := res.RowsAffected()
	if links > 0 || autosaves > 0 {
		s.log.Info("content: cleanup", "preview_links", links, "autosaves", autosaves)
	}
	return nil
}
