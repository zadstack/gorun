package admin

import (
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/observe"
)

// Integrations API. Reads return the saved snapshot; writes are
// compare-and-swap; checks queue background work (202) except the mail
// test and the Worker identity check, which are explicit synchronous
// actions bounded by their own timeouts. Secrets are never returned.

var integrationErrors = []kernel.APIErrorResponse{
	{Status: 404, Code: "not_found", Description: "Unknown inventory kind or Worker"},
	{Status: 409, Code: "conflict", Description: "The stored configuration changed since it was read; reload and retry"},
	{Status: 409, Code: "cloudflare_not_configured", Description: "No Cloudflare token is stored"},
	{Status: 409, Code: "account_required", Description: "No Cloudflare account is selected"},
	{Status: 409, Code: "capability_disabled", Description: "Mail is not configured"},
	{Status: 422, Code: "validation_failed", Description: "Invalid input (fields)"},
	{Status: 422, Code: "edge_identity_failed", Description: "The origin did not answer the GoRun Edge manager contract for the pasted key"},
	{Status: 429, Code: "rate_limited", Description: "At most 5 test messages per operator per 10 minutes"},
}

func (h *handlers) integrationAPIOperations() {
	kindParam := []kernel.APIParam{{Name: "kind", In: "path", Type: "string", Required: true, Description: "r2, d1 or workers"}}
	get := api.Op("GET", "/site/integrations", "Integrations snapshot: Cloudflare connection and scope lamps, R2/D1/Workers inventory, storage bindings with sources, mail settings; no external calls",
		[]string{"site"}, []string{PermSettings}, nil, integrationsReport{}, h.apiIntegrations)
	cf := api.Op("PUT", "/site/integrations/cloudflare", "Store the Cloudflare API token (encrypted, never returned) and/or select the account; queues the read-only checks",
		[]string{"site"}, []string{PermSettings}, cloudflareInput{}, cloudflareView{}, h.apiCloudflareSave)
	cf.Contract = &kernel.APIContract{Errors: []kernel.APIErrorResponse{integrationErrors[1], integrationErrors[5]}}
	check := api.Op("POST", "/site/integrations/cloudflare/check", "Queue the token verification, account listing and per-service read permission checks; answers 202 with the task references",
		[]string{"site"}, []string{PermSettings}, nil, []observe.Ref{}, h.apiCloudflareCheck)
	check.Contract = &kernel.APIContract{SuccessStatuses: []int{202}, Errors: integrationErrors[2:3]}
	inv := api.Op("GET", "/site/integrations/cloudflare/inventory/{kind}", "Saved inventory of one kind with age and an explicit complete flag",
		[]string{"site"}, []string{PermSettings}, nil, inventoryView{}, h.apiInventoryGet)
	inv.Params = kindParam
	inv.Contract = &kernel.APIContract{Errors: integrationErrors[:1]}
	refresh := api.Op("POST", "/site/integrations/cloudflare/inventory/{kind}/refresh", "Queue a full re-read of one inventory kind (paginated, bounded); answers 202; concurrent requests share one job",
		[]string{"site"}, []string{PermSettings}, nil, observe.Ref{}, h.apiInventoryRefresh)
	refresh.Params = kindParam
	refresh.Contract = &kernel.APIContract{SuccessStatuses: []int{202}, Errors: []kernel.APIErrorResponse{integrationErrors[0], integrationErrors[2], integrationErrors[3]}}
	identify := api.Op("POST", "/site/integrations/cloudflare/workers/{name}/identify", "Confirm a Worker as a GoRun Edge instance through its manager contract with a pasted manager key (never stored); records identity evidence",
		[]string{"site"}, []string{PermSettings}, workerIdentifyInput{}, cfEdgeIdentity{}, h.apiWorkerIdentify)
	identify.Params = []kernel.APIParam{{Name: "name", In: "path", Type: "string", Required: true}}
	identify.Contract = &kernel.APIContract{Errors: []kernel.APIErrorResponse{integrationErrors[1], integrationErrors[3], integrationErrors[5], integrationErrors[6]}}
	bind := api.Op("POST", "/site/integrations/storage/bind", "Bind an existing bucket (discovered R2 or any S3 endpoint) as the media and/or backup destination with its own S3 keys; queues the read-only check",
		[]string{"site"}, []string{PermSettings}, storageBindInput{}, storageBindResult{}, h.apiStorageBind)
	bind.Contract = &kernel.APIContract{Errors: []kernel.APIErrorResponse{integrationErrors[1], integrationErrors[5]}}
	mailTest := api.Op("POST", "/site/integrations/mail/test", "Send one test message to the caller's own address or a chosen address (full recipient audited, 5 per operator per 10 minutes); reports provider acceptance, never inbox delivery",
		[]string{"site"}, []string{PermCapabilityTest}, mailTestInput{}, mailTestOutcome{}, h.apiMailTest)
	mailTest.Contract = &kernel.APIContract{Errors: []kernel.APIErrorResponse{integrationErrors[4], integrationErrors[5], integrationErrors[7]}}
	for _, op := range []kernel.APIOperation{get, cf, check, inv, refresh, identify, bind, mailTest} {
		h.d.API.Add(op)
	}
}

func (h *handlers) apiIntegrations(w http.ResponseWriter, r *http.Request) {
	report, err := h.integrationsReport(r.Context())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteItem(w, report)
}

func (h *handlers) apiCloudflareSave(w http.ResponseWriter, r *http.Request) {
	var in cloudflareInput
	if err := api.Decode(w, r, &in); err != nil {
		api.Fail(w, r, err)
		return
	}
	ctx := r.Context()
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	vals, fields, replaced := h.cloudflareValues(ctx, in, snapshot)
	if len(fields) > 0 {
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "validation failed", fields)
		return
	}
	if len(vals) > 0 {
		if err := h.saveCloudflare(ctx, snapshot, vals); err != nil {
			if errors.Is(err, errCFConflict) {
				api.WriteError(w, http.StatusConflict, "conflict", "cloudflare settings changed since they were read", nil)
				return
			}
			api.Fail(w, r, err)
			return
		}
	}
	h.d.Audit.Record(ctx, "integrations.cloudflare", "integrations", "cloudflare", map[string]any{"token_replaced": replaced, "cleared": in.Clear, "account_id": vals[settingCFAccount]})
	if !in.Clear {
		h.queueCloudflareChecks(ctx)
		if _, ok := vals[settingCFAccount]; ok || replaced {
			for _, kind := range cfInventoryKinds {
				_, _ = h.observe.Refresh(ctx, kindCFInventory, kind)
			}
		}
	}
	current, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, h.cloudflareView(ctx, current))
}

func (h *handlers) apiCloudflareCheck(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.cloudflareSnapshot(r.Context())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	if !snapshot.configured() {
		api.WriteError(w, http.StatusConflict, "cloudflare_not_configured", "store a Cloudflare API token first", nil)
		return
	}
	refs := h.queueCloudflareChecks(r.Context())
	h.d.Audit.Record(r.Context(), "integrations.cloudflare_check", "integrations", "cloudflare", map[string]any{"jobs": len(refs)})
	httpx.JSON(w, http.StatusAccepted, refs, nil)
}

func (h *handlers) apiInventoryGet(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if !slices.Contains(cfInventoryKinds, kind) {
		api.NotFound(w)
		return
	}
	report, err := h.integrationsReport(r.Context())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	for _, iv := range report.Inventory {
		if iv.Kind == kind {
			w.Header().Set("Cache-Control", "no-store")
			api.WriteItem(w, iv)
			return
		}
	}
	api.NotFound(w)
}

func (h *handlers) apiInventoryRefresh(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if !slices.Contains(cfInventoryKinds, kind) {
		api.NotFound(w)
		return
	}
	snapshot, err := h.cloudflareSnapshot(r.Context())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	switch {
	case !snapshot.configured():
		api.WriteError(w, http.StatusConflict, "cloudflare_not_configured", "store a Cloudflare API token first", nil)
		return
	case snapshot.AccountID == "":
		api.WriteError(w, http.StatusConflict, "account_required", "select a Cloudflare account first", nil)
		return
	}
	h.capabilityRefresh(w, r, kindCFInventory, kind, "integrations.inventory_refresh")
}

func (h *handlers) apiWorkerIdentify(w http.ResponseWriter, r *http.Request) {
	var in workerIdentifyInput
	if err := api.Decode(w, r, &in); err != nil {
		api.Fail(w, r, err)
		return
	}
	identity, err := h.identifyWorker(r.Context(), integrationsEdgeClient(), chi.URLParam(r, "name"), in.Origin, in.Key)
	switch {
	case err == nil:
		api.WriteItem(w, identity)
	case errors.Is(err, errConnectionConflict):
		api.WriteError(w, http.StatusConflict, "conflict", "the connection configuration changed during the check", nil)
	case errors.Is(err, errCFNoAccount):
		api.WriteError(w, http.StatusConflict, "account_required", "select a Cloudflare account first", nil)
	case errors.Is(err, errConnectionInvalid):
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "validation failed", map[string]string{"name": "admin.integrations.workers.error.worker"})
	case errors.Is(err, errEdgeOrigin):
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "validation failed", map[string]string{"origin": "admin.integrations.workers.error.origin"})
	case errors.Is(err, errEdgeCheck):
		api.WriteError(w, http.StatusUnprocessableEntity, "edge_identity_failed", "the origin did not answer the manager contract for this key", nil)
	default:
		api.Fail(w, r, err)
	}
}

func (h *handlers) apiStorageBind(w http.ResponseWriter, r *http.Request) {
	var in storageBindInput
	if err := api.Decode(w, r, &in); err != nil {
		api.Fail(w, r, err)
		return
	}
	res, fields, err := h.bindStorage(r.Context(), in)
	switch {
	case errors.Is(err, errConnectionConflict):
		api.WriteError(w, http.StatusConflict, "conflict", "the connection configuration changed since it was read", nil)
	case err != nil:
		api.Fail(w, r, err)
	case len(fields) > 0:
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "validation failed", fields)
	default:
		api.WriteItem(w, res)
	}
}

func (h *handlers) apiMailTest(w http.ResponseWriter, r *http.Request) {
	var in mailTestInput
	if err := api.Decode(w, r, &in); err != nil {
		api.Fail(w, r, err)
		return
	}
	ctx := r.Context()
	u := auth.UserFromContext(ctx)
	if u == nil {
		api.WriteError(w, http.StatusForbidden, api.CodeForbidden, "a user principal is required to send a test message", nil)
		return
	}
	locale := u.Locale
	if !validLocale(locale) {
		locale = i18n.Locale(ctx)
	}
	out, err := h.sendIntegrationsTestMail(ctx, u, in, locale, h.d.Settings.GetString(ctx, "site.title", ""))
	switch {
	case errors.Is(err, errMailRecipient):
		api.WriteError(w, http.StatusUnprocessableEntity, api.CodeValidation, "validation failed", map[string]string{"to": "auth.error.email_invalid"})
	case errors.Is(err, observe.ErrDisabled):
		api.WriteError(w, http.StatusConflict, "capability_disabled", "mail is not configured", nil)
	case errors.Is(err, errMailRateLimit):
		api.WriteError(w, http.StatusTooManyRequests, api.CodeRateLimited, "too many test messages from this operator; wait before sending another", nil)
	case err != nil:
		api.Fail(w, r, err)
	default:
		api.WriteItem(w, out)
	}
}
