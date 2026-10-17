package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

// Error codes returned in the tool error envelope.
const (
	codeForbidden    = "forbidden"
	codeNotFound     = "not_found"
	codeValidation   = "validation_failed"
	codeConflict     = "conflict"
	codeRateLimit    = "rate_limited"
	codeUnauthorized = "unauthorized"
	codeError        = "error"
	codeInternal     = "internal"
)

// ToolError is an error a tool returns to the agent as-is (code, message,
// optional per-field messages).
type ToolError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

func errForbidden() *ToolError { return &ToolError{Code: codeForbidden, Message: "forbidden"} }

func invalid(msg string, fields map[string]string) *ToolError {
	return &ToolError{Code: codeValidation, Message: msg, Fields: fields}
}

func notFound(what string) *ToolError {
	return &ToolError{Code: codeNotFound, Message: what + " not found"}
}

var notFoundErrors = []error{content.ErrNotFound, taxonomy.ErrNotFound, media.ErrNotFound, menus.ErrNotFound,
	theme.ErrNotFound, auth.ErrNotFound}

// inputErrors are service errors whose message describes a problem with the
// agent's input and is safe to return.
var inputErrors = []error{theme.ErrInvalidTemplate, theme.ErrInvalidName, media.ErrTooLarge, media.ErrEmpty,
	media.ErrNotAllowed, media.ErrInvalidSVG}

// toToolError classifies err for the agent: ToolError as-is, validation
// errors with their fields, input errors (template syntax, upload type),
// known not-found sentinels; known reports false for anything else, which
// gets a generic message.
func toToolError(err error) (te *ToolError, known bool) {
	if errors.Is(err, content.ErrConflict) {
		return &ToolError{Code: codeConflict, Message: "Content changed; retry the request so live changes can receive approval."}, true
	}
	if errors.As(err, &te) {
		return te, true
	}
	var coded interface {
		ErrorCode() string
		Error() string
	}
	if errors.As(err, &coded) {
		switch coded.ErrorCode() {
		case codeForbidden, codeNotFound, codeConflict, codeValidation:
			var fields map[string]string
			if detailed, ok := coded.(interface{ FieldErrors() map[string]string }); ok {
				fields = detailed.FieldErrors()
			}
			return &ToolError{Code: coded.ErrorCode(), Message: coded.Error(), Fields: fields}, true
		default:
			return &ToolError{Code: codeInternal, Message: "internal error"}, false
		}
	}
	if status, fields := api.ParseValidation(err); status != 0 {
		return invalid(err.Error(), fields), true
	}
	for _, s := range inputErrors {
		if errors.Is(err, s) {
			return invalid(err.Error(), nil), true
		}
	}
	for _, s := range notFoundErrors {
		if errors.Is(err, s) {
			return &ToolError{Code: codeNotFound, Message: err.Error()}, true
		}
	}
	return &ToolError{Code: codeInternal, Message: "internal error"}, false
}

// classify is toToolError that logs unclassified errors (never sent).
func (s *server) classify(tool string, err error) *ToolError {
	te, known := toToolError(err)
	if !known {
		s.log.Error("mcp: tool failed", "tool", tool, "err", err)
	}
	return te
}

// envelope builds the response envelope, classifying err.
func (s *server) envelope(tool string, data any, err error) envelope {
	if err != nil {
		return envelope{OK: false, Error: s.classify(tool, err)}
	}
	return newEnvelope(data, nil)
}

// pendingResult is returned by a sensitive tool awaiting approval.
type pendingResult struct {
	Status     string `json:"status"`
	ApprovalID string `json:"approval_id"`
	Tool       string `json:"tool"`
	ExpiresAt  string `json:"expires_at"`
	Message    string `json:"message"`
}

// envelope is the response envelope shared with the REST API (spec §2.5).
type envelope struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data,omitempty"`
	Error *ToolError `json:"error,omitempty"`
}

func newEnvelope(data any, err error) envelope {
	if err != nil {
		te, _ := toToolError(err)
		return envelope{OK: false, Error: te}
	}
	if data == nil {
		data = map[string]any{}
	}
	return envelope{OK: true, Data: data}
}

// argsHash fingerprints call arguments for the audit log (the arguments
// themselves are never logged).
func argsHash(args json.RawMessage) string {
	sum := sha256.Sum256(args)
	return hex.EncodeToString(sum[:])
}

// normalizeArgs turns absent or null arguments into an empty object.
func normalizeArgs(args json.RawMessage) json.RawMessage {
	if t := bytes.TrimSpace(args); len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return json.RawMessage(`{}`)
	}
	return args
}

// allowed reports whether ctx holds every scope of t (tools without scopes
// are open to any authenticated key).
func allowed(ctx context.Context, t kernel.MCPTool) bool {
	for _, sc := range t.Scopes {
		if !auth.Can(ctx, sc) {
			return false
		}
	}
	return true
}

// call runs one tool call for the principal in ctx: scope check, approval
// gate for sensitive tools, the handler, and one mcp.call audit row.
func (s *server) call(ctx context.Context, t kernel.MCPTool, args json.RawMessage) (any, error) {
	start := time.Now()
	args = normalizeArgs(args)
	meta := map[string]any{"args_hash": argsHash(args)}
	data, err := s.dispatch(ctx, t, args, meta)
	meta["ok"] = err == nil
	meta["duration_ms"] = time.Since(start).Milliseconds()
	if err != nil {
		meta["error_code"] = s.classify(t.Name, err).Code
	}
	s.d.Audit.Record(ctx, "mcp.call", "tool", t.Name, meta)
	return data, err
}

func (s *server) dispatch(ctx context.Context, t kernel.MCPTool, args json.RawMessage, meta map[string]any) (any, error) {
	if !allowed(ctx, t) {
		return nil, errForbidden()
	}
	sensitive := s.sensitive(ctx, t, args)
	dynamic := s.dynamic[t.Name] != nil
	selfApproved := (sensitive || dynamic) && s.selfApproved(ctx)
	if sensitive && !selfApproved {
		if err := validateParked(t, args); err != nil {
			return nil, err
		}
		ap, err := s.approvals.create(ctx, t.Name, args)
		if err != nil {
			return nil, err
		}
		meta["approval_id"], meta["pending"] = ap.ID, true
		return pendingResult{Status: statusPendingApproval, ApprovalID: ap.ID, Tool: t.Name, ExpiresAt: ap.ExpiresAt,
			Message: "This tool needs human approval. Poll check_approval with approval_id."}, nil
	}
	if dynamic && !selfApproved {
		ctx = content.GuardUnapprovedMutation(ctx)
	}
	return s.execute(ctx, t, args)
}

// selfApproved reports whether the API key in ctx may run sensitive tools
// without approval: mcp.approve_self must be listed in the key's own scopes
// ("*" does not imply it) and the owner's role must allow it.
func (s *server) selfApproved(ctx context.Context) bool {
	actor := auth.ActorFromContext(ctx)
	if actor.Kind != "api_key" || !auth.Can(ctx, PermApproveSelf) {
		return false
	}
	k, err := s.findKey(ctx, actor.ID)
	if err != nil {
		return false
	}
	return slices.Contains(k.Scopes, PermApproveSelf)
}

// sensitive reports whether this call of t needs approval: statically
// sensitive tools always; tools with a dynamic rule (update_content on a
// live item) when the rule says so.
func (s *server) sensitive(ctx context.Context, t kernel.MCPTool, args json.RawMessage) bool {
	if t.Sensitive {
		return true
	}
	if rule := s.dynamic[t.Name]; rule != nil {
		return rule(ctx, args)
	}
	return false
}

// execute runs the handler, converting a panic into an internal error.
func (s *server) execute(ctx context.Context, t kernel.MCPTool, args json.RawMessage) (data any, err error) {
	defer func() {
		if p := recover(); p != nil {
			s.log.Error("mcp: tool panic", "tool", t.Name, "panic", fmt.Sprint(p))
			data, err = nil, &ToolError{Code: codeInternal, Message: "internal error"}
		}
	}()
	return t.Handler(ctx, args)
}

// decode strictly decodes tool arguments into v.
func decode(args json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(normalizeArgs(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return invalid("invalid arguments: "+err.Error(), nil)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return invalid("arguments must contain one JSON object", nil)
	}
	return nil
}

// bind adapts a typed handler to kernel.MCPTool.Handler.
func bind[In any](fn func(ctx context.Context, in In) (any, error)) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, args json.RawMessage) (any, error) {
		var in In
		if err := decode(args, &in); err != nil {
			return nil, err
		}
		return fn(ctx, in)
	}
}

func logger(d *kernel.Deps) *slog.Logger {
	if d != nil && d.Log != nil {
		return d.Log
	}
	return slog.Default()
}
