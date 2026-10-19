package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"unicode"

	"github.com/google/jsonschema-go/jsonschema"
)

var errManagedTools = errors.New("managed tools: unavailable or requires review")

type agentManagedTool struct {
	ConnectionID      string          `json:"connection_id"`
	ConnectionVersion string          `json:"connection_version"`
	Name              string          `json:"name"`
	Alias             string          `json:"alias"`
	Description       string          `json:"description"`
	Schema            json.RawMessage `json:"schema"`
	CatalogHash       string          `json:"catalog_hash"`
}
type agentToolPolicy struct {
	ModelVersion string             `json:"model_version"`
	Enabled      bool               `json:"enabled"`
	Allow        []agentManagedTool `json:"allow"`
}

func managedToolAlias(t agentManagedTool) string {
	sum := sha256.Sum256([]byte(t.ConnectionID + "\x00" + t.ConnectionVersion + "\x00" + t.Name + "\x00" + t.CatalogHash))
	return "gorun_" + hex.EncodeToString(sum[:16])
}
func boundedJSON(raw []byte, max, depth, nodes int) (any, error) {
	if len(raw) == 0 || len(raw) > max {
		return nil, errManagedTools
	}
	var data any
	if json.Unmarshal(raw, &data) != nil {
		return nil, errManagedTools
	}
	var walk func(any, int) bool
	walk = func(v any, d int) bool {
		nodes--
		if nodes < 0 || d > depth {
			return false
		}
		switch x := v.(type) {
		case map[string]any:
			for _, y := range x {
				if !walk(y, d+1) {
					return false
				}
			}
		case []any:
			for _, y := range x {
				if !walk(y, d+1) {
					return false
				}
			}
		}
		return true
	}
	if !walk(data, 0) {
		return nil, errManagedTools
	}
	return data, nil
}
func resolveManagedSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	data, e := boundedJSON(raw, 8192, 12, 512)
	if e != nil {
		return nil, e
	}
	root, ok := data.(map[string]any)
	if !ok || root["type"] != "object" {
		return nil, errManagedTools
	}
	var safe func(any) bool
	safe = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for key, value := range x {
				if key == "$ref" || key == "$dynamicRef" || key == "$recursiveRef" || key == "$id" {
					return false
				}
				if !safe(value) {
					return false
				}
			}
		case []any:
			for _, value := range x {
				if !safe(value) {
					return false
				}
			}
		}
		return true
	}
	if !safe(data) {
		return nil, errManagedTools
	}
	var schema jsonschema.Schema
	if json.Unmarshal(raw, &schema) != nil {
		return nil, errManagedTools
	}
	resolved, e := schema.Resolve(&jsonschema.ResolveOptions{Loader: nil})
	if e != nil {
		return nil, errManagedTools
	}
	return resolved, nil
}
func validateManagedArguments(t agentManagedTool, raw json.RawMessage) (map[string]any, error) {
	data, e := boundedJSON(raw, 4096, 12, 512)
	if e != nil {
		return nil, e
	}
	args, ok := data.(map[string]any)
	if !ok {
		return nil, errManagedTools
	}
	schema, e := resolveManagedSchema(t.Schema)
	if e != nil || schema.Validate(args) != nil {
		return nil, errManagedTools
	}
	return args, nil
}
func validateAgentToolPolicy(p agentToolPolicy, refs []agentMCPReference) error {
	if !p.Enabled || !connectionIDPattern.MatchString(p.ModelVersion) {
		return errManagedTools
	}
	if len(p.Allow) == 0 || len(p.Allow) > 8 {
		return errManagedTools
	}
	allowed := map[string]string{}
	for _, r := range refs {
		allowed[r.ID] = r.Version
	}
	seen := map[string]bool{}
	for _, t := range p.Allow {
		if allowed[t.ConnectionID] != t.ConnectionVersion || len(t.Name) == 0 || len(t.Name) > 128 || strings.IndexFunc(t.Name, unicode.IsControl) >= 0 || !canvasText(t.Description, 512) || len(t.CatalogHash) != 64 || t.Alias != managedToolAlias(t) || seen[t.Alias] {
			return errManagedTools
		}
		if _, e := resolveManagedSchema(t.Schema); e != nil {
			return e
		}
		seen[t.Alias] = true
	}
	return nil
}
func canonicalToolCatalog(rows []studioTool) ([]studioTool, string, error) {
	if len(rows) == 0 || len(rows) > 32 {
		return nil, "", errManagedTools
	}
	seen := map[string]bool{}
	for i, t := range rows {
		if t.Name == "" || len(t.Name) > 128 || strings.IndexFunc(t.Name, unicode.IsControl) >= 0 || seen[t.Name] || !canvasText(t.Description, 512) {
			return nil, "", errManagedTools
		}
		seen[t.Name] = true
		raw, e := boundedJSON(t.InputSchema, 8192, 12, 512)
		if e != nil {
			return nil, "", e
		}
		rows[i].InputSchema, _ = json.Marshal(raw)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, studioReviewHash(map[string]any{"tools": rows}), nil
}
func (h *handlers) managedCatalog(ctx context.Context, client *http.Client, c connectionRecord) (*studioSession, []studioTool, string, error) {
	secrets, e := h.connectionSecrets(c)
	if e != nil {
		return nil, nil, "", errManagedTools
	}
	session, e := openStudioMCP(ctx, client, c, secrets)
	if e != nil {
		return nil, nil, "", errManagedTools
	}
	rows, e := session.tools(ctx)
	if e != nil {
		return nil, nil, "", errManagedTools
	}
	rows, hash, e := canonicalToolCatalog(rows)
	if e != nil {
		return nil, nil, "", e
	}
	// Do not forward a catalog which reflects backend credentials/capability URLs.
	raw, _ := json.Marshal(rows)
	for _, secret := range []string{secrets.Token, secrets.AccessKey, secrets.SecretKey, c.Secret, c.Endpoint} {
		if secret != "" && managedContainsSecret(string(raw), []string{secret}) {
			return nil, nil, "", errManagedTools
		}
	}
	return session, rows, hash, nil
}
func managedToolsEnabled(p canvasPrepared) bool {
	return p.ManagedTools != nil && p.ManagedTools.Enabled
}
