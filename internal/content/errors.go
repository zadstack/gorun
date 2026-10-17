package content

import (
	"errors"
	"sort"
	"strings"
)

// Error implements error. ValidationError is always returned as a pointer
// (*ValidationError); match it with errors.As(err, &ve) where ve is
// *ValidationError. Field values are i18n keys (e.g.
// "content.validation.required").
func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e.Fields[k]
	}
	return "content: validation failed: " + strings.Join(parts, ", ")
}

var (
	// ErrForbidden: the acting user may not perform the action (e.g. an
	// author editing someone else's item, or publishing without
	// content.publish).
	ErrForbidden = errors.New("content: forbidden")
	// ErrSystemType: system types (post, page) cannot be deleted.
	ErrSystemType = errors.New("content: system type")
	// ErrTypeInUse: a type with content cannot be deleted.
	ErrTypeInUse = errors.New("content: type has content")
)

func invalid(fields FieldErrors) error {
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}
