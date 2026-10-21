package commerce

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
)

// Order and entitlement statuses offered as list filters.
var (
	orderStatuses       = []string{OrderPending, OrderPaid, OrderProvisioning, OrderActive, OrderFailed, OrderRefunded, OrderCancelled}
	entitlementStatuses = []string{EntitlementPending, EntitlementActive, EntitlementSuspended, EntitlementExpired, EntitlementFailed}
)

type orderRow struct {
	Order
	Total string
}

func rowOf(o Order) orderRow {
	return orderRow{Order: o, Total: FormatPrice(o.TotalMinor, o.Currency) + " " + o.Currency}
}

type listPage struct {
	Q, Status string
	Statuses  []string
	Page      httpx.Pagination
}

func listParams(r *http.Request, statuses []string) (ListQuery, listPage) {
	q := r.URL.Query()
	lp := listPage{Q: strings.TrimSpace(q.Get("q")), Status: q.Get("status"), Statuses: statuses}
	valid := false
	for _, s := range statuses {
		valid = valid || s == lp.Status
	}
	if !valid {
		lp.Status = ""
	}
	return ListQuery{Search: lp.Q, Status: lp.Status, Page: pageParam(r), PerPage: adminPerPage}, lp
}

func (h *adminHandlers) orders(w http.ResponseWriter, r *http.Request) {
	lq, lp := listParams(r, orderStatuses)
	list, total, err := h.svc.Orders(r.Context(), lq)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rows := make([]orderRow, len(list))
	for i, o := range list {
		rows[i] = rowOf(o)
	}
	lp.Page = httpx.NewPagination(lq.Page, adminPerPage, total)
	h.tpl.Render(w, r, "pages/commerce/orders.html", map[string]any{"Orders": rows, "List": lp})
}

// orderRetry re-enqueues provisioning for a failed order.
func (h *adminHandlers) orderRetry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := h.svc.RetryProvisioning(r.Context(), id)
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.SetFlash(w, "error", "commerce.error.not_retryable")
	case err != nil:
		h.fail(w, r, err)
		return
	default:
		httpx.SetFlash(w, "success", "commerce.flash.retry_queued")
	}
	http.Redirect(w, r, "/admin/commerce/orders/"+id, http.StatusSeeOther)
}

type opRow struct {
	Kind, Key, Status, LastError, CreatedAt string
	Attempts                                int
}

func (h *adminHandlers) order(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	o, err := h.svc.Order(ctx, chi.URLParam(r, "id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ents, _, err := h.svc.Entitlements(ctx, ListQuery{CustomerID: o.CustomerID, PerPage: 100})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var mine []Entitlement
	for _, e := range ents {
		if e.OrderID == o.ID {
			mine = append(mine, e)
		}
	}
	var ops []opRow
	rows, err := h.svc.db.QueryContext(ctx, `SELECT p.kind, p.idempotency_key, p.status, p.attempts, p.last_error, p.created_at
		FROM provisioning_ops p JOIN entitlements e ON e.id = p.entitlement_id WHERE e.order_id = ? ORDER BY p.created_at, p.id`, o.ID)
	if err == nil {
		for rows.Next() {
			var op opRow
			if rows.Scan(&op.Kind, &op.Key, &op.Status, &op.Attempts, &op.LastError, &op.CreatedAt) == nil {
				ops = append(ops, op)
			}
		}
		rows.Close()
	}
	h.tpl.Render(w, r, "pages/commerce/order.html", map[string]any{"Order": rowOf(*o), "Entitlements": mine, "Ops": ops,
		"Items":     func() []OrderItem { items, _ := h.svc.orderItems(ctx, o); return items }(),
		"CanRetry":  (o.snapshot().Kind == "" || o.snapshot().Kind == ProductProvisioning) && o.Status == OrderFailed && o.PaidAt != "" && auth.Can(ctx, PermManage),
		"CanRefund": o.StripePaymentIntent != "" && o.Status != OrderRefunded && o.Status != OrderPending && o.Status != OrderCancelled && auth.Can(ctx, PermManage)})
}

// orderRefund asks Stripe to refund the order's PaymentIntent, then suspends access.
func (h *adminHandlers) orderRefund(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := h.svc.RefundOrder(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		h.fail(w, r, err)
		return
	case err != nil:
		h.svc.log.Error("commerce: refund", "order", id, "err", err)
		httpx.SetFlash(w, "error", "commerce.error.refund_failed")
	default:
		httpx.SetFlash(w, "success", "commerce.flash.refunded")
	}
	http.Redirect(w, r, "/admin/commerce/orders/"+id, http.StatusSeeOther)
}

// requestUpdate is a permission/CSRF-protected HTML service fulfillment action.
func (h *adminHandlers) requestUpdate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	_, err := h.svc.UpdateRequest(r.Context(), chi.URLParam(r, "id"), RequestUpdate{Status: r.PostForm.Get("status"), Note: r.PostForm.Get("note")})
	var ve *ValidationError
	if errors.As(err, &ve) {
		httpx.SetFlash(w, "error", "commerce.error.cart_invalid")
	} else if err != nil {
		h.fail(w, r, err)
		return
	} else {
		httpx.SetFlash(w, "success", "commerce.flash.saved")
	}
	http.Redirect(w, r, "/admin/commerce/orders/"+chi.URLParam(r, "id"), 303)
}
