package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/settings"
)

// Error codes used in the error envelope.
const (
	CodeBadRequest       = "bad_request"
	CodeInvalidJSON      = "invalid_json"
	CodeEmptyBody        = "empty_body"
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodePayloadTooLarge  = "payload_too_large"
	CodeValidation       = "validation_failed"
	CodeRateLimited      = "rate_limited"
	CodeInternal         = "internal_error"
)

// maxBody caps JSON request bodies read by Decode (1 MiB).
const maxBody = 1 << 20

// FieldErrorer is implemented by validation errors that carry per-field
// messages (field → message or i18n key). ParseValidation and Fail map them
// to 422 validation_failed.
type FieldErrorer interface {
	FieldErrors() map[string]string
}

// DecodeError is returned by Decode for a body that cannot be bound.
type DecodeError struct {
	Status  int    // 400 or 413
	Code    string // invalid_json or payload_too_large
	Message string
	Fields  map[string]string // field → hint, when known
}

func (e *DecodeError) Error() string { return "api: " + e.Message }

// logger is used by Fail and the panic guard; the module sets it.
var logger atomic.Pointer[slog.Logger]

func log() *slog.Logger {
	if l := logger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// WriteError writes the error envelope; empty fields are omitted.
func WriteError(w http.ResponseWriter, status int, code, msg string, fields map[string]string) {
	if len(fields) == 0 {
		httpx.Error(w, status, code, msg, nil)
		return
	}
	httpx.Error(w, status, code, msg, fields)
}

// NotFound writes the 404 envelope.
func NotFound(w http.ResponseWriter) {
	WriteError(w, http.StatusNotFound, CodeNotFound, "resource not found", nil)
}

// Decode reads a single JSON value from the request body into v: at most
// 1 MiB (w lets net/http close the connection on oversize bodies), unknown
// fields rejected. Errors are *DecodeError (400 empty_body / invalid_json
// with field hints, or 413); pass them to Fail or ParseValidation.
func Decode(w http.ResponseWriter, r *http.Request, v any) error {
	if r.Body == nil || r.Body == http.NoBody {
		return emptyBody()
	}
	body := http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return decodeError(err)
		}
		return &DecodeError{Status: http.StatusBadRequest, Code: CodeInvalidJSON, Message: "request body must contain a single JSON value"}
	}
	return nil
}

func emptyBody() *DecodeError {
	return &DecodeError{Status: http.StatusBadRequest, Code: CodeEmptyBody, Message: "request body is empty"}
}

func decodeError(err error) *DecodeError {
	bad := func(msg string, fields map[string]string) *DecodeError {
		return &DecodeError{Status: http.StatusBadRequest, Code: CodeInvalidJSON, Message: msg, Fields: fields}
	}
	var (
		syntax  *json.SyntaxError
		typeErr *json.UnmarshalTypeError
		tooBig  *http.MaxBytesError
	)
	switch {
	case errors.As(err, &tooBig):
		return &DecodeError{Status: http.StatusRequestEntityTooLarge, Code: CodePayloadTooLarge,
			Message: fmt.Sprintf("request body exceeds %d bytes", maxBody)}
	case errors.Is(err, io.EOF):
		return emptyBody()
	case errors.Is(err, io.ErrUnexpectedEOF):
		return bad("request body is truncated JSON", nil)
	case errors.As(err, &syntax):
		return bad(fmt.Sprintf("malformed JSON at offset %d", syntax.Offset), nil)
	case errors.As(err, &typeErr):
		field := typeErr.Field
		if field == "" {
			return bad("request body has the wrong JSON type", nil)
		}
		return bad("a field has the wrong type", map[string]string{field: "expected " + typeErr.Type.String()})
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return bad("unknown field "+field, map[string]string{field: "unknown field"})
	default:
		return bad("invalid JSON body", nil)
	}
}

// ParseValidation classifies err for an HTTP response: a *DecodeError gives
// its status (400/413) and hints; a FieldErrorer, or any error in the chain
// that is a struct (pointer) with a string-to-string map field named
// Fields (content.ValidationError, auth.ValidationError), gives 422 and the
// fields. Other errors give (0, nil).
func ParseValidation(err error) (status int, fields map[string]string) {
	if err == nil {
		return 0, nil
	}
	var de *DecodeError
	if errors.As(err, &de) {
		return de.Status, de.Fields
	}
	var fe FieldErrorer
	if errors.As(err, &fe) {
		return http.StatusUnprocessableEntity, fe.FieldErrors()
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		if f, ok := fieldsOf(e); ok {
			return http.StatusUnprocessableEntity, f
		}
	}
	return 0, nil
}

// fieldsOf extracts a `Fields map[string]string`-shaped field by reflection.
func fieldsOf(err error) (map[string]string, bool) {
	v := reflect.ValueOf(err)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, false
	}
	f := v.FieldByName("Fields")
	if !f.IsValid() || f.Kind() != reflect.Map || f.Type().Key().Kind() != reflect.String || f.Type().Elem().Kind() != reflect.String {
		return nil, false
	}
	out := make(map[string]string, f.Len())
	iter := f.MapRange()
	for iter.Next() {
		out[iter.Key().String()] = iter.Value().String()
	}
	return out, true
}

// notFoundErrors are the not-found sentinels of modules api may import.
var notFoundErrors = []error{content.ErrNotFound, auth.ErrNotFound, jobs.ErrNotFound, settings.ErrNotFound}

// Fail writes the envelope that fits err: decode errors 400/413, validation
// 422 with fields, known not-found sentinels (or errors with a
// NotFound() bool method returning true) 404, anything else 500 with a
// generic message (the error is logged, never sent).
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	if status, fields := ParseValidation(err); status != 0 {
		code, msg := CodeValidation, "validation failed"
		var de *DecodeError
		if errors.As(err, &de) {
			code, msg = de.Code, de.Message
		}
		WriteError(w, status, code, msg, fields)
		return
	}
	if isNotFound(err) {
		NotFound(w)
		return
	}
	log().Error("api: request failed", "method", r.Method, "path", httpx.RedactPath(r.URL.Path), "err", err,
		"request_id", httpx.RequestIDFromContext(r.Context()))
	WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error", nil)
}

func isNotFound(err error) bool {
	for _, s := range notFoundErrors {
		if errors.Is(err, s) {
			return true
		}
	}
	var nf interface{ NotFound() bool }
	return errors.As(err, &nf) && nf.NotFound()
}
