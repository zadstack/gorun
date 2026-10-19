package admin

import (
	"context"
	"net/http"
	"net/url"
	"sort"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

const canvasActivityLimit = 8

// The optional webhooks module boots after admin. Check its registered service
// and permission at request time without depending on its implementation.
const canvasActivityWebhookPermission = "webhooks.manage"

type canvasActivityEvent struct {
	Title, Body, Source, Time, Href, Kind, Status string
}
type canvasActivityPage struct {
	Events            []canvasActivityEvent
	WebhooksAvailable bool
}

// The activity projection contains only display-safe metadata. Private notes
// are always joined through the current owner; webhook payloads, URLs, secrets
// and raw errors never enter this projection.
func (h *handlers) canvasActivity(ctx context.Context) (canvasActivityPage, error) {
	var page canvasActivityPage
	owner := auth.UserFromContext(ctx)
	if owner == nil {
		return page, nil
	}
	rows, err := h.d.DB.QueryContext(ctx, `SELECT coalesce(json_extract(c.value,'$.title'),''),substr(coalesce(json_extract(c.value,'$.body'),''),1,180),p.actor_name,p.created_at,b.id,p.card_id
 FROM admin_canvas_provenance p JOIN admin_canvases b ON b.id=p.board_id JOIN json_each(b.document_json) c ON json_extract(c.value,'$.id')=p.card_id
 WHERE b.owner_id=? ORDER BY p.created_at DESC,p.card_id DESC LIMIT ?`, owner.ID, canvasActivityLimit)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var event canvasActivityEvent
		var board, card string
		if err := rows.Scan(&event.Title, &event.Body, &event.Source, &event.Time, &board, &card); err != nil {
			rows.Close()
			return page, err
		}
		event.Href = "/admin/canvases/" + url.PathEscape(board) + "#card-" + url.PathEscape(card)
		event.Kind = "note"
		event.Status = "received"
		page.Events = append(page.Events, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if _, ok := kernel.Get[any](h.d, kernel.ServiceWebhooks); ok && auth.Can(ctx, canvasActivityWebhookPermission) {
		page.WebhooksAvailable = true
		rows, err := h.d.DB.QueryContext(ctx, `SELECT w.name,d.event,d.status,d.created_at,w.id FROM webhook_deliveries d JOIN webhooks w ON w.id=d.webhook_id ORDER BY d.created_at DESC,d.id DESC LIMIT ?`, canvasActivityLimit)
		if err != nil {
			return page, err
		}
		for rows.Next() {
			var event canvasActivityEvent
			var id string
			if err := rows.Scan(&event.Title, &event.Body, &event.Status, &event.Time, &id); err != nil {
				rows.Close()
				return page, err
			}
			event.Href = "/admin/webhooks/" + url.PathEscape(id)
			event.Kind = "webhook"
			page.Events = append(page.Events, event)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return page, err
		}
	}
	sort.SliceStable(page.Events, func(i, j int) bool { return page.Events[i].Time > page.Events[j].Time })
	if len(page.Events) > canvasActivityLimit {
		page.Events = page.Events[:canvasActivityLimit]
	}
	return page, nil
}
func (h *handlers) canvasActivityFragment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !WorkspaceShowsAdvanced(r.Context(), h.d.Settings) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	page, err := h.canvasActivity(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	body, err := h.fragment(r, "pages/system/fragments/canvas_activity.html", page)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
