package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/auth"
)

// ServiceCanvasRead is the kernel service name for grant-scoped canvas reads.
const ServiceCanvasRead = "admin.canvases"

// CanvasSummary is one board in list_canvases / GET /api/v1/canvases.
type CanvasSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
	Version   int64  `json:"version"`
}

// CanvasDocument is get_canvas / GET /api/v1/canvases/{id} (no secrets).
type CanvasDocument struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	UpdatedAt string       `json:"updated_at"`
	Version   int64        `json:"version"`
	Cards     []canvasCard `json:"cards"`
	Edges     []canvasEdge `json:"edges"`
}

// CanvasReader is the grant-scoped list/get surface for MCP and REST.
type CanvasReader interface {
	List(ctx context.Context, page, perPage int) ([]CanvasSummary, int, error)
	Get(ctx context.Context, id string) (*CanvasDocument, error)
}

type canvasCodedError struct {
	code, msg string
}

func (e *canvasCodedError) Error() string     { return e.msg }
func (e *canvasCodedError) ErrorCode() string { return e.code }

func canvasReadForbidden() error {
	return &canvasCodedError{code: "forbidden", msg: "forbidden"}
}

func canvasReadNotFound() error {
	return &canvasCodedError{code: "not_found", msg: "canvas not found"}
}

func canvasReadInvalid() error {
	return &canvasCodedError{code: "validation_failed", msg: "invalid canvas request"}
}

func (h *handlers) List(ctx context.Context, page, perPage int) ([]CanvasSummary, int, error) {
	return h.canvasListReadable(ctx, page, perPage)
}

func (h *handlers) Get(ctx context.Context, id string) (*CanvasDocument, error) {
	return h.canvasGetReadable(ctx, id)
}

func (h *handlers) canvasIsAgentKey(ctx context.Context) bool {
	actor := auth.ActorFromContext(ctx)
	if actor.Kind != "api_key" || actor.ID == "" {
		return false
	}
	k, err := h.keys.Get(ctx, actor.ID)
	return err == nil && k != nil && k.Kind == auth.KeyKindAgent
}

func canvasPage(page, perPage int) (int, int) {
	if page < 1 {
		page = 1
	}
	switch {
	case perPage <= 0:
		perPage = 20
	case perPage > 100:
		perPage = 100
	}
	return page, perPage
}

func (h *handlers) canvasListReadable(ctx context.Context, page, perPage int) ([]CanvasSummary, int, error) {
	if !auth.Can(ctx, "canvas.read") || !auth.Can(ctx, "content.view") {
		return nil, 0, canvasReadForbidden()
	}
	page, perPage = canvasPage(page, perPage)
	var all []CanvasSummary
	var err error
	if h.canvasIsAgentKey(ctx) {
		all, err = h.canvasListGranted(ctx)
	} else {
		all, err = h.canvasListOwnedSummaries(ctx)
	}
	if err != nil {
		return nil, 0, err
	}
	if all == nil {
		all = []CanvasSummary{}
	}
	total := len(all)
	start := (page - 1) * perPage
	if start >= total {
		return []CanvasSummary{}, total, nil
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return all[start:end], total, nil
}

func (h *handlers) canvasListOwnedSummaries(ctx context.Context) ([]CanvasSummary, error) {
	boards, err := h.canvasBoards(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []CanvasSummary{}, nil
		}
		return nil, err
	}
	out := make([]CanvasSummary, 0, len(boards))
	for _, b := range boards {
		out = append(out, CanvasSummary{ID: b.ID, Title: b.Title, UpdatedAt: b.UpdatedAt, Version: b.Version})
	}
	return out, nil
}

func (h *handlers) canvasListGranted(ctx context.Context) ([]CanvasSummary, error) {
	u := auth.UserFromContext(ctx)
	actor := auth.ActorFromContext(ctx)
	if u == nil || actor.ID == "" {
		return nil, canvasReadForbidden()
	}
	rows, err := h.d.DB.QueryContext(ctx, `
SELECT b.id, b.title, b.version, b.updated_at
FROM admin_canvas_grants g
JOIN admin_canvases b ON b.id = g.board_id
WHERE g.key_id = ? AND g.can_read = 1 AND b.owner_id = ?
ORDER BY b.updated_at DESC, b.id
LIMIT 100`, actor.ID, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CanvasSummary{}
	for rows.Next() {
		var s CanvasSummary
		if err = rows.Scan(&s.ID, &s.Title, &s.Version, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *handlers) canvasGetReadable(ctx context.Context, id string) (*CanvasDocument, error) {
	if !auth.Can(ctx, "canvas.read") || !auth.Can(ctx, "content.view") {
		return nil, canvasReadForbidden()
	}
	if !connectionIDPattern.MatchString(id) {
		return nil, canvasReadNotFound()
	}
	b, err := h.canvasOwned(ctx, id)
	if err != nil {
		return nil, canvasReadNotFound()
	}
	doc := &CanvasDocument{
		ID: b.ID, Title: b.Title, UpdatedAt: b.UpdatedAt, Version: b.Version,
		Cards: b.Cards, Edges: b.Edges,
	}
	if doc.Cards == nil {
		doc.Cards = []canvasCard{}
	}
	if doc.Edges == nil {
		doc.Edges = []canvasEdge{}
	}
	if !h.canvasIsAgentKey(ctx) {
		return doc, nil
	}
	actor := auth.ActorFromContext(ctx)
	if !h.canvasGrantAllows(ctx, b.ID, actor.ID, "read", nil) {
		return nil, canvasReadForbidden()
	}
	allowed, err := h.canvasGrantNodeIDs(ctx, b.ID, actor.ID)
	if err != nil {
		return nil, err
	}
	doc.Cards, doc.Edges = canvasFilterGranted(doc.Cards, doc.Edges, allowed)
	return doc, nil
}

func (h *handlers) canvasGrantNodeIDs(ctx context.Context, boardID, keyID string) ([]string, error) {
	var raw string
	if err := h.d.DB.QueryRowContext(ctx, `SELECT node_ids FROM admin_canvas_grants WHERE board_id=? AND key_id=? AND can_read=1`, boardID, keyID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, canvasReadForbidden()
		}
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, canvasReadForbidden()
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func canvasFilterGranted(cards []canvasCard, edges []canvasEdge, allowed []string) ([]canvasCard, []canvasEdge) {
	ok := map[string]bool{}
	for _, id := range allowed {
		ok[id] = true
	}
	outCards := make([]canvasCard, 0, len(cards))
	for _, c := range cards {
		if ok[c.ID] {
			outCards = append(outCards, c)
		}
	}
	outEdges := make([]canvasEdge, 0, len(edges))
	for _, e := range edges {
		if ok[e.From] && ok[e.To] {
			outEdges = append(outEdges, e)
		}
	}
	return outCards, outEdges
}
