package admin

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// The notifications centre derives its items from module tables through
// kernel.NotificationSource (permission-checked here, filtered further by
// each source) and stores only per-user read state in notifications_read.
const (
	notifyPerSource = 20
	notifyMax       = 50
	notifyMaxKey    = 200
	notifyWindow    = 7 * 24 * time.Hour
	notifyReadTTL   = 60 * 24 * time.Hour
	notifyPage      = "/admin/notifications"
)

// notifyItem is a notification prepared for templates.
type notifyItem struct {
	kernel.Notification
	Unread bool
}

// notifyData is the computed list for one request.
type notifyData struct {
	Items  []notifyItem
	Unread int
}

// notifications computes the current user's items once per call: each
// permitted source loads at most notifyPerSource rows (bounded queries), the
// merged list is capped at notifyMax and joined with the read state.
func (h *handlers) notifications(ctx context.Context) (notifyData, error) {
	var out notifyData
	u := auth.UserFromContext(ctx)
	if u == nil {
		return out, nil
	}
	var all []kernel.Notification
	for _, n := range gatherNotifications(ctx, h.d) {
		all = append(all, n.Notification)
	}
	if len(all) == 0 {
		return out, nil
	}
	keys := make([]any, 0, len(all)+1)
	keys = append(keys, u.ID)
	for _, n := range all {
		keys = append(keys, n.Key)
	}
	rows, err := h.d.DB.QueryContext(ctx, `SELECT key FROM notifications_read WHERE user_id = ? AND key IN (?`+strings.Repeat(",?", len(all)-1)+`)`, keys...)
	if err != nil {
		return out, err
	}
	read := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return out, err
		}
		read[k] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, n := range all {
		item := notifyItem{Notification: n, Unread: !read[n.Key]}
		if item.Unread {
			out.Unread++
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// categorized is a notification with its source's category.
type categorized struct {
	kernel.Notification
	Category string
}

// gatherNotifications computes the items of the user in ctx: each source
// whose permission the user holds loads at most notifyPerSource rows; the
// merged list is newest first and capped at notifyMax. The bell and the
// delivery run (acting as the recipient) share it, so both apply the same
// permission rules at the moment they run.
func gatherNotifications(ctx context.Context, d *kernel.Deps) []categorized {
	var all []categorized
	for _, src := range d.Admin.NotificationSources() {
		if !auth.Can(ctx, src.Permission) {
			continue
		}
		items, err := src.Load(ctx, notifyPerSource)
		if err != nil {
			// One failing module must not hide the others.
			d.Log.Error("admin: notification source", "source", src.ID, "err", err)
			continue
		}
		if len(items) > notifyPerSource {
			items = items[:notifyPerSource]
		}
		for _, n := range items {
			all = append(all, categorized{Notification: n, Category: src.Category})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At > all[j].At })
	if len(all) > notifyMax {
		all = all[:notifyMax]
	}
	return all
}

func (h *handlers) notificationRoutes() {
	r := h.d.Router.Admin
	r.Get("/notifications", h.notificationsPage)
	r.Get("/notifications/go", h.notificationOpen)
	r.Post("/notifications/read", h.notificationRead)
	r.Post("/notifications/read-all", h.notificationReadAll)
	r.Get("/x/notifications/count", h.notificationCount)
	r.Get("/x/notifications/panel", h.notificationPanel)
	h.registerNotificationSources()
	h.d.Jobs.Cron("notifications.prune", 24*time.Hour, func(ctx context.Context) error {
		_, err := h.d.DB.ExecContext(ctx, `DELETE FROM notifications_read WHERE read_at < ?`, db.FormatTime(time.Now().Add(-notifyReadTTL)))
		return err
	})
}

func (h *handlers) notificationsPage(w http.ResponseWriter, r *http.Request) {
	data, err := h.notifications(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.Render(w, r, "pages/notifications.html", data)
}

func (h *handlers) notificationCount(w http.ResponseWriter, r *http.Request) {
	data, err := h.notifications(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.RenderPartial(w, r, "partials/notifications-count", data)
}

func (h *handlers) notificationPanel(w http.ResponseWriter, r *http.Request) {
	data, err := h.notifications(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	h.tpl.RenderPartial(w, r, "partials/notifications-panel", data)
}

// markRead stores read state for keys that are currently visible to the
// user (anything else is ignored, so the table holds only real items).
func (h *handlers) markRead(ctx context.Context, data notifyData, only string) error {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil
	}
	now := db.Now()
	for _, it := range data.Items {
		if !it.Unread || (only != "" && it.Key != only) {
			continue
		}
		if _, err := h.d.DB.ExecContext(ctx, `INSERT INTO notifications_read (user_id, key, read_at) VALUES (?, ?, ?)
			ON CONFLICT (user_id, key) DO NOTHING`, u.ID, it.Key, now); err != nil {
			return err
		}
	}
	return nil
}

// notificationRespond answers a mark-read: the refreshed panel for HTMX
// (plus a notify-refresh event for the bell count), else back to the page.
func (h *handlers) notificationRespond(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "notify-refresh")
		h.notificationPanel(w, r)
		return
	}
	http.Redirect(w, r, notifyPage, http.StatusSeeOther)
}

func (h *handlers) notificationRead(w http.ResponseWriter, r *http.Request) {
	key := r.FormValue("key")
	if key == "" || len(key) > notifyMaxKey {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	data, err := h.notifications(r.Context())
	if err == nil {
		err = h.markRead(r.Context(), data, key)
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.notificationRespond(w, r)
}

func (h *handlers) notificationReadAll(w http.ResponseWriter, r *http.Request) {
	data, err := h.notifications(r.Context())
	if err == nil {
		err = h.markRead(r.Context(), data, "")
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.notificationRespond(w, r)
}

// notificationOpen marks one item read and redirects to where to act. The
// target comes from the derived item, never from the request.
func (h *handlers) notificationOpen(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	data, err := h.notifications(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	for _, it := range data.Items {
		if it.Key != key {
			continue
		}
		if err := h.markRead(r.Context(), data, key); err != nil {
			h.serverError(w, r, err)
			return
		}
		target := it.Href
		if !strings.HasPrefix(target, "/admin") || strings.HasPrefix(target, "//") {
			target = notifyPage
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	httpx.SetFlash(w, "info", "notifications.gone")
	http.Redirect(w, r, notifyPage, http.StatusSeeOther)
}
