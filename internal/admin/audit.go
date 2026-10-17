package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
)

const metaPreviewMax = 4000

// auditActorKinds are the actor_kind values of audit_log.
var auditActorKinds = []string{"user", "api_key", "system", "extension"}

// auditFilter is the audit viewer / API filter. Action is a prefix
// (audit.ListQuery only matches exact actions). Since/Until are dates
// (YYYY-MM-DD, inclusive) or RFC3339 instants.
type auditFilter struct {
	ActorKind, ActorID, Action, TargetKind, TargetID, Since, Until string
	Page, PerPage                                                  int
}

func auditFilterFrom(q url.Values) auditFilter {
	page, perPage := pageParams(q)
	get := func(k string) string { return strings.TrimSpace(q.Get(k)) }
	return auditFilter{ActorKind: get("actor_kind"), ActorID: get("actor_id"), Action: get("action"), TargetKind: get("target_kind"),
		TargetID: get("target_id"), Since: get("since"), Until: get("until"), Page: page, PerPage: perPage}
}

// bound converts a date or RFC3339 value to an RFC3339 UTC bound; endOfDay
// moves a date to the next midnight (the exclusive upper bound).
func bound(v string, endOfDay bool) (string, bool) {
	if v == "" {
		return "", true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return db.FormatTime(t), true
	}
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return "", false
	}
	if endOfDay {
		t = t.AddDate(0, 0, 1)
	}
	return db.FormatTime(t), true
}

// listAudit reads audit_log with prefix matching on action.
func listAudit(ctx context.Context, d *sql.DB, f auditFilter) ([]audit.Entry, int, error) {
	var conds []string
	var args []any
	add := func(cond string, v any) { conds, args = append(conds, cond), append(args, v) }
	if f.ActorKind != "" {
		add("actor_kind = ?", f.ActorKind)
	}
	if f.ActorID != "" {
		add("actor_id = ?", f.ActorID)
	}
	if f.Action != "" {
		add(`action LIKE ? ESCAPE '\'`, likeEscape(f.Action)+"%")
	}
	if f.TargetKind != "" {
		add("target_kind = ?", f.TargetKind)
	}
	if f.TargetID != "" {
		add("target_id = ?", f.TargetID)
	}
	if since, ok := bound(f.Since, false); ok && since != "" {
		add("at >= ?", since)
	}
	if until, ok := bound(f.Until, true); ok && until != "" {
		add("at < ?", until)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM audit_log`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("admin: count audit: %w", err)
	}
	rows, err := d.QueryContext(ctx, `SELECT id, at, actor_kind, actor_id, actor_name, action, target_kind, target_id, ip, meta_json
		FROM audit_log`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, f.PerPage, (f.Page-1)*f.PerPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("admin: list audit: %w", err)
	}
	defer rows.Close()
	out := []audit.Entry{}
	for rows.Next() {
		var e audit.Entry
		var meta string
		if err := rows.Scan(&e.ID, &e.At, &e.Actor.Kind, &e.Actor.ID, &e.Actor.Name, &e.Action, &e.TargetKind, &e.TargetID, &e.IP, &meta); err != nil {
			return nil, 0, fmt.Errorf("admin: scan audit: %w", err)
		}
		e.Meta = json.RawMessage(meta)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// auditView adds the pretty-printed meta.
type auditView struct {
	audit.Entry
	MetaPretty string
	HasMeta    bool
}

func viewAudit(list []audit.Entry) []auditView {
	out := make([]auditView, len(list))
	for i, e := range list {
		m := strings.TrimSpace(string(e.Meta))
		out[i] = auditView{Entry: e, MetaPretty: prettyJSON(e.Meta, metaPreviewMax), HasMeta: m != "" && m != "{}" && m != "null"}
	}
	return out
}

type auditPage struct {
	Entries    []auditView
	Filter     auditFilter
	ActorKinds []string
	Pager      pager
	BadDates   bool
}

func (h *handlers) auditList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := auditFilterFrom(q)
	_, okSince := bound(f.Since, false)
	_, okUntil := bound(f.Until, true)
	list, total, err := listAudit(r.Context(), h.d.DB, f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.tpl.Render(w, r, "pages/agents/audit.html", auditPage{Entries: viewAudit(list), Filter: f, ActorKinds: auditActorKinds,
		Pager: newPager("/admin/agents/audit", q, f.Page, f.PerPage, total), BadDates: !okSince || !okUntil})
}
