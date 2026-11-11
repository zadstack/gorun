package mcp

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
)

// Request parameters and headers that narrow the tool list of one session.
const (
	paramToolsets  = "toolsets"
	paramReadOnly  = "readonly"
	headerToolsets = "X-MCP-Toolsets"
	headerReadOnly = "X-MCP-Readonly"
)

// alwaysVisible tools stay listed under any toolset or read-only filter
// (scope filtering still applies).
var alwaysVisible = map[string]bool{"check_approval": true, "get_site_structure": true}

// toolView is a client-chosen narrowing of the tool list: some toolsets
// only (nil = all) and/or read-only tools only. Hidden tools are refused
// on call like tools outside the key's scopes.
type toolView struct {
	toolsets map[string]bool
	readOnly bool
}

// shows reports whether t is part of the view.
func (v toolView) shows(t kernel.MCPTool) bool {
	if alwaysVisible[t.Name] {
		return true
	}
	if v.readOnly && !t.ReadOnly {
		return false
	}
	return v.toolsets == nil || v.toolsets[t.ToolsetName()]
}

// newView validates toolset names (comma separated; empty = all) and builds
// the view.
func newView(toolsets []string, readOnly bool) (toolView, error) {
	v := toolView{readOnly: readOnly}
	for _, name := range toolsets {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if !slices.Contains(kernel.MCPToolsets, name) {
			return toolView{}, fmt.Errorf("unknown toolset %q (known: %s)", name, strings.Join(kernel.MCPToolsets, ", "))
		}
		if v.toolsets == nil {
			v.toolsets = map[string]bool{}
		}
		v.toolsets[name] = true
	}
	return v, nil
}

// parseBool reads a readonly flag value: 1/true/yes, 0/false/no or empty.
func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "0", "false", "no":
		return false, nil
	case "1", "true", "yes":
		return true, nil
	}
	return false, fmt.Errorf("invalid readonly value %q (use 1 or 0)", s)
}

// viewFromRequest reads ?toolsets= / X-MCP-Toolsets and ?readonly= /
// X-MCP-Readonly (the query parameter wins when both are given).
func viewFromRequest(r *http.Request) (toolView, error) {
	q := r.URL.Query()
	sets := r.Header.Get(headerToolsets)
	if q.Has(paramToolsets) {
		sets = q.Get(paramToolsets)
	}
	ro := r.Header.Get(headerReadOnly)
	if q.Has(paramReadOnly) {
		ro = q.Get(paramReadOnly)
	}
	readOnly, err := parseBool(ro)
	if err != nil {
		return toolView{}, err
	}
	return newView(strings.Split(sets, ","), readOnly)
}

type viewKey struct{}

// withView marks ctx with the session's view (checked in dispatch).
func withView(ctx context.Context, v toolView) context.Context {
	return context.WithValue(ctx, viewKey{}, v)
}

// hiddenByView reports whether the view in ctx (if any) hides t.
func hiddenByView(ctx context.Context, t kernel.MCPTool) bool {
	v, ok := ctx.Value(viewKey{}).(toolView)
	return ok && !v.shows(t)
}

func errHidden() *ToolError {
	return &ToolError{Code: codeForbidden, Message: "forbidden: this tool is hidden by the session's toolsets/readonly filter"}
}
