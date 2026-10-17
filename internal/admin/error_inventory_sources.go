package admin

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/diagnostics"
	"github.com/zadstack/gorun/internal/kernel"
)

func inventoryBounds(f errorInventoryFilter) (string, string) {
	since, _ := bound(f.Since, false)
	until, _ := bound(f.Until, true)
	return since, until
}
func (h *handlers) errorInventoryRows(ctx context.Context, f errorInventoryFilter) ([]errorInventoryRow, error) {
	out := []errorInventoryRow{}
	since, until := inventoryBounds(f)
	if (f.Source == "" || f.Source == "runtime") && (f.Status == "" || f.Status == "recorded") {
		if c, ok := kernel.Get[*diagnostics.Collector](h.d, diagnostics.ServiceName); ok {
			rows, e := c.List(ctx, diagnostics.Filter{Category: f.Category, Since: since, Until: until, HTTPStatus: f.HTTPStatus, Limit: 200})
			if e != nil {
				return nil, e
			}
			for _, v := range rows {
				out = append(out, errorInventoryRow{Source: "runtime", Category: v.Category, Code: v.Code, Status: "recorded", FirstAt: v.FirstAt, LastAt: v.LastAt, HTTPStatus: v.HTTPStatus, Count: v.Count})
			}
		}
	}
	if (f.Source == "" || f.Source == "jobs") && (f.Category == "" || f.Category == "jobs") && f.HTTPStatus == 0 && f.Status != "recorded" {
		conds := []string{"(status IN ('dead','failed') OR (status='queued' AND attempts>0))"}
		args := []any{}
		if since != "" {
			conds = append(conds, "updated_at>=?")
			args = append(args, since)
		}
		if until != "" {
			conds = append(conds, "updated_at<?")
			args = append(args, until)
		}
		if f.Status != "" {
			state := f.Status
			if state == "retrying" {
				state = "queued"
			}
			conds = append(conds, "status=?")
			args = append(args, state)
		}
		rows, e := h.d.DB.QueryContext(ctx, `SELECT status,MIN(updated_at),MAX(updated_at),COUNT(*) FROM jobs WHERE `+strings.Join(conds, " AND ")+` GROUP BY status`, args...)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			v := errorInventoryRow{Source: "jobs", Category: "jobs", Code: "job_failed"}
			if e := rows.Scan(&v.Status, &v.FirstAt, &v.LastAt, &v.Count); e != nil {
				rows.Close()
				return nil, e
			}
			if v.Status == "queued" {
				v.Status = "retrying"
			}
			v.Href = "/admin/system/jobs"
			if v.Status == "dead" || v.Status == "retrying" {
				v.Href += "?status=" + v.Status
			}
			out = append(out, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	if (f.Source == "" || f.Source == "audit") && (f.Status == "" || f.Status == "recorded") && f.HTTPStatus == 0 {
		rows, e := h.errorInventoryAudit(ctx, f, since, until)
		if e != nil {
			return nil, e
		}
		out = append(out, rows...)
	}
	slices.SortFunc(out, func(a, b errorInventoryRow) int {
		if n := strings.Compare(b.LastAt, a.LastAt); n != 0 {
			return n
		}
		return strings.Compare(a.Source+a.Category+a.Code, b.Source+b.Category+b.Code)
	})
	return out, nil
}

var inventoryAuditActions = map[string]struct{ category, code string }{"user.login_failed": {"auth", "login_failed"}, "security.ip_blocked": {"auth", "login_blocked"}, "user.setup_denied": {"auth", "setup_denied"}, "site.form.failed": {"site", "form_failed"}, "ext.error": {"ext", "extension_failed"}, "commerce.entitlement.suspend_failed": {"commerce", "suspend_failed"}, "mcp.call": {"mcp", "tool_failed"}}

func (h *handlers) errorInventoryAudit(ctx context.Context, f errorInventoryFilter, since, until string) ([]errorInventoryRow, error) {
	actions := []string{}
	args := []any{}
	for action, v := range inventoryAuditActions {
		if f.Category == "" || v.category == f.Category {
			actions = append(actions, action)
		}
	}
	if len(actions) == 0 {
		return nil, nil
	}
	slices.Sort(actions)
	for _, a := range actions {
		args = append(args, a)
	}
	conds := []string{`action IN (` + strings.TrimSuffix(strings.Repeat("?,", len(actions)), ",") + `)`, `(action!='mcp.call' OR json_extract(meta_json,'$.ok')=0)`}
	if since != "" {
		conds = append(conds, "at>=?")
		args = append(args, since)
	}
	if until != "" {
		conds = append(conds, "at<?")
		args = append(args, until)
	}
	rows, e := h.d.DB.QueryContext(ctx, `SELECT action,MIN(at),MAX(at),COUNT(*) FROM audit_log WHERE `+strings.Join(conds, " AND ")+` GROUP BY action`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []errorInventoryRow{}
	for rows.Next() {
		v := errorInventoryRow{Source: "audit", Status: "recorded"}
		var action string
		if e = rows.Scan(&action, &v.FirstAt, &v.LastAt, &v.Count); e != nil {
			return nil, e
		}
		info := inventoryAuditActions[action]
		v.Category, v.Code = info.category, info.code
		v.Href = "/admin/agents/audit?action=" + url.QueryEscape(action)
		out = append(out, v)
	}
	return out, rows.Err()
}
