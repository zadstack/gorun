package content

import (
	"context"
	"database/sql"

	"github.com/zadstack/gorun/internal/auth"
)

// DraftCreator atomically saves a new draft and its caller-owned provenance.
// The callback participates in the same transaction as content, revisions and audit.
// It must not perform network calls or start another database transaction.
type DraftCreator interface {
	CreateDraftWith(context.Context, Input, func(*sql.Tx, *Item) error) (*Item, error)
}

func (s *service) CreateDraftWith(ctx context.Context, in Input, provenance func(*sql.Tx, *Item) error) (*Item, error) {
	u := auth.UserFromContext(ctx)
	if u == nil || !auth.Can(ctx, "content.create") {
		return nil, ErrForbidden
	}
	in.Status, in.AuthorID, in.PublishAt = "draft", u.ID, ""
	return s.save(ctx, nil, in, saveOpts{afterWrite: func(tx *sql.Tx, item *Item) error {
		var authorized int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users u WHERE u.id=? AND u.status='active' AND u.onboarding_required=0 AND EXISTS(SELECT 1 FROM role_permissions WHERE role=u.role AND permission IN ('*','content.create'))`, u.ID).Scan(&authorized); err != nil {
			return err
		}
		if authorized != 1 {
			return ErrForbidden
		}
		// Filters cannot turn this human-review action into publication or reassignment.
		if item.Status != "draft" || item.AuthorID != u.ID || item.PublishAt != "" {
			return ErrConflict
		}
		if provenance != nil {
			return provenance(tx, item)
		}
		return nil
	}})
}
