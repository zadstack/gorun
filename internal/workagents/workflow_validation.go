package workagents

import (
	"encoding/json"
	"errors"
)

var errWorkflowGraph = errors.New("workflow_graph_unsupported: imported steps and human approvals require a graph executor; keep this workflow disabled")

// Imported organization graphs are executable instructions, not an agent prompt.
// Never flatten them into a single turn that would skip their approval gates.
func workflowBlockedReason(input string) string {
	var document map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &document) != nil {
		return ""
	}
	var source map[string]json.RawMessage
	if raw, ok := document["source_config"]; ok && json.Unmarshal(raw, &source) == nil {
		if raw, ok := source["organization"]; ok && string(raw) != "null" {
			return "workflow_graph_unsupported"
		}
	}
	if raw, ok := document["organization"]; ok && string(raw) != "null" {
		return "workflow_graph_unsupported"
	}
	return ""
}
