package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/edgeprovision"
)

func (h *handlers) edgeInstancePlanAllowed(w http.ResponseWriter, r *http.Request) bool {
	if auth.Can(r.Context(), "settings.manage") {
		return true
	}
	edgeInstanceAPIError(w, 403, "forbidden")
	return false
}
func (h *handlers) edgeInstancePlansList(w http.ResponseWriter, r *http.Request) {
	if !h.edgeInstancePlanAllowed(w, r) {
		return
	}
	rows, e := h.d.Settings.All(r.Context(), edgePlanPrefix)
	if e != nil {
		h.edgeInstancesError(w, r, e)
		return
	}
	plans := make([]edgeInstancePlan, 0, len(rows))
	for _, v := range rows {
		var stored storedEdgePlan
		if json.Unmarshal(v, &stored) != nil {
			h.edgeInstancesError(w, r, errEdgeSettings)
			return
		}
		plans = append(plans, stored.Plan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].ID > plans[j].ID })
	h.aiJSON(w, 200, map[string]any{"ok": true, "data": plans})
}
func (h *handlers) edgeInstancePlanGet(w http.ResponseWriter, r *http.Request) {
	if !h.edgeInstancePlanAllowed(w, r) {
		return
	}
	p, e := h.edgeInstancePlan(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		edgeInstanceAPIError(w, 404, "plan_not_found")
		return
	}
	h.aiJSON(w, 200, map[string]any{"ok": true, "data": p.Plan})
}

// Root supplies the trusted adapter/artifact, never a request-selected endpoint
// or browser supplied script hash. The prod wrappers are wired in module.go.
func (h *handlers) edgeInstancePlanCreateWithAdapter(w http.ResponseWriter, r *http.Request, a edgeprovision.Adapter, artifact string) {
	if !h.edgeInstancePlanAllowed(w, r) {
		return
	}
	var in struct {
		Request          edgeprovision.Request `json:"request"`
		RegistryRevision string                `json:"registry_revision"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
		edgeInstanceAPIError(w, 400, "invalid_request")
		return
	}
	p, e := h.createEdgeInstancePlan(r.Context(), a, artifact, in.Request, in.RegistryRevision)
	if e != nil {
		h.edgeInstancePlanError(w, r, e)
		return
	}
	h.aiJSON(w, 200, map[string]any{"ok": true, "data": p})
}
func (h *handlers) edgeInstancePlanApproveWithArtifact(w http.ResponseWriter, r *http.Request, artifact string) {
	if !h.edgeInstancePlanAllowed(w, r) {
		return
	}
	var in struct {
		Approved    bool   `json:"approved"`
		Fingerprint string `json:"fingerprint"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
		edgeInstanceAPIError(w, 400, "invalid_request")
		return
	}
	p, e := h.approveEdgeInstancePlan(r.Context(), chi.URLParam(r, "id"), in.Fingerprint, in.Approved, artifact)
	if e != nil {
		h.edgeInstancePlanError(w, r, e)
		return
	}
	h.aiJSON(w, 202, map[string]any{"ok": true, "data": p})
}
func (h *handlers) edgeInstancePlanError(w http.ResponseWriter, r *http.Request, e error) {
	if errors.Is(e, errEdgePlan) {
		edgeInstanceAPIError(w, 422, "plan_rejected")
		return
	}
	h.edgeInstancesError(w, r, e)
}
