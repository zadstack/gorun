package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

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
	Ref       string `json:"ref,omitempty"`
}

// Per-tool inputs: they define each tool's published schema. Calls are
// decoded into canvasMCPInput (their union), so arguments accepted before
// the split keep working.
type canvasBoardInput struct {
	BoardID string `json:"board_id" jsonschema:"a board this agent key holds a grant for (list_canvases)"`
}

type canvasRunInput struct {
	BoardID string `json:"board_id"`
	RunID   string `json:"run_id" jsonschema:"a waiting run of this key, from list_canvas_pending_runs"`
}

type canvasSourceInput struct {
	BoardID string `json:"board_id"`
	RunID   string `json:"run_id"`
	Ref     string `json:"ref" jsonschema:"one of the refs returned with the run context, e.g. content/<id> or note/<card id>"`
}

type canvasRequestInput struct {
	BoardID   string `json:"board_id"`
	NodeID    string `json:"node_id" jsonschema:"id of a provider or agent card on the board"`
	RequestID string `json:"request_id" jsonschema:"client-chosen idempotency key (card-id style); repeating it returns the same run"`
}

type canvasAppendInput struct {
	BoardID string `json:"board_id"`
	RunID   string `json:"run_id"`
	Token   string `json:"context_token" jsonschema:"context_token returned by get_canvas_context for this run"`
	Text    string `json:"text" jsonschema:"the result note text (plain text)"`
}

// canvasOps are the selected-board agent operations. op is the internal
// operation key (the original tool name).
var canvasOps = []struct {
	op, name, scope, title, description string
	input                               any
	readOnly                            bool
}{
	{"canvases.pending", "canvases.pending", "canvas.read", "List pending canvas runs",
		"List up to 20 runs on one granted board that wait for this agent key (status waiting_agent), oldest first: {runs:[{id,node_id,created_at}]}. Next: get_canvas_context with a run id.",
		canvasBoardInput{}, true},
	{"canvases.context", "canvases.context", "canvas.read", "Get canvas run context",
		"Read the bounded prompt and context of one waiting run, plus the context_token needed to append its result. Context sources start with a \"## [ref:…]\" header; a shortened source ends with a \"[… N more characters. Full text: ref:…]\" marker (fetch it with get_canvas_source). Reading changes nothing.",
		canvasRunInput{}, true},
	{"canvases.source", "canvases.source", "canvas.read", "Get canvas run source",
		"Return the full current text of one ref listed in that run's context, at most 200,000 characters: characters is the returned length, total_characters the full length, truncated is true when cut. Other refs are refused.",
		canvasSourceInput{}, true},
	{"canvases.request_run", "canvases.request_run", "canvas.run", "Request canvas run",
		"Ask for a run of a provider or agent card. It only creates a requested run for the board owner to review; nothing executes until a human starts it. Idempotent per request_id.",
		canvasRequestInput{}, false},
	{"canvases.append_result", "canvases.append_result", "canvas.append", "Append canvas result",
		"Append the result of a waiting run as a new private note card (with the run's context_token). Never edits or deletes cards and never publishes content. Repeating the same text after success is a no-op.",
		canvasAppendInput{}, false},
}

func (h *handlers) canvasMCPTools() {
	for _, spec := range canvasOps {
		op, scope := spec.op, spec.scope
		h.d.MCP.AddTool(kernel.MCPTool{Name: spec.name, Title: spec.title, ReadOnly: spec.readOnly, Idempotent: !spec.readOnly,
			Description: spec.description + " Selected-board operation: the key needs a board grant; named-agent tasks require owner review.",
			Input:       spec.input, Scopes: []string{scope, "content.view"},
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				out, err := h.canvasOperation(ctx, op, scope, raw)
				return out, canvasMCPError(err)
			}})
	}
}

var (
	// errCanvasRequest is an undecodable or oversized MCP canvas request.
	errCanvasRequest = errors.New("invalid canvas request")
	// errCanvasMCPForbidden is the one generic refusal agents receive.
	errCanvasMCPForbidden = &canvasCodedError{code: "forbidden",
		msg: "not allowed: the board grant, run, review or context_token is missing, expired or changed"}
	errCanvasMCPInvalid = &canvasCodedError{code: "validation_failed",
		msg: "invalid canvas request: check board_id, run_id, node_id, request_id and the argument names"}
)

// canvasMCPError gives the agent a stable error code: a missing or changed
// grant, consent, run or token is forbidden (without telling which, so
// boards and runs of others stay invisible); a malformed request is
// validation_failed. Anything else stays an internal error.
func canvasMCPError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errCanvasConsent):
		return errCanvasMCPForbidden
	case errors.Is(err, errCanvasInvalid), errors.Is(err, errCanvasRequest):
		return errCanvasMCPInvalid
	}
	return err
}

// canvasOperation runs one selected-board operation for the API key in ctx.
func (h *handlers) canvasOperation(ctx context.Context, name, scope string, raw json.RawMessage) (any, error) {
	var in canvasMCPInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if len(raw) > 16384 || dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, errCanvasRequest
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
	case "canvases.context", "canvases.source", "canvases.append_result":
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
			return map[string]any{"run_id": in.RunID, "context_token": token, "prompt": p.Prompt, "context": p.Context, "refs": p.Refs, "spilled": p.Spilled, "board_version": b.Version, "model_preference": p.Provider["model"]}, nil
		}
		if name == "canvases.source" {
			// Only refs recorded for this run, and still produced by the
			// current preparation (same authority checks as execution).
			text, ok := current.full[in.Ref]
			if !slices.Contains(p.Refs, in.Ref) || !slices.Contains(current.Refs, in.Ref) || !ok {
				return nil, errCanvasConsent
			}
			total := utf8.RuneCountInString(text)
			if total > canvasSourceMax {
				text = string([]rune(text)[:canvasSourceMax])
			}
			return map[string]any{"run_id": in.RunID, "ref": in.Ref, "text": text, "characters": min(total, canvasSourceMax), "total_characters": total, "truncated": total > canvasSourceMax}, nil
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
