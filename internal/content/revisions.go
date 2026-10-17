package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

// addRevision snapshots it and prunes the item's history to the newest
// keep revisions (IDs are monotonic ULIDs, so id order is creation order).
func (s *service) addRevision(ctx context.Context, tx *sql.Tx, it *Item, keep int) error {
	snap, err := json.Marshal(it)
	if err != nil {
		return fmt.Errorf("content: encode revision: %w", err)
	}
	author := ""
	if u := auth.UserFromContext(ctx); u != nil {
		author = u.ID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO revisions (id, content_id, snapshot_json, author_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		db.NewID(), it.ID, string(snap), author, s.nowString()); err != nil {
		return fmt.Errorf("content: add revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM revisions WHERE content_id = ? AND id NOT IN
		(SELECT id FROM revisions WHERE content_id = ? ORDER BY id DESC LIMIT ?)`, it.ID, it.ID, keep); err != nil {
		return fmt.Errorf("content: prune revisions: %w", err)
	}
	return nil
}

// Revisions returns the item's revisions, newest first.
func (s *service) Revisions(ctx context.Context, id string) ([]Revision, error) {
	it, err := s.getItem(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	// A published item can still have confidential drafts in its history.
	if err := checkOwner(ctx, it); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, content_id, author_id, created_at, snapshot_json FROM revisions
		WHERE content_id = ? ORDER BY id DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("content: list revisions: %w", err)
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var r Revision
		var snap string
		if err := rows.Scan(&r.ID, &r.ContentID, &r.AuthorID, &r.CreatedAt, &snap); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(snap), &r.Snapshot); err != nil {
			return nil, fmt.Errorf("content: decode revision %s: %w", r.ID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Restore snapshots the current state as a new revision, then replaces the
// item's content (title, slug, excerpt, body, fields, seo, template, parent,
// menu order, featured media, terms) with the revision's. Status, author,
// locale and publish dates stay as they are now.
func (s *service) Restore(ctx context.Context, id, revisionID string) (*Item, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkOwner(ctx, cur); err != nil {
		return nil, err
	}
	var snap string
	err = s.db.QueryRowContext(ctx, `SELECT snapshot_json FROM revisions WHERE id = ? AND content_id = ?`, revisionID, id).Scan(&snap)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("content: get revision: %w", err)
	}
	var old Item
	if err := json.Unmarshal([]byte(snap), &old); err != nil {
		return nil, fmt.Errorf("content: decode revision: %w", err)
	}
	keep := s.revisionsKeep(ctx)
	in := InputFromItem(cur)
	in.Slug, in.Title, in.Excerpt, in.Body, in.Fields, in.SEO = old.Slug, old.Title, old.Excerpt, old.Body, old.Fields, old.SEO
	in.Template, in.ParentID, in.FeaturedMediaID, in.Terms = old.Template, old.ParentID, old.FeaturedMediaID, old.Terms
	order := old.MenuOrder
	in.MenuOrder = &order
	// One transaction: snapshot of the current state, then the restored
	// save. Stale relationship ids in the old snapshot are dropped.
	return s.save(ctx, cur, in, saveOpts{
		prev:   []map[string]any{old.Fields},
		inTx:   func(tx *sql.Tx) error { return s.addRevision(ctx, tx, cur, keep) },
		action: "content.restore",
	})
}
