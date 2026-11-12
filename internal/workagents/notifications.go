package workagents

import (
	"context"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// handoffWindow limits the hand-off notifications to recent messages.
const handoffWindow = 7 * 24 * time.Hour

// registerNotifications adds advisory channel hand-offs addressed to the
// signed-in user (in their own open channels) to the admin notifications
// centre. A hand-off executes nothing; the item only links to the thread.
func registerNotifications(d *kernel.Deps) {
	d.Admin.AddNotificationSource(kernel.NotificationSource{ID: "workagents.handoffs", Permission: "workagents.manage", Category: kernel.NotifyHandoffs,
		Load: func(ctx context.Context, limit int) ([]kernel.Notification, error) {
			o := owner(ctx)
			if o == "" {
				return nil, nil
			}
			rows, err := d.DB.QueryContext(ctx, `SELECT m.id, m.channel_id, c.title, m.author_name, m.created_at
				FROM workagents_channels c JOIN workagents_channel_messages m ON m.channel_id = c.id
				WHERE c.owner_id = ? AND c.status = 'open' AND m.created_at > ? AND m.author_id != ?
				  AND json_extract(m.meta_json, '$.type') = 'handoff'
				  AND json_extract(m.meta_json, '$.target_member_kind') = 'user'
				  AND json_extract(m.meta_json, '$.target_member_id') = ?
				ORDER BY m.created_at DESC LIMIT ?`, o, db.FormatTime(time.Now().Add(-handoffWindow)), o, o, limit)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var out []kernel.Notification
			for rows.Next() {
				var id, channel, title, author, at string
				if err := rows.Scan(&id, &channel, &title, &author, &at); err != nil {
					return nil, err
				}
				detail := title
				if author != "" {
					detail = author + " · " + title
				}
				out = append(out, kernel.Notification{Key: "handoff:" + id, TitleKey: "notifications.handoff", Detail: detail,
					Href: "/admin/workagents/channels/" + channel, At: at, Icon: "forward", Tone: "info"})
			}
			return out, rows.Err()
		}})
}
