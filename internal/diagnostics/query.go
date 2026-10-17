package diagnostics

import (
	"context"
	"strings"
)

type Row struct {
	Category, Code, FirstAt, LastAt string
	HTTPStatus                      int
	Count                           int64
}
type Filter struct {
	Category, Since, Until string
	HTTPStatus, Limit      int
}

func (c *Collector) List(ctx context.Context, f Filter) ([]Row, error) {
	conditions := []string{"1=1"}
	args := []any{}
	for _, p := range []struct {
		sql string
		v   string
	}{{"category=?", f.Category}, {"last_at>=?", f.Since}, {"first_at<?", f.Until}} {
		if p.v != "" {
			conditions = append(conditions, p.sql)
			args = append(args, p.v)
		}
	}
	if f.HTTPStatus != 0 {
		conditions = append(conditions, "http_status=?")
		args = append(args, f.HTTPStatus)
	}
	limit := f.Limit
	if limit < 1 || limit > 200 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := c.db.QueryContext(ctx, `SELECT category,code,http_status,MIN(first_at),MAX(last_at),SUM(count) FROM diagnostic_events WHERE `+strings.Join(conditions, " AND ")+` GROUP BY category,code,http_status ORDER BY MAX(last_at) DESC,category,code LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.Category, &r.Code, &r.HTTPStatus, &r.FirstAt, &r.LastAt, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
