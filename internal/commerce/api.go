package commerce

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/kernel"
)

// DeleteResponse is returned by DELETE operations.
type DeleteResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// ActionResponse is returned by entitlement actions.
type ActionResponse struct {
	Entitlement *Entitlement `json:"entitlement"`
}

// ServerTestResponse is returned by POST /v1/commerce/servers/{id}/test.
type ServerTestResponse struct {
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Catalog Catalog `json:"catalog"`
}

type RequestDetail struct {
	Order *Order      `json:"order"`
	Items []OrderItem `json:"items"`
}

type apiHandlers struct{ svc *service }

func idParam() []kernel.APIParam {
	return []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}
}

func listParamsDoc() []kernel.APIParam {
	return append(append([]kernel.APIParam{}, api.PageParams...),
		kernel.APIParam{Name: "search", In: "query", Type: "string", Description: "email or order number"},
		kernel.APIParam{Name: "status", In: "query", Type: "string"})
}

// registerAPI adds the /v1/commerce operations. Reading orders and
// entitlements needs commerce.view_orders; everything else commerce.manage.
func registerAPI(d *kernel.Deps, s *service) {
	a := &apiHandlers{svc: s}
	tags, manage, view := []string{"commerce"}, []string{PermManage}, []string{PermViewOrders}
	add := func(op kernel.APIOperation, params []kernel.APIParam) {
		op.Params = params
		d.API.Add(op)
	}
	add(api.Op("GET", "/v1/commerce/account-settings", "Read customer account registration policy", tags, manage, nil, AccountSettings{}, a.accountSettings), nil)
	add(api.Op("PATCH", "/v1/commerce/account-settings", "Configure customer registration without changing payment or email", tags, manage, AccountSettings{}, AccountSettings{}, a.saveAccountSettings), nil)
	add(api.Op("GET", "/v1/commerce/products", "List products", tags, manage, nil, []Product{}, a.products), nil)
	add(api.Op("POST", "/v1/commerce/products", "Create or replace a product (id empty = create)", tags, manage, Product{}, Product{}, a.saveProduct), nil)
	add(api.Op("DELETE", "/v1/commerce/products/{id}", "Delete a product", tags, manage, nil, DeleteResponse{}, a.deleteProduct), idParam())
	add(api.Op("POST", "/v1/commerce/requests", "Record a manual request without initiating payment", tags, manage, CartCheckout{}, Order{}, a.submitRequest), nil)
	add(api.Op("GET", "/v1/commerce/orders/{id}", "Read order details and frozen line items", tags, view, nil, RequestDetail{}, a.orderDetail), idParam())
	add(api.Op("PATCH", "/v1/commerce/requests/{id}", "Track service fulfillment independently of payment", tags, manage, RequestUpdate{}, Order{}, a.updateRequest), idParam())
	add(api.Op("GET", "/v1/commerce/orders", "List orders", tags, view, nil, []Order{}, a.orders), listParamsDoc())
	add(api.Op("GET", "/v1/commerce/entitlements", "List entitlements", tags, view, nil, []Entitlement{}, a.entitlements), listParamsDoc())
	for _, action := range []string{"suspend", "resume", "reconcile"} {
		add(api.Op("POST", "/v1/commerce/entitlements/{id}/"+action, strings.ToUpper(action[:1])+action[1:]+" an entitlement on its billing server",
			tags, manage, nil, ActionResponse{}, a.entitlementAction(action)), idParam())
	}
	add(api.Op("GET", "/v1/commerce/servers", "List billing servers (secrets are never returned)", tags, manage, nil, []Server{}, a.servers), nil)
	add(api.Op("POST", "/v1/commerce/servers", "Create or update a billing server (secret is write-only)", tags, manage, Server{}, Server{}, a.saveServer), nil)
	add(api.Op("POST", "/v1/commerce/servers/{id}/test", "Test a billing server (GET /catalog)", tags, manage, nil, ServerTestResponse{}, a.testServer), idParam())
}

func (a *apiHandlers) products(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Products(r.Context(), r.URL.Query().Get("active") == "1")
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteList(w, list, len(list), 1, max(len(list), 1))
}

func (a *apiHandlers) saveProduct(w http.ResponseWriter, r *http.Request) {
	var p Product
	if err := api.Decode(w, r, &p); err != nil {
		api.Fail(w, r, err)
		return
	}
	saved, err := a.svc.SaveProduct(r.Context(), p)
	if errors.Is(err, ErrNotFound) {
		api.NotFound(w)
		return
	}
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, saved)
}

func (a *apiHandlers) deleteProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := a.svc.DeleteProduct(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			api.NotFound(w)
			return
		}
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, DeleteResponse{ID: id, Deleted: true})
}

func apiListQuery(r *http.Request) ListQuery {
	page, perPage := api.Pagination(r)
	q := r.URL.Query()
	return ListQuery{Search: strings.TrimSpace(q.Get("search")), Status: q.Get("status"), Page: page, PerPage: perPage}
}

func (a *apiHandlers) orders(w http.ResponseWriter, r *http.Request) {
	q := apiListQuery(r)
	list, total, err := a.svc.Orders(r.Context(), q)
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteList(w, list, total, q.Page, q.PerPage)
}

func (a *apiHandlers) entitlements(w http.ResponseWriter, r *http.Request) {
	q := apiListQuery(r)
	list, total, err := a.svc.Entitlements(r.Context(), q)
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteList(w, list, total, q.Page, q.PerPage)
}

func (a *apiHandlers) entitlementAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, id := r.Context(), chi.URLParam(r, "id")
		var err error
		switch action {
		case "suspend":
			err = a.svc.Suspend(ctx, id)
		case "resume":
			err = a.svc.Resume(ctx, id)
		case "reconcile":
			err = a.svc.Reconcile(ctx, id)
		}
		var me *MCBBError
		switch {
		case errors.Is(err, ErrNotFound):
			api.NotFound(w)
			return
		case errors.Is(err, ErrPeriodEnded):
			api.WriteError(w, http.StatusConflict, "period_ended", err.Error(), nil)
			return
		case errors.As(err, &me):
			api.WriteError(w, http.StatusBadGateway, "billing_server_error", me.Error(), nil)
			return
		case err != nil:
			api.Fail(w, r, err)
			return
		}
		e, err := a.svc.Entitlement(ctx, id)
		if err != nil {
			api.Fail(w, r, err)
			return
		}
		api.WriteItem(w, ActionResponse{Entitlement: e})
	}
}

func (a *apiHandlers) servers(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Servers(r.Context())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteList(w, list, len(list), 1, max(len(list), 1))
}

func (a *apiHandlers) saveServer(w http.ResponseWriter, r *http.Request) {
	var s Server
	if err := api.Decode(w, r, &s); err != nil {
		api.Fail(w, r, err)
		return
	}
	saved, err := a.svc.saveServer(r.Context(), Server{ID: s.ID, Name: s.Name, BaseURL: s.BaseURL, ClientID: s.ClientID, Secret: s.Secret, Protocol: s.Protocol})
	if errors.Is(err, ErrNotFound) {
		api.NotFound(w)
		return
	}
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, saved)
}

func (a *apiHandlers) testServer(w http.ResponseWriter, r *http.Request) {
	cat, err := a.svc.TestServer(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		api.NotFound(w)
		return
	}
	resp := ServerTestResponse{OK: err == nil, Catalog: cat}
	if err != nil {
		resp.Error = err.Error()
	}
	api.WriteItem(w, resp)
}

func (a *apiHandlers) submitRequest(w http.ResponseWriter, r *http.Request) {
	var req CartCheckout
	if err := api.Decode(w, r, &req); err != nil {
		api.Fail(w, r, err)
		return
	}
	o, err := a.svc.SubmitCart(r.Context(), req)
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, o)
}
func (a *apiHandlers) orderDetail(w http.ResponseWriter, r *http.Request) {
	o, err := a.svc.Order(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, ErrNotFound) {
		api.NotFound(w)
		return
	}
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	items, err := a.svc.orderItems(r.Context(), o)
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, RequestDetail{Order: o, Items: items})
}
func (a *apiHandlers) updateRequest(w http.ResponseWriter, r *http.Request) {
	var req RequestUpdate
	if err := api.Decode(w, r, &req); err != nil {
		api.Fail(w, r, err)
		return
	}
	o, err := a.svc.UpdateRequest(r.Context(), chi.URLParam(r, "id"), req)
	if errors.Is(err, ErrNotFound) {
		api.NotFound(w)
		return
	}
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, o)
}

// AccountSettings controls native customer registration only. Registration
// does not grant paid access or claim verified ownership of an email address.
type AccountSettings struct {
	RegistrationEnabled bool `json:"registration_enabled"`
}

func (a *apiHandlers) accountSettings(w http.ResponseWriter, r *http.Request) {
	api.WriteItem(w, AccountSettings{a.svc.registrationEnabled(r.Context())})
}
func (a *apiHandlers) saveAccountSettings(w http.ResponseWriter, r *http.Request) {
	var v AccountSettings
	if err := api.Decode(w, r, &v); err != nil {
		api.Fail(w, r, err)
		return
	}
	if err := a.svc.d.Settings.Set(r.Context(), "commerce.registration_enabled", v.RegistrationEnabled); err != nil {
		api.Fail(w, r, err)
		return
	}
	a.svc.audit.Record(r.Context(), "commerce.account_settings.save", "settings", "commerce", map[string]any{"registration_enabled": v.RegistrationEnabled})
	api.WriteItem(w, v)
}
