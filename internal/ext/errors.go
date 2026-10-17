package ext

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned for an unknown extension slug.
var ErrNotFound = notFoundError{}

type notFoundError struct{}

func (notFoundError) Error() string  { return "ext: extension not found" }
func (notFoundError) NotFound() bool { return true }

// ErrNoOwner is returned by Install when no active admin user can own the
// extension's API key.
var ErrNoOwner = errors.New("ext: no active admin user to own the extension API key")

// ValidationError lists manifest or package problems keyed by field path
// ("hooks.0.kind") with i18n message keys.
type ValidationError struct {
	Fields map[string]string `json:"fields"`
	// Detail is untranslated technical detail (e.g. the JSON decode error of
	// a bad manifest), shown escaped in the admin and logged.
	Detail string `json:"detail,omitempty"`
}

func (e *ValidationError) Error() string { return fmt.Sprintf("ext: invalid extension: %v", e.Fields) }

// FieldErrors implements api.FieldErrorer.
func (e *ValidationError) FieldErrors() map[string]string { return e.Fields }

func invalid(field, key string) error {
	return &ValidationError{Fields: map[string]string{field: key}}
}
