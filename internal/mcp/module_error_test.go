package mcp

import (
	"fmt"
	"testing"
)

type moduleError struct{ code string }

func (e moduleError) Error() string     { return "safe module message" }
func (e moduleError) ErrorCode() string { return e.code }
func TestModuleErrorsPreserveSafeCodesWithoutDependingOnMCP(t *testing.T) {
	for _, code := range []string{"forbidden", "not_found", "conflict", "validation_failed"} {
		got, known := toToolError(fmt.Errorf("wrapped: %w", moduleError{code: code}))
		if !known || got.Code != code || got.Message != "safe module message" {
			t.Fatalf("module code lost %+v %v", got, known)
		}
	}
	got, known := toToolError(moduleError{code: "unexpected"})
	if known || got.Code != "internal" {
		t.Fatalf("unknown module code accepted %+v %v", got, known)
	}
}

type detailedModuleError struct{ moduleError }

func (e detailedModuleError) FieldErrors() map[string]string {
	return map[string]string{"document": "internal database diagnostic"}
}
func TestUnknownCodedModuleErrorDoesNotBecomeValidation(t *testing.T) {
	got, known := toToolError(detailedModuleError{moduleError{"internal"}})
	if known || got.Code != codeInternal || got.Fields != nil {
		t.Fatalf("internal error leaked %+v known=%v", got, known)
	}
}
