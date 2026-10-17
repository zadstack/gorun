package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

type canvasCheck struct {
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}
type canvasCard struct {
	ID             string        `json:"id"`
	Kind           string        `json:"kind"`
	Title          string        `json:"title"`
	Body           string        `json:"body"`
	X              int           `json:"x"`
	Y              int           `json:"y"`
	URL            string        `json:"url,omitempty"`
	ContentID      string        `json:"content_id,omitempty"`
	WidgetID       string        `json:"widget_id,omitempty"`
	SourceType     string        `json:"source_type,omitempty"`
	SourceLimit    int           `json:"source_limit,omitempty"`
	SourceIDs      []string      `json:"source_ids,omitempty"`
	GenerationKind string        `json:"generation_kind,omitempty"`
	PresetID       string        `json:"preset_id,omitempty"`
	RunID          string        `json:"run_id,omitempty"`
	AgentID        string        `json:"agent_id,omitempty"`
	ProviderID     string        `json:"provider_id,omitempty"`
	Interval       string        `json:"interval,omitempty"`
	Items          []canvasCheck `json:"items,omitempty"`
}
type canvasEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type canvasBoard struct {
	Edges     []canvasEdge `json:"edges"`
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Version   int64        `json:"version"`
	UpdatedAt string       `json:"updated_at"`
	Cards     []canvasCard `json:"cards"`
}

var errCanvasLimit = errors.New("canvas limit")

func (h *handlers) canvasBoards(ctx context.Context) ([]canvasBoard, error) {
	out := []canvasBoard{}
	u := auth.UserFromContext(ctx)
	if u == nil {
		return out, sql.ErrNoRows
	}
	rows, e := h.d.DB.QueryContext(ctx, `SELECT id,title,version,updated_at FROM admin_canvases WHERE owner_id=? ORDER BY updated_at DESC,id LIMIT 100`, u.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var b canvasBoard
		if e = rows.Scan(&b.ID, &b.Title, &b.Version, &b.UpdatedAt); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (h *handlers) canvasOwned(ctx context.Context, id string) (*canvasBoard, error) {
	u := auth.UserFromContext(ctx)
	if u == nil || !connectionIDPattern.MatchString(id) {
		return nil, sql.ErrNoRows
	}
	var b canvasBoard
	var raw, edges string
	e := h.d.DB.QueryRowContext(ctx, `SELECT id,title,version,updated_at,document_json,edges_json FROM admin_canvases WHERE id=? AND owner_id=?`, id, u.ID).Scan(&b.ID, &b.Title, &b.Version, &b.UpdatedAt, &raw, &edges)
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal([]byte(raw), &b.Cards); e != nil {
		return nil, e
	}
	if e = json.Unmarshal([]byte(edges), &b.Edges); e != nil {
		return nil, e
	}
	return &b, nil
}
func (h *handlers) canvasCreate(ctx context.Context, title string) (string, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return "", sql.ErrNoRows
	}
	id, now := db.NewID(), db.Now()
	res, e := h.d.DB.ExecContext(ctx, `INSERT INTO admin_canvases(id,owner_id,title,document_json,version,created_at,updated_at) SELECT ?,?,?,'[]',1,?,? WHERE (SELECT count(*) FROM admin_canvases WHERE owner_id=?)<100`, id, u.ID, title, now, now, u.ID)
	if e != nil {
		return "", e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return "", e
	}
	if n != 1 {
		return "", errCanvasLimit
	}
	return id, nil
}
