package content

import (
	"context"
	"database/sql"
	"errors"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// maxPendingCommentsCounted bounds the count shown on the comments item.
const maxPendingCommentsCounted = 99

// registerNotifications adds the editorial items of the admin notifications
// centre: content submitted for review (status pending) that the user may
// publish, and comments awaiting moderation.
func registerNotifications(d *kernel.Deps) {
	d.Admin.AddNotificationSource(kernel.NotificationSource{ID: "content.review", Permission: "content.publish",
		Load: func(ctx context.Context, limit int) ([]kernel.Notification, error) {
			return reviewNotifications(ctx, d, limit)
		}})
	d.Admin.AddNotificationSource(kernel.NotificationSource{ID: "content.comments", Permission: PermModerateComments,
		Load: func(ctx context.Context, limit int) ([]kernel.Notification, error) {
			return commentNotifications(ctx, d)
		}})
}

// reviewNotifications lists pending items of public content types written by
// someone else. Authors and contributors only publish their own content, so
// they get none.
func reviewNotifications(ctx context.Context, d *kernel.Deps, limit int) ([]kernel.Notification, error) {
	u := auth.UserFromContext(ctx)
	if u == nil || ownOnly(u) {
		return nil, nil
	}
	rows, err := d.DB.QueryContext(ctx, `SELECT c.id, c.type, c.title, c.slug, c.updated_at FROM content c
		JOIN content_types t ON t.slug = c.type AND t.public = 1
		WHERE c.status = 'pending' AND c.author_id != ?
		ORDER BY c.updated_at DESC LIMIT ?`, u.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []kernel.Notification
	for rows.Next() {
		var id, typ, title, slug, at string
		if err := rows.Scan(&id, &typ, &title, &slug, &at); err != nil {
			return nil, err
		}
		if title == "" {
			title = slug
		}
		out = append(out, kernel.Notification{Key: "content.review:" + id, TitleKey: "notifications.content_review", Detail: title,
			Href: "/admin/content/" + typ + "/" + id, At: at, Icon: "file-check", Tone: "info"})
	}
	return out, rows.Err()
}

// commentNotifications is one item for the moderation queue; its key names
// the newest pending comment, so a new comment makes it unread again.
func commentNotifications(ctx context.Context, d *kernel.Deps) ([]kernel.Notification, error) {
	var newest, at string
	err := d.DB.QueryRowContext(ctx, `SELECT id, created_at FROM comments WHERE status = 'pending' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&newest, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var n int
	if err := d.DB.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM comments WHERE status = 'pending' LIMIT ?)`, maxPendingCommentsCounted).Scan(&n); err != nil {
		return nil, err
	}
	return []kernel.Notification{{Key: "comments.pending:" + newest, TitleKey: "notifications.comments_pending", Count: n,
		Href: "/admin/comments", At: at, Icon: "message-square", Tone: "warning"}}, nil
}
