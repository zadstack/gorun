package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

type getLogsInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"audit entries to return, default 50, max 200"`
	Action string `json:"action,omitempty" jsonschema:"only this audit action, e.g. mcp.call"`
}

func logTools(d *kernel.Deps) kernel.MCPTool {
	return kernel.MCPTool{Name: "get_logs", Scopes: []string{scopeAudit}, Input: getLogsInput{},
		Description: "Recent audit log entries for this API key or signed-in user only. Site-wide job queues stay on system.health (admin / REST).",
		Handler: bind(func(ctx context.Context, in getLogsInput) (any, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			limit = min(limit, 200)
			q := audit.ListQuery{Action: in.Action, Limit: limit}
			actor := auth.ActorFromContext(ctx)
			if actor.Kind != "" && actor.Kind != audit.SystemActor.Kind && actor.ID != "" {
				q.ActorKind, q.ActorID = actor.Kind, actor.ID
			}
			entries, total, err := d.Audit.List(ctx, q)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(entries))
			for _, e := range entries {
				out = append(out, map[string]any{
					"id": e.ID, "at": e.At, "actor": e.Actor, "action": e.Action,
					"target_kind": e.TargetKind, "target_id": e.TargetID,
					"meta": scrubAuditMeta(e.Meta),
					// IP deliberately omitted: get_logs must not leak peer addresses.
				})
			}
			return map[string]any{"audit": out, "audit_total": total}, nil
		})}
}

// scrubAuditMeta drops keys that commonly hold secrets or peer identifiers.
func scrubAuditMeta(raw []byte) any {
	if len(raw) == 0 || string(raw) == "{}" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	for k := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "secret") || strings.Contains(lk, "token") ||
			strings.Contains(lk, "password") || strings.Contains(lk, "key") ||
			lk == "ip" || lk == "authorization" {
			delete(m, k)
		}
	}
	return m
}
