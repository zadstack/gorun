package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

type listCanvasesInput struct {
	Page    int `json:"page,omitempty"`
	PerPage int `json:"per_page,omitempty"`
}

type getCanvasInput struct {
	ID string `json:"id"`
}

func (h *handlers) canvasReadMCPTools() {
	h.d.MCP.AddTool(kernel.MCPTool{
		Name:        "list_canvases",
		Description: "List canvas boards the key may read: owner boards for REST/session keys, or boards with an explicit read grant for agent keys. Returns id, title, updated_at and version only.",
		Scopes:      []string{"canvas.read", "content.view"},
		Input:       listCanvasesInput{},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in listCanvasesInput
			if err := decodeCanvasReadInput(raw, &in); err != nil {
				return nil, err
			}
			if auth.ActorFromContext(ctx).Kind != "api_key" {
				return nil, canvasReadForbidden()
			}
			items, total, err := h.canvasListReadable(ctx, in.Page, in.PerPage)
			if err != nil {
				return nil, err
			}
			page, per := canvasPage(in.Page, in.PerPage)
			return map[string]any{"items": items, "total": total, "page": page, "per_page": per}, nil
		},
	})
	h.d.MCP.AddTool(kernel.MCPTool{
		Name:        "get_canvas",
		Description: "Read one canvas board by id. Agent keys receive only grant-allowed cards and edges between them. Never returns inbound secrets or provider credentials.",
		Scopes:      []string{"canvas.read", "content.view"},
		Input:       getCanvasInput{},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in getCanvasInput
			if err := decodeCanvasReadInput(raw, &in); err != nil {
				return nil, err
			}
			if auth.ActorFromContext(ctx).Kind != "api_key" {
				return nil, canvasReadForbidden()
			}
			if in.ID == "" {
				return nil, canvasReadInvalid()
			}
			return h.canvasGetReadable(ctx, in.ID)
		},
	})
}

func decodeCanvasReadInput(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if len(raw) > 16384 || dec.Decode(dst) != nil || dec.Decode(new(any)) != io.EOF {
		return canvasReadInvalid()
	}
	return nil
}
