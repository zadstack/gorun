package content

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zadstack/gorun/internal/db"
)

// PublishExpectation guards reviewed content against changes before activation.
type PublishExpectation struct {
	ID, UpdatedAt string
	Body          *string
	Template      string
}

var ErrPublishConflict = errors.New("content: reviewed content has changed")

// BatchPublisher publishes a reviewed collection and companion settings atomically.
// The companion callback must use tx and must not call a pooled service method.
type BatchPublisher interface {
	PublishBatch(context.Context, []PublishExpectation, func(*sql.Tx, []*Item) error) error
}

func (s *service) PublishBatch(ctx context.Context, expected []PublishExpectation, companion func(*sql.Tx, []*Item) error) error {
	if !canPublish(ctx) {
		return ErrForbidden
	}
	if len(expected) == 0 || len(expected) > 50 {
		return fmt.Errorf("content: invalid publication batch size")
	}
	keep := s.revisionsKeep(ctx)
	before := make([]*Item, 0, len(expected))
	after := make([]*Item, 0, len(expected))
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		seen := map[string]bool{}
		for _, want := range expected {
			if seen[want.ID] || want.UpdatedAt == "" {
				return ErrPublishConflict
			}
			seen[want.ID] = true
			it, err := s.getItem(ctx, tx, want.ID)
			if err != nil {
				return err
			}
			if err := checkOwner(ctx, it); err != nil {
				return err
			}
			if EditToken(it) != want.UpdatedAt || it.Status == "trash" {
				return ErrPublishConflict
			}
			it.Terms, err = s.readTermsTx(ctx, tx, it.ID)
			if err != nil {
				return err
			}
			old := *it
			before = append(before, &old)
			if want.Body != nil {
				it.Body = Sanitize(*want.Body)
				if want.Template != "" {
					it.Template = want.Template
				}
				if _, err := tx.ExecContext(ctx, `UPDATE content SET body=?,template=? WHERE id=?`, it.Body, it.Template, it.ID); err != nil {
					return err
				}
				if err := indexSearchTx(ctx, tx, it); err != nil {
					return err
				}
			}
			it.Status = "published"
			it.PublishAt = ""
			it.UpdatedAt = s.nowString()
			if it.PublishedAt == "" || old.Status == "scheduled" {
				it.PublishedAt = it.UpdatedAt
			}
			if _, err := tx.ExecContext(ctx, `UPDATE content SET status='published',publish_at=NULL,published_at=?,updated_at=? WHERE id=?`, it.PublishedAt, it.UpdatedAt, it.ID); err != nil {
				return err
			}
			if err := s.addRevision(ctx, tx, it, keep); err != nil {
				return err
			}
			s.audit.RecordTx(ctx, tx, "content.publish", "content", it.ID, map[string]any{"type": it.Type, "title": it.Title})
			after = append(after, it)
		}
		if companion != nil {
			return companion(tx, after)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i, it := range after {
		s.afterSave(ctx, before[i], it)
	}
	return nil
}
