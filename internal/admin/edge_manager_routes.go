package admin

import (
	"net/http"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/edgeprovision"
	"github.com/zadstack/gorun/internal/kernel"
)

type edgeManagerCapabilities struct {
	Contract     string `json:"contract"`
	List         bool   `json:"list"`
	Connect      bool   `json:"connect"`
	Check        bool   `json:"check"`
	Create       bool   `json:"create"`
	Experimental bool   `json:"experimental"`
	Independent  bool   `json:"independent"`
}

type edgePlanCreateInput struct {
	Request          edgeprovision.Request `json:"request"`
	RegistryRevision string                `json:"registry_revision"`
}
type edgePlanExecuteInput struct {
	Approved    bool   `json:"approved"`
	Fingerprint string `json:"fingerprint"`
}
type edgeInstanceCheckInput struct {
	Revision string `json:"revision"`
}

func (h *handlers) edgeManagerCapabilities(w http.ResponseWriter, r *http.Request) {
	if !h.edgeInstancePlanAllowed(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteItem(w, edgeManagerCapabilities{Contract: "edge-instances.v1", List: true, Connect: true, Check: true, Create: edgeprovision.Available(), Experimental: true, Independent: true})
}
func (h *handlers) edgePlanCreate(w http.ResponseWriter, r *http.Request) {
	h.edgeInstancePlanCreateWithAdapter(w, r, edgeprovision.NewAdapter(), edgeprovision.ArtifactSHA())
}
func (h *handlers) edgePlanExecute(w http.ResponseWriter, r *http.Request) {
	h.edgeInstancePlanApproveWithArtifact(w, r, edgeprovision.ArtifactSHA())
}

func (h *handlers) edgeManagerAPIOperations() {
	base := "/site/edge/instances"
	ops := []kernel.APIOperation{
		api.Op("GET", base+"/capabilities", "Independent Edge manager contract; local capability discovery only, no provider request", []string{"site"}, []string{PermSettings}, nil, edgeManagerCapabilities{}, h.edgeManagerCapabilities),
		api.Op("GET", base, "Saved independent Edge instance identities; no provider request. Revision is returned at envelope level", []string{"site"}, []string{PermSettings}, nil, []edgeInstance{}, h.edgeInstancesList),
		api.Op("POST", base, "Connect an independent existing Edge instance after verifying its primary manager identity; write-only key and registry revision required", []string{"site"}, []string{PermSettings}, edgeInstanceInput{}, edgeInstance{}, h.edgeInstancesConnect),
		api.Op("POST", base+"/{id}/check", "Explicitly recheck one saved instance identity; preserves other instances", []string{"site"}, []string{PermSettings}, edgeInstanceCheckInput{}, edgeInstance{}, h.edgeInstancesCheck),
		api.Op("GET", base+"/plans", "Saved creation plans and operation states; never returns credentials or calls Cloudflare", []string{"site"}, []string{PermSettings}, nil, []edgeInstancePlan{}, h.edgeInstancePlansList),
		api.Op("GET", base+"/plans/{id}", "One saved creation plan with resource receipts and reconciliation state", []string{"site"}, []string{PermSettings}, nil, edgeInstancePlan{}, h.edgeInstancePlanGet),
		api.Op("POST", base+"/plans", "Preview isolated Worker, D1 and private R2 creation using bounded read-only Cloudflare checks; creates no provider resource", []string{"site"}, []string{PermSettings}, edgePlanCreateInput{}, edgeInstancePlan{}, h.edgePlanCreate),
		api.Op("POST", base+"/plans/{id}/execute", "Explicitly approve the exact unexpired resource plan and queue one durable creation operation; no retries after an uncertain provider mutation", []string{"site"}, []string{PermSettings}, edgePlanExecuteInput{}, edgeInstancePlan{}, h.edgePlanExecute),
	}
	for _, op := range ops {
		if op.Path == base+"/{id}/check" || op.Path == base+"/plans/{id}" || op.Path == base+"/plans/{id}/execute" {
			op.Params = []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}
		}
		if op.Path == base+"/plans/{id}/execute" {
			op.Contract = &kernel.APIContract{SuccessStatuses: []int{202}}
		}
		h.d.API.Add(op)
	}
}
