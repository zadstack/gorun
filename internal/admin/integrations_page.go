package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/observe"
)

// Integrations hub: one report model serves the admin page and the API.
// Everything shown is a saved snapshot; nothing external runs on a GET.

// observationMeta is the display state of one saved observation.
type observationMeta struct {
	Status        string `json:"status" enum:"never,waiting,running,fresh,stale,failed,disabled"`
	CheckedAt     string `json:"checked_at,omitempty" doc:"Time of the saved result"`
	AgeSeconds    int64  `json:"age_seconds"`
	Age           string `json:"age,omitempty"`
	AttemptAt     string `json:"attempt_at,omitempty" doc:"Time of the newest attempt when it differs"`
	Error         string `json:"error,omitempty" doc:"Sanitized failure of the newest attempt"`
	ErrorKey      string `json:"error_key,omitempty"`
	Remediation   string `json:"remediation,omitempty"`
	RevisionMatch *bool  `json:"revision_match"`
	Deduplicated  bool   `json:"-"`
}

// currentCFObservation reports whether the saved read may authorize an
// operation or an identity claim: it must exist, belong to the current
// revision (fails closed when the revision cannot be read) and be no older
// than freshness. A newer attempt that is waiting, running or failed does
// not retract a current saved read: the scheduler refreshes inventories on
// its own, and a transient provider failure must not revoke a binding that
// the same token and account discovered a moment ago.
func currentCFObservation(o observe.Observation, freshness time.Duration) bool {
	if o.Saved == nil || o.Saved.RevisionMatch == nil || !*o.Saved.RevisionMatch {
		return false
	}
	switch o.Status {
	case "fresh":
		return true
	case "waiting", "running", "failed":
		return freshness <= 0 || time.Duration(o.Saved.AgeSeconds)*time.Second <= freshness
	}
	return false
}

func metaOf(o observe.Observation) observationMeta {
	m := observationMeta{Status: o.Status}
	if o.Saved != nil {
		m.CheckedAt, m.AgeSeconds, m.Age, m.RevisionMatch = o.Saved.At, o.Saved.AgeSeconds, observe.Age(o.Saved.AgeSeconds), o.Saved.RevisionMatch
	}
	if o.Attempt != nil {
		if o.Attempt.FinishedAt != nil {
			m.AttemptAt = *o.Attempt.FinishedAt
		}
		if o.Attempt.Status == "failed" || o.Attempt.Status == "superseded" {
			m.Error, m.Remediation = o.Attempt.Error, o.Attempt.Remediation
		}
	}
	return m
}

func cfSnapshotMeta(o observe.Observation, revision string) observationMeta {
	meta := metaOf(o)
	if o.Saved != nil {
		matches := o.Saved.Revision == revision
		meta.RevisionMatch = &matches
		if !matches && meta.Status == "fresh" {
			meta.Status = "stale"
		}
	}
	return meta
}

type cfScopeView struct {
	cfScopeState
	Permission string `json:"permission"`
	Repair     string `json:"repair"`
	RepairKey  string `json:"repair_key"`
}

type cloudflareView struct {
	Configured      bool            `json:"configured"`
	TokenHint       string          `json:"token_hint,omitempty" doc:"Last four characters of the token; the token itself is never returned"`
	AccountID       string          `json:"account_id"`
	AccountName     string          `json:"account_name"`
	Accounts        []cfAccount     `json:"accounts" doc:"Accounts the token listed in the last saved read"`
	AccountsCurrent bool            `json:"accounts_current" doc:"Whether the saved account list is fresh and belongs to the current token"`
	AccountsFull    bool            `json:"accounts_complete"`
	AccountsMeta    observationMeta `json:"accounts_observation"`
	Scopes          []cfScopeView   `json:"scopes"`
	ScopesMeta      observationMeta `json:"scopes_observation"`
	MinimumScopes   []string        `json:"minimum_scopes" doc:"Token permissions this integration needs; a dedicated token limited to one account is recommended"`
	R2Endpoint      string          `json:"r2_endpoint,omitempty"`
}

type cfResourceView struct {
	cfResource
	Bound    []string        `json:"bound" doc:"Purposes this bucket is the active destination for (R2), or [\"edge\"] for the configured Edge origin (Workers)"`
	Identity *cfEdgeIdentity `json:"identity,omitempty" doc:"Workers: confirmed GoRun Edge manager identity, only after a passed identity check"`
	Checked  string          `json:"identity_status,omitempty" enum:"passed,failed,stale,none"`
}

type inventoryView struct {
	ConfigRevision string           `json:"-"`
	Kind           string           `json:"kind"`
	Page           int              `json:"-"`
	Pages          int              `json:"-"`
	TotalItems     int              `json:"-"`
	Search         string           `json:"-"`
	PrevURL        string           `json:"-"`
	NextURL        string           `json:"-"`
	Actionable     bool             `json:"actionable" doc:"Whether saved resources are fresh and belong to the current token and account"`
	Meta           observationMeta  `json:"observation"`
	Complete       bool             `json:"complete"`
	Count          int              `json:"count"`
	Items          []cfResourceView `json:"items"`
}

type integrationIssue struct {
	Section string `json:"section"`
	Code    string `json:"code"`
}

type integrationsReport struct {
	Issues     []integrationIssue `json:"issues"`
	ObservedAt string             `json:"observed_at"`
	Cloudflare cloudflareView     `json:"cloudflare"`
	Inventory  []inventoryView    `json:"inventory"`
	Storage    []storageBinding   `json:"storage"`
	Backup     backupSummary      `json:"backup"`
	Mail       mailStatus         `json:"mail"`
	Coverage   string             `json:"coverage"`
}

// backupSummary shows the backup settings that already exist elsewhere
// (System → Backups) as resolved read-only values; they are edited there.
type backupSummary struct {
	ScheduleHours     int    `json:"schedule_hours"`
	Retain            int    `json:"retain"`
	RemoteDestination string `json:"remote_destination,omitempty" doc:"backup.schedule.remote_destination when set by the backup schedule"`
	LastRemote        string `json:"last_remote_at,omitempty"`
	LastRemoteStatus  string `json:"last_remote_status,omitempty"`
}

func (h *handlers) backupSummary(ctx context.Context) backupSummary {
	var s backupSummary
	_ = h.d.Settings.Get(ctx, SettingBackupScheduleHours, &s.ScheduleHours)
	_ = h.d.Settings.Get(ctx, SettingBackupRetain, &s.Retain)
	s.RemoteDestination = h.d.Settings.GetString(ctx, "backup.schedule.remote_destination", "")
	var last remoteBackupState
	if err := h.d.Settings.Get(ctx, "storage.backup.last", &last); err == nil {
		s.LastRemote, s.LastRemoteStatus = last.At, last.Status
	}
	return s
}

var cfScopeRepair = map[string]string{
	"token":    "Create a dedicated API token (My Profile → API Tokens) and paste it here; tokens are never shown again.",
	"accounts": "Add Account Settings: Read to the token so the account can be selected.",
	"r2":       "Add Workers R2 Storage: Read for the selected account; buckets are only listed, never changed.",
	"d1":       "Add D1: Read for the selected account; databases are only listed, rows are never read.",
	"workers":  "Add Workers Scripts: Read for the selected account; scripts are only listed, never deployed.",
}

func (h *handlers) cloudflareView(ctx context.Context, snapshot cfSnapshot) cloudflareView {
	v := cloudflareView{Configured: snapshot.configured(), TokenHint: snapshot.Hint, AccountID: snapshot.AccountID, AccountName: snapshot.AccountName, Accounts: []cfAccount{}, AccountsMeta: observationMeta{Status: "never"}, ScopesMeta: observationMeta{Status: "never"}}
	for _, d := range cfScopeDefs {
		v.MinimumScopes = append(v.MinimumScopes, d.Permission)
	}
	if snapshot.AccountID != "" {
		v.R2Endpoint = r2Endpoint(snapshot.AccountID)
	}
	lamps := map[string]cfScopeState{}
	if v.Configured {
		if o, err := h.observe.Get(ctx, kindCFAccounts, cfObservationKey); err == nil {
			v.AccountsMeta = cfSnapshotMeta(o, snapshot.revision(false))
			v.AccountsCurrent = currentCFObservation(o, cfScopesFreshness) && o.Saved.Revision == snapshot.revision(false)
			if o.Saved != nil {
				var data cfAccountsResult
				if json.Unmarshal(o.Saved.Data, &data) == nil {
					v.Accounts, v.AccountsFull = data.Accounts, data.Complete
				} else {
					v.AccountsMeta = unavailableObservation()
					v.AccountsCurrent = false
				}
			}
		} else {
			v.AccountsMeta = unavailableObservation()
		}
		if o, err := h.observe.Get(ctx, kindCFScopes, cfObservationKey); err == nil {
			v.ScopesMeta = cfSnapshotMeta(o, snapshot.revision(snapshot.AccountID != ""))
			if o.Saved != nil {
				var data cfScopesResult
				if json.Unmarshal(o.Saved.Data, &data) == nil {
					for _, s := range data.Scopes {
						lamps[s.ID] = s
					}
				} else {
					v.ScopesMeta = unavailableObservation()
				}
			}
			// A failed newer attempt means the token itself could not be
			// verified: the saved lamps describe an older revision at best.
			if o.Status == "failed" && o.Attempt != nil {
				// The stored error is text; an auth status or an inactive
				// token means the token itself is the problem.
				status := "could_not_verify"
				if strings.Contains(o.Attempt.Error, "HTTP 401") || strings.Contains(o.Attempt.Error, "HTTP 403") || strings.Contains(o.Attempt.Error, "not active") {
					status = "incomplete"
				}
				lamps["token"] = cfScopeState{ID: "token", Status: status, Error: o.Attempt.Error, CheckedAt: v.ScopesMeta.AttemptAt}
			}
		} else {
			v.ScopesMeta = unavailableObservation()
		}
	}
	for _, d := range cfScopeDefs {
		s, ok := lamps[d.ID]
		if !ok {
			s = cfScopeState{ID: d.ID, Status: "not_checked"}
		}
		if s.Status == "passed" {
			if v.ScopesMeta.RevisionMatch != nil && !*v.ScopesMeta.RevisionMatch {
				// Saved for another token or account: it says nothing about this one.
				s.Status, s.Evidence = "not_checked", "saved for an earlier configuration"
			} else if v.ScopesMeta.Status == "stale" {
				s.Evidence += " (older than the refresh window; a refresh is due)"
			}
		}
		v.Scopes = append(v.Scopes, cfScopeView{cfScopeState: s, Permission: d.Permission, Repair: cfScopeRepair[d.ID], RepairKey: "admin.integrations.cloudflare.repair." + d.ID})
	}
	return v
}

func (h *handlers) inventoryViews(ctx context.Context, snapshot cfSnapshot, bindings []storageBinding) []inventoryView {
	edgeOrigin := strings.ToLower(h.d.Settings.GetString(ctx, settingEdgeOrigin, ""))
	edgeManager := h.d.Settings.GetString(ctx, settingEdgeManager, "")
	identities := map[string]observe.Observation{}
	identitiesFailed := false
	if list, err := h.observe.ListWithRevision(ctx, kindCFEdge, func(_ context.Context, key string) (string, error) {
		return cfEdgeRevisionFor(snapshot, key), nil
	}); err == nil {
		for _, o := range list {
			identities[o.Key] = o
		}
	} else {
		identitiesFailed = true
	}
	var out []inventoryView
	for _, kind := range cfInventoryKinds {
		iv := inventoryView{Kind: kind, Items: []cfResourceView{}, ConfigRevision: snapshot.revision(true)}
		if !snapshot.configured() || snapshot.AccountID == "" {
			iv.Meta.Status = "never"
			out = append(out, iv)
			continue
		}
		o, err := h.observe.Get(ctx, kindCFInventory, kind)
		if err != nil {
			iv.Meta = unavailableObservation()
			out = append(out, iv)
			continue
		}
		iv.Meta = cfSnapshotMeta(o, snapshot.revision(true))
		iv.Actionable = currentCFObservation(o, cfInventoryFresh) && o.Saved.Revision == snapshot.revision(true)
		if o.Saved != nil {
			var data cfInventoryResult
			if json.Unmarshal(o.Saved.Data, &data) == nil && data.Kind == kind && data.Count >= 0 && data.Count == len(data.Items) {
				iv.Complete, iv.Count = data.Complete, data.Count
				for _, item := range data.Items {
					rv := cfResourceView{cfResource: item, Bound: []string{}}
					switch kind {
					case "r2":
						if b := r2BucketBound(bindings, snapshot.AccountID, item.Name); iv.Actionable && b != nil {
							rv.Bound = b
						}
					case "workers":
						rv.Checked = "none"
						if io, ok := identities[item.Name]; ok && (io.Saved != nil || io.Attempt != nil) {
							switch {
							case io.Attempt != nil && io.Attempt.Status == "failed" && (io.Saved == nil || io.Attempt.FinishedAt == nil || *io.Attempt.FinishedAt >= io.Saved.At):
								rv.Checked = "failed"
							case io.Saved != nil && (!iv.Actionable || !currentCFObservation(io, evidenceTTL)):
								rv.Checked = "stale"
							case io.Saved != nil:
								var id cfEdgeIdentity
								if json.Unmarshal(io.Saved.Data, &id) == nil && id.SiteID != "" {
									rv.Checked, rv.Identity = "passed", &id
									if (edgeOrigin != "" && strings.ToLower(id.Origin) == edgeOrigin) || (edgeManager != "" && id.ManagerID == edgeManager) {
										rv.Bound = []string{"edge"}
									}
								}
							}
						}
					}
					iv.Items = append(iv.Items, rv)
				}
			} else {
				iv.Meta = unavailableObservation()
				iv.Actionable = false
			}
		}
		if kind == "workers" && identitiesFailed {
			iv.Meta = unavailableObservation()
			iv.Actionable = false
		}
		out = append(out, iv)
	}
	return out
}

func (h *handlers) integrationsReport(ctx context.Context) (integrationsReport, error) {
	report := integrationsReport{ObservedAt: time.Now().UTC().Format(time.RFC3339), Issues: []integrationIssue{}, Storage: []storageBinding{}}
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		report.addIssue("cloudflare")
		report.Cloudflare.AccountsMeta = unavailableObservation()
		report.Cloudflare.ScopesMeta = unavailableObservation()
		h.d.Log.Error("admin: integrations section unavailable", "section", "cloudflare", "err", err, "request_id", httpx.RequestIDFromContext(ctx))
	} else {
		report.Cloudflare = h.cloudflareView(ctx, snapshot)
	}
	bindings, err := h.storageBindings(ctx)
	if err != nil {
		report.addIssue("storage")
		h.d.Log.Error("admin: integrations section unavailable", "section", "storage", "err", err, "request_id", httpx.RequestIDFromContext(ctx))
	} else {
		report.Storage = bindings
	}
	if report.SectionAvailable("cloudflare") {
		report.Inventory = h.inventoryViews(ctx, snapshot, bindings)
	} else {
		report.Inventory = []inventoryView{}
	}
	mailSt, err := h.mailStatus(ctx)
	if err != nil {
		report.addIssue("mail")
		h.d.Log.Error("admin: integrations section unavailable", "section", "mail", "err", err, "request_id", httpx.RequestIDFromContext(ctx))
	} else {
		report.Mail = mailSt
	}
	report.Backup = h.backupSummary(ctx)
	report.Coverage = "Saved snapshots only; no provider calls on GET. Historical inventories are retained but cannot be used until refreshed for the current account and token. Issues explicitly identify unavailable sections. A valid token does not establish all service permissions; excess permissions are not detectable."
	return report, nil
}

// --- HTML -----------------------------------------------------------------------------

type integrationsPage struct {
	integrationsReport
	Fields             map[string]string
	Form               map[string]string
	CanTest            bool
	MailForm           mailInput
	TLSModes           []string
	TestOutcome        *mailTestOutcome
	S3Profiles         []connectionView
	ConnectionVersions map[string]string
}

func (h *handlers) renderIntegrations(w http.ResponseWriter, r *http.Request, status int, fields, form map[string]string, extra func(*integrationsPage)) {
	report, err := h.integrationsReport(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if fields == nil {
		fields = map[string]string{}
	}
	if form == nil {
		form = map[string]string{}
	}
	snap, err := mailSnapshot(r.Context(), h.d.DB)
	if err != nil {
		report.addIssue("mail")
		h.d.Log.Error("admin: integrations section unavailable", "section", "mail", "err", err, "request_id", httpx.RequestIDFromContext(r.Context()))
	}
	page := integrationsPage{integrationsReport: report, Fields: fields, Form: form, CanTest: auth.Can(r.Context(), PermCapabilityTest), MailForm: mailFromSnapshot(snap), TLSModes: tlsModes, ConnectionVersions: map[string]string{}}
	profiles, err := h.listConnections(r.Context())
	if err != nil {
		page.addIssue("storage")
		h.d.Log.Error("admin: integrations section unavailable", "section", "storage", "err", err, "request_id", httpx.RequestIDFromContext(r.Context()))
	}
	for _, p := range profiles {
		if p.Kind == "s3" {
			page.S3Profiles = append(page.S3Profiles, viewConnection(p))
			page.ConnectionVersions[p.ID] = p.Version
		}
	}
	paginateIntegrations(&page.integrationsReport, r.URL.Query())
	if extra != nil {
		extra(&page)
	}
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.RenderWith(w, r, status, "admin", "pages/settings/integrations.html", page)
}

func (h *handlers) integrationsGet(w http.ResponseWriter, r *http.Request) {
	h.renderIntegrations(w, r, http.StatusOK, nil, nil, nil)
}

func (h *handlers) integrationsCloudflareSave(w http.ResponseWriter, r *http.Request) {
	var in cloudflareInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	in.Clear = in.Clear || strings.HasSuffix(r.URL.Path, "/clear")
	ctx := r.Context()
	snapshot, err := h.cloudflareSnapshot(ctx)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	vals, fields, replaced := h.cloudflareValues(ctx, in, snapshot)
	if len(fields) > 0 {
		h.renderIntegrations(w, r, http.StatusUnprocessableEntity, fields, map[string]string{"account_id": in.AccountID}, nil)
		return
	}
	if len(vals) > 0 {
		if err := h.saveCloudflare(ctx, snapshot, vals); err != nil {
			if errors.Is(err, errCFConflict) {
				h.renderIntegrations(w, r, http.StatusConflict, map[string]string{"cloudflare": "admin.integrations.cloudflare.error.conflict"}, nil, nil)
				return
			}
			h.serverError(w, r, err)
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
	httpx.SetFlash(w, "success", "admin.settings.saved")
	redirect(w, r, "/admin/settings/integrations")
}

func (h *handlers) integrationsCloudflareCheck(w http.ResponseWriter, r *http.Request) {
	refs := h.queueCloudflareChecks(r.Context())
	if len(refs) == 0 {
		h.renderIntegrations(w, r, http.StatusUnprocessableEntity, map[string]string{"cloudflare": "admin.integrations.cloudflare.error.token"}, nil, nil)
		return
	}
	h.d.Audit.Record(r.Context(), "integrations.cloudflare_check", "integrations", "cloudflare", map[string]any{"jobs": len(refs)})
	httpx.SetFlash(w, "success", "admin.integrations.queued")
	redirect(w, r, "/admin/settings/integrations")
}

func (h *handlers) integrationsInventoryRefresh(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if !slices.Contains(cfInventoryKinds, kind) {
		h.tpl.RenderError(w, r, http.StatusNotFound)
		return
	}
	_, err := h.observe.Refresh(r.Context(), kindCFInventory, kind)
	switch {
	case err == nil:
		h.d.Audit.Record(r.Context(), "integrations.inventory_refresh", "integrations", kind, nil)
		httpx.SetFlash(w, "success", "admin.integrations.queued")
	case errors.Is(err, observe.ErrDisabled):
		httpx.SetFlash(w, "error", "admin.integrations.cloudflare.error.token")
	case errors.Is(err, observe.ErrNotFound):
		httpx.SetFlash(w, "error", "admin.integrations.cloudflare.error.account")
	default:
		h.serverError(w, r, err)
		return
	}
	redirect(w, r, "/admin/settings/integrations")
}

type workerIdentifyInput struct {
	Origin string `form:"origin" json:"origin"`
	Key    string `form:"manager_key" json:"manager_key"`
}

func (h *handlers) integrationsWorkerIdentify(w http.ResponseWriter, r *http.Request) {
	var in workerIdentifyInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	worker := chi.URLParam(r, "name")
	_, err := h.identifyWorker(r.Context(), integrationsEdgeClient(), worker, in.Origin, in.Key)
	if errors.Is(err, errConnectionConflict) {
		h.renderIntegrations(w, r, http.StatusConflict, map[string]string{"worker": worker, "identify": "admin.connections.conflict"}, map[string]string{"worker": worker, "origin": in.Origin}, nil)
		return
	}
	if err != nil {
		field := "identify"
		switch {
		case errors.Is(err, errEdgeOrigin):
			field = "identify_origin"
		case errors.Is(err, errCFNoAccount), errors.Is(err, errConnectionInvalid):
			field = "identify_worker"
		case !errors.Is(err, errEdgeCheck):
			h.serverError(w, r, err)
			return
		}
		h.renderIntegrations(w, r, http.StatusUnprocessableEntity, map[string]string{field: "admin.integrations.workers.error." + strings.TrimPrefix(field, "identify_"), "worker": worker}, map[string]string{"worker": worker, "origin": in.Origin}, nil)
		return
	}
	httpx.SetFlash(w, "success", "admin.integrations.workers.identified")
	redirect(w, r, "/admin/settings/integrations")
}

// integrationsEdgeClient is the client for Worker identity checks: public
// hosts only, like Settings → Edge. Tests swap it for a fake origin.
var integrationsEdgeClient = func() *http.Client { return edgeHTTPClient }

func (h *handlers) integrationsStorageBind(w http.ResponseWriter, r *http.Request) {
	var in storageBindInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	_, fields, err := h.bindStorage(r.Context(), in)
	if err != nil {
		if errors.Is(err, errConnectionConflict) {
			h.renderIntegrations(w, r, http.StatusConflict, map[string]string{"storage": "admin.connections.conflict"}, nil, nil)
			return
		}
		h.serverError(w, r, err)
		return
	}
	if len(fields) > 0 {
		form := map[string]string{"source": in.Source, "name": in.Name, "endpoint": in.Endpoint, "region": in.Region, "bucket": in.Bucket, "connection_id": in.Connection, "purposes": strings.Join(in.Purposes, ",")}
		h.renderIntegrations(w, r, http.StatusUnprocessableEntity, fields, form, nil)
		return
	}
	httpx.SetFlash(w, "success", "admin.integrations.storage.bound")
	redirect(w, r, "/admin/settings/integrations")
}

func (h *handlers) integrationsMailSave(w http.ResponseWriter, r *http.Request) {
	var in mailInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	vals, fields := h.validateMail(in)
	if len(fields) > 0 {
		in.Password, in.ResendAPIKey = "", ""
		h.renderIntegrations(w, r, http.StatusUnprocessableEntity, prefixFields(fields, "mail_"), nil, func(p *integrationsPage) { p.MailForm = in })
		return
	}
	if err := h.saveMailSettings(r.Context(), vals, in.EditToken, nil); err != nil {
		if errors.Is(err, errMailConflict) {
			h.renderIntegrations(w, r, http.StatusConflict, map[string]string{"mail_conflict": "admin.mail.conflict"}, nil, nil)
			return
		}
		h.serverError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.settings.saved")
	redirect(w, r, "/admin/settings/integrations")
}

func prefixFields(fields map[string]string, prefix string) map[string]string {
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[prefix+k] = v
	}
	return out
}

func (h *handlers) integrationsMailTest(w http.ResponseWriter, r *http.Request) {
	var in mailTestInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	u := auth.UserFromContext(ctx)
	locale := ""
	if u != nil && validLocale(u.Locale) {
		locale = u.Locale
	} else {
		locale = i18n.Locale(ctx)
	}
	out, err := h.sendIntegrationsTestMail(ctx, u, in, locale, h.siteTitle(r))
	switch {
	case errors.Is(err, errMailRecipient):
		h.renderIntegrations(w, r, http.StatusUnprocessableEntity, map[string]string{"mail_to": "auth.error.email_invalid"}, map[string]string{"to": in.To}, nil)
		return
	case errors.Is(err, observe.ErrDisabled):
		h.renderIntegrations(w, r, http.StatusUnprocessableEntity, map[string]string{"mail_test": "admin.mail.not_configured"}, nil, nil)
		return
	case errors.Is(err, errMailRateLimit):
		h.renderIntegrations(w, r, http.StatusTooManyRequests, map[string]string{"mail_test": "admin.integrations.mail.rate_limited"}, map[string]string{"to": in.To}, nil)
		return
	case err != nil:
		h.serverError(w, r, err)
		return
	}
	h.renderIntegrations(w, r, http.StatusOK, nil, map[string]string{"to": in.To}, func(p *integrationsPage) { p.TestOutcome = &out })
}

// integrationsStorageProbe queues the isolated write/read/delete probe for
// one purpose: the active media storage, or the S3 profile bound as the
// backup destination. Explicit, and behind system.capability_test.
func (h *handlers) integrationsStorageProbe(w http.ResponseWriter, r *http.Request) {
	purpose := chi.URLParam(r, "purpose")
	bindings, err := h.storageBindings(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	kind, key := "", ""
	for _, b := range bindings {
		if b.Purpose != purpose || !b.Configured {
			continue
		}
		if purpose == "media" {
			kind, key = kindOperationStorage, "media"
		} else if b.ConnectionID != "" {
			kind, key = kindOperationS3, b.ConnectionID
		}
	}
	if kind == "" {
		httpx.SetFlash(w, "error", "admin.integrations.storage.probe.unavailable")
		redirect(w, r, "/admin/settings/integrations")
		return
	}
	ref, err := h.observe.Refresh(r.Context(), kind, key)
	switch {
	case err == nil:
		h.d.Audit.Record(r.Context(), "capability.operation", "capability", key, map[string]any{"kind": kind, "job": ref.JobID, "deduplicated": ref.Deduped, "via": "integrations"})
		httpx.SetFlash(w, "success", "admin.integrations.queued")
	case errors.Is(err, observe.ErrNotFound), errors.Is(err, observe.ErrDisabled):
		httpx.SetFlash(w, "error", "admin.integrations.storage.probe.unavailable")
	default:
		h.serverError(w, r, err)
		return
	}
	redirect(w, r, "/admin/settings/integrations")
}
