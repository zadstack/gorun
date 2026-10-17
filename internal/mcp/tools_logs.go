package mcp

import (
	"context"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/kernel"
)

type getLogsInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"audit entries to return, default 50, max 200"`
	Action string `json:"action,omitempty" jsonschema:"only this audit action, e.g. mcp.call"`
}

// failedJob is a failed or dead job without its payload (payloads may hold
// personal data).
type failedJob struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error"`
	UpdatedAt string `json:"updated_at"`
}

func logTools(d *kernel.Deps) kernel.MCPTool {
	return kernel.MCPTool{Name: "get_logs", Scopes: []string{scopeHealth}, Input: getLogsInput{},
		Description: "Recent audit log entries and failed background jobs.",
		Handler: bind(func(ctx context.Context, in getLogsInput) (any, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			limit = min(limit, 200)
			entries, total, err := d.Audit.List(ctx, audit.ListQuery{Action: in.Action, Limit: limit})
			if err != nil {
				return nil, err
			}
			if entries == nil {
				entries = []audit.Entry{}
			}
			jobsOut := []failedJob{}
			for _, status := range []string{"failed", "dead"} {
				list, err := d.Jobs.List(ctx, status, 20)
				if err != nil {
					return nil, err
				}
				for _, j := range list {
					jobsOut = append(jobsOut, failedJob{ID: j.ID, Kind: j.Kind, Status: j.Status, Attempts: j.Attempts,
						LastError: j.LastError, UpdatedAt: j.UpdatedAt})
				}
			}
			return map[string]any{"audit": entries, "audit_total": total, "failed_jobs": jobsOut}, nil
		})}
}
