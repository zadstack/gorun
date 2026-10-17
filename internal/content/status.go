package content

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

// canPublish reports whether ctx may publish (true without a user).
func canPublish(ctx context.Context) bool {
	return auth.UserFromContext(ctx) == nil || auth.Can(ctx, "content.publish")
}

func isLive(st Status) bool { return st == "published" || st == "scheduled" || st == "private" }

// applyStatus normalizes status, publish_at and published_at: published
// with a future publish_at becomes scheduled, scheduled with a past one
// becomes published; going live needs content.publish.
func (s *service) applyStatus(ctx context.Context, existing, it *Item, publishAt string, now time.Time, errs FieldErrors) error {
	if it.Status == "" {
		it.Status = "draft"
	}
	if !slices.Contains(Statuses, it.Status) {
		errs["status"] = "content.validation.status"
		return nil
	}
	// Trashing goes through Delete (content.delete, content.deleted hook);
	// Create/Update may only keep an already trashed item in the trash.
	if it.Status == "trash" && (existing == nil || existing.Status != "trash") {
		errs["status"] = "content.validation.status_trash"
		return nil
	}
	// Un-trashing restores a deleted item: like deleting, it needs
	// content.delete (system callers without a user may).
	if existing != nil && existing.Status == "trash" && it.Status != "trash" &&
		auth.UserFromContext(ctx) != nil && !auth.Can(ctx, "content.delete") {
		errs["status"] = "content.validation.untrash_forbidden"
		return nil
	}
	var at time.Time
	if strings.TrimSpace(publishAt) != "" {
		ts, ok := ParseDatetime(publishAt)
		if !ok {
			errs["publish_at"] = "content.validation.datetime"
			return nil
		}
		at = ts
		it.PublishAt = db.FormatTime(ts)
	}
	switch {
	case it.Status == "published" && !at.IsZero() && at.After(now):
		it.Status = "scheduled"
	case it.Status == "scheduled" && at.IsZero():
		errs["publish_at"] = "content.validation.required"
		return nil
	case it.Status == "scheduled" && !at.After(now):
		it.Status = "published"
	}
	if isLive(it.Status) && !canPublish(ctx) && (existing == nil || existing.Status != it.Status) {
		return ErrForbidden
	}
	if it.Status == "published" && it.PublishedAt == "" {
		if !at.IsZero() {
			it.PublishedAt = it.PublishAt
		} else {
			it.PublishedAt = db.FormatTime(now)
		}
	}
	return nil
}

// Delete moves the item to trash, or removes it (hard). Emits
// content.deleted {id, type, hard}.
func (s *service) Delete(ctx context.Context, id string, hard bool) error {
	return s.deleteWithTokens(ctx, id, hard, nil)
}

type editorTokens struct{ content, terms string }

func (s *service) deleteWithTokens(ctx context.Context, id string, hard bool, expected *editorTokens) error {
	it, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := checkOwner(ctx, it); err != nil {
		return err
	}
	if expected != nil && (expected.content == "" || expected.terms == "" || EditToken(it) != expected.content || termsEditToken(it.Terms) != expected.terms) {
		return ErrConflict
	}
	if !hard && it.Status == "trash" {
		return nil
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if expected != nil {
			current, err := s.getItem(ctx, tx, id)
			if err != nil {
				return err
			}
			if err = checkOwner(ctx, current); err != nil {
				return err
			}
			if EditToken(current) != expected.content {
				return ErrConflict
			}
			current.Terms, err = s.readTermsTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if termsEditToken(current.Terms) != expected.terms {
				return ErrConflict
			}
			it = current
		}
		if hard {
			if _, err := tx.ExecContext(ctx, `DELETE FROM content WHERE id = ?`, id); err != nil {
				return fmt.Errorf("content: delete %s: %w", id, err)
			}
			if err := unindexSearchTx(ctx, tx, id); err != nil {
				return err
			}
			if err := pruneGroupTx(ctx, tx, it.TranslationGroup); err != nil {
				return err
			}
			s.audit.RecordTx(ctx, tx, "content.delete", "content", id, map[string]any{"type": it.Type, "title": it.Title})
			return nil
		}
		now := s.nowString()
		if _, err := tx.ExecContext(ctx, `UPDATE content SET status = 'trash', deleted_at = ?, updated_at = ? WHERE id = ?`, now, now, id); err != nil {
			return fmt.Errorf("content: trash %s: %w", id, err)
		}
		if it.TranslationGroup != "" {
			if err := leaveGroupTx(ctx, tx, id, it.TranslationGroup); err != nil {
				return err
			}
		}
		s.audit.RecordTx(ctx, tx, "content.trash", "content", id, map[string]any{"type": it.Type, "title": it.Title})
		return nil
	})
	if err != nil {
		return err
	}
	if s.hooks != nil {
		s.hooks.Emit(ctx, "content.deleted", map[string]any{"id": id, "type": it.Type, "slug": it.Slug, "hard": hard})
	}
	return nil
}

// errTrashed: publish/unpublish of a trashed item (restore it first).
var errTrashed = &ValidationError{Fields: FieldErrors{"status": "content.validation.trashed"}}

// errNotScheduled: a scheduled publish found the item no longer scheduled.
var errNotScheduled = errors.New("content: item is no longer scheduled")

// Publish makes the item published now (a future publish_at is cleared).
// Trashed items must be restored first.
func (s *service) Publish(ctx context.Context, id string) error {
	it, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := checkOwner(ctx, it); err != nil {
		return err
	}
	if !canPublish(ctx) {
		return ErrForbidden
	}
	if it.Status == "trash" {
		return errTrashed
	}
	return s.setLive(ctx, it, "content.publish", false)
}

// setLive marks it published (keeping a due publish_at as the publish
// date), snapshots a revision and fires the hooks. With onlyScheduled the
// update applies only if the row is still scheduled and not trashed
// (errNotScheduled otherwise); trashed rows are never touched.
func (s *service) setLive(ctx context.Context, it *Item, action string, onlyScheduled bool) error {
	before := *it
	now := s.now()
	if at, ok := ParseDatetime(it.PublishAt); ok && at.After(now) {
		it.PublishAt = ""
	}
	it.Status = "published"
	if it.PublishedAt == "" || before.Status == "scheduled" {
		it.PublishedAt = db.FormatTime(now)
		if it.PublishAt != "" {
			it.PublishedAt = it.PublishAt
		}
	}
	it.UpdatedAt = db.FormatTime(now)
	keep := s.revisionsKeep(ctx)
	guard := ` AND status != 'trash' AND deleted_at IS NULL`
	if onlyScheduled {
		guard = ` AND status = 'scheduled' AND deleted_at IS NULL`
	}
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE content SET status = 'published', publish_at = ?, published_at = ?,
			updated_at = ? WHERE id = ?`+guard, nullable(it.PublishAt), it.PublishedAt, it.UpdatedAt, it.ID)
		if err != nil {
			return fmt.Errorf("content: publish %s: %w", it.ID, err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			if onlyScheduled {
				return errNotScheduled
			}
			return errTrashed
		}
		if err := s.addRevision(ctx, tx, it, keep); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, action, "content", it.ID, map[string]any{"type": it.Type, "title": it.Title})
		return nil
	})
	if err != nil {
		return err
	}
	s.afterSave(ctx, &before, it)
	return nil
}

// Unpublish returns the item to draft (needs content.publish, like
// Publish). Trashed items are refused.
func (s *service) Unpublish(ctx context.Context, id string) error {
	it, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := checkOwner(ctx, it); err != nil {
		return err
	}
	if !canPublish(ctx) {
		return ErrForbidden
	}
	if it.Status == "trash" {
		return errTrashed
	}
	before := *it
	it.Status, it.UpdatedAt = "draft", s.nowString()
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE content SET status = 'draft', updated_at = ? WHERE id = ? AND deleted_at IS NULL`, it.UpdatedAt, id); err != nil {
			return fmt.Errorf("content: unpublish %s: %w", id, err)
		}
		s.audit.RecordTx(ctx, tx, "content.unpublish", "content", id, map[string]any{"type": it.Type})
		return nil
	})
	if err != nil {
		return err
	}
	s.afterSave(ctx, &before, it)
	return nil
}
