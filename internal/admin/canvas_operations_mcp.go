package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

type canvasMCPInput struct {
	BoardID   string `json:"board_id"`
	RunID     string `json:"run_id,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
	Token     string `json:"context_token,omitempty"`
	Text      string `json:"text,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func (h *handlers) canvasMCPTools() {
	for _, spec := range []struct{ name, scope string }{{"canvases.context", "canvas.read"}, {"canvases.pending", "canvas.read"}, {"canvases.request_run", "canvas.run"}, {"canvases.append_result", "canvas.append"}} {
		name, scope := spec.name, spec.scope
		h.d.MCP.AddTool(kernel.MCPTool{Name: name, Description: "Selected-board operations only. Named-agent tasks require explicit owner review. Results append notes and never publish content.", Input: canvasMCPInput{}, Scopes: []string{scope, "content.view"}, Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in canvasMCPInput
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if len(raw) > 16384 || dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
				return nil, errors.New("invalid canvas request")
			}
			if !auth.Can(ctx, scope) || !auth.Can(ctx, "content.view") {
				return nil, errCanvasConsent
			}
			actor := auth.ActorFromContext(ctx)
			if actor.Kind != "api_key" {
				return nil, errCanvasConsent
			}
			b, e := h.canvasOwned(ctx, in.BoardID)
			if e != nil {
				return nil, errCanvasConsent
			}
			if _, e = h.canvasAgentKey(ctx, actor.ID); e != nil {
				return nil, errCanvasConsent
			}
			switch name {
			case "canvases.pending":
				if !h.canvasGrantAllows(ctx, b.ID, actor.ID, "read", nil) {
					return nil, errCanvasConsent
				}
				rows, e := h.d.DB.QueryContext(ctx, `SELECT id,node_id,created_at FROM admin_canvas_runs WHERE board_id=? AND key_id=? AND status='waiting_agent' ORDER BY created_at LIMIT 20`, b.ID, actor.ID)
				if e != nil {
					return nil, e
				}
				defer rows.Close()
				out := []map[string]string{}
				for rows.Next() {
					var id, node, created string
					if e = rows.Scan(&id, &node, &created); e != nil {
						return nil, e
					}
					out = append(out, map[string]string{"id": id, "node_id": node, "created_at": created})
				}
				return map[string]any{"runs": out}, rows.Err()
			case "canvases.context", "canvases.append_result":
				var raw, status string
				e = h.d.DB.QueryRowContext(ctx, `SELECT document,status FROM admin_canvas_runs WHERE id=? AND board_id=? AND key_id=?`, in.RunID, b.ID, actor.ID).Scan(&raw, &status)
				if e != nil {
					return nil, errCanvasConsent
				}
				var p canvasPrepared
				if json.Unmarshal([]byte(raw), &p) != nil {
					return nil, errCanvasConsent
				}
				action := "read"
				if name == "canvases.append_result" {
					action = "append"
				}
				if !h.canvasGrantAllows(ctx, b.ID, actor.ID, action, p.Nodes) || !h.canvasGrantAllows(ctx, b.ID, actor.ID, "run", p.Nodes) || !h.canvasScheduleActive(ctx, p) {
					return nil, errCanvasConsent
				}
				if status == "succeeded" && name == "canvases.append_result" {
					return h.canvasAppendRetry(ctx, in.RunID, in.Text)
				}
				if status != "waiting_agent" {
					return nil, errCanvasConsent
				}
				// Source access uses the current key principal, so a scope revocation is effective immediately.
				current, e := h.canvasPrepare(ctx, b, p.NodeID)
				if e != nil || current.ReviewHash != p.ReviewHash {
					return nil, errCanvasConsent
				}
				token := studioReviewHash(map[string]any{"run": in.RunID, "key": actor.ID, "review": p.ReviewHash})
				if name == "canvases.context" {
					return map[string]any{"run_id": in.RunID, "context_token": token, "prompt": p.Prompt, "context": p.Context, "board_version": b.Version, "model_preference": p.Provider["model"]}, nil
				}
				if in.Token != token {
					return nil, errCanvasConsent
				}
				p.Version = b.Version
				if e = h.canvasAppend(ctx, in.RunID, p, strings.TrimSpace(in.Text), "api_key", actor.Name, "waiting_agent"); e != nil {
					return nil, e
				}
				return map[string]bool{"ok": true}, nil
			case "canvases.request_run":
				if !canvasRequestID(in.RequestID) || !h.canvasGrantAllows(ctx, b.ID, actor.ID, "run", nil) {
					return nil, errCanvasConsent
				}
				valid := false
				for _, c := range b.Cards {
					if c.ID == in.NodeID && (c.Kind == "provider" || c.Kind == "agent") {
						valid = true
					}
				}
				if !valid {
					return nil, errCanvasInvalid
				}
				// Requesting is not execution. Only a later owner-session preview can enqueue a provider call.
				id := db.NewID()
				p := canvasPrepared{BoardID: b.ID, OwnerID: auth.UserFromContext(ctx).ID, NodeID: in.NodeID, Provider: map[string]string{"title": actor.Name}}
				_, e = h.d.DB.ExecContext(ctx, `INSERT INTO admin_canvas_runs(id,board_id,owner_id,key_id,request_id,node_id,status,document,created_at) SELECT ?,?,?,?,?,?,'requested',?,? WHERE (SELECT count(*) FROM admin_canvas_runs WHERE board_id=? AND status='requested')<100 ON CONFLICT(board_id,request_id) DO NOTHING`, id, b.ID, p.OwnerID, actor.ID, "request:"+in.RequestID, in.NodeID, canvasEncode(p), db.Now(), b.ID)
				if e != nil {
					return nil, e
				}
				if e = h.d.DB.QueryRowContext(ctx, `SELECT id FROM admin_canvas_runs WHERE board_id=? AND request_id=? AND key_id=? AND node_id=?`, b.ID, "request:"+in.RequestID, actor.ID, in.NodeID).Scan(&id); e != nil {
					return nil, errCanvasConsent
				}
				return map[string]string{"id": id, "status": "requested"}, nil
			}
			return nil, errCanvasInvalid
		}})
	}
}
func (h *handlers) canvasAppendRetry(ctx context.Context, id, text string) (any, error) {
	var raw, card string
	e := h.d.DB.QueryRowContext(ctx, `SELECT b.document_json,r.result_card_id FROM admin_canvas_runs r JOIN admin_canvases b ON b.id=r.board_id WHERE r.id=? AND b.owner_id=?`, id, auth.UserFromContext(ctx).ID).Scan(&raw, &card)
	if e != nil {
		return nil, e
	}
	var cards []canvasCard
	if json.Unmarshal([]byte(raw), &cards) != nil {
		return nil, errCanvasInvalid
	}
	for _, c := range cards {
		if c.ID == card && c.Body == strings.TrimSpace(text) {
			return map[string]bool{"ok": true}, nil
		}
	}
	return nil, errCanvasConsent
}
