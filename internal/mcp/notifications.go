package mcp

import (
	"context"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// registerNotifications adds pending agent approvals to the admin
// notifications centre for users who may decide them (mcp.approve; a
// session user may decide any pending call, see checkDecider).
func (s *server) registerNotifications() {
	s.d.Admin.AddNotificationSource(kernel.NotificationSource{ID: "mcp.approvals", Permission: PermApprove, Category: kernel.NotifyApprovals,
		Load: func(ctx context.Context, limit int) ([]kernel.Notification, error) {
			rows, err := s.d.DB.QueryContext(ctx, `SELECT id, tool, key_name, requested_at FROM mcp_approvals
				WHERE status = 'pending' AND expires_at > ? ORDER BY requested_at DESC LIMIT ?`, db.Now(), limit)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var out []kernel.Notification
			for rows.Next() {
				var id, tool, key, at string
				if err := rows.Scan(&id, &tool, &key, &at); err != nil {
					return nil, err
				}
				detail := tool
				if key != "" {
					detail = tool + " · " + key
				}
				out = append(out, kernel.Notification{Key: "mcp.approval:" + id, TitleKey: "notifications.approval", Detail: detail,
					Href: approvalsPath, At: at, Icon: "shield-check", Tone: "warning"})
			}
			return out, rows.Err()
		}})
}
