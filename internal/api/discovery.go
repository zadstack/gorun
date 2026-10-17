package api

import (
	"maps"

	"github.com/zadstack/gorun/internal/kernel"
)

func discovery(ops []kernel.APIOperation) map[string]any {
	caps := map[string]any{}
	limits := map[string]any{"pagination_max_per_page": MaxPerPage, "json_decode_helper_bytes": maxBody}
	for _, op := range ops {
		if op.Contract != nil {
			maps.Copy(limits, op.Contract.Limits)
		}
		switch op.Method + " " + normalizePath(op.Path) {
		case "PATCH /v1/content/{id}":
			caps["content_patch"] = map[string]any{"merge": true, "cas": false}
		case "PUT /v1/builder/{id}/draft":
			caps["builder_draft_cas"] = true
		case "POST /v1/builder/{id}/publish":
			caps["builder_publish_cas"] = true
		case "POST /v1/media":
			caps["media_multipart_upload"] = true
		}
	}
	return map[string]any{"core_version": CoreVersion, "api_contract_version": ContractVersion, "capabilities": caps, "limits": limits}
}
