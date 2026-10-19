package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

type managedToolCatalogSnapshot struct {
	Rows         []agentManagedTool `json:"rows"`
	ModelVersion string             `json:"model_version"`
}
type managedToolsPage struct {
	ID, Name, Version, Token, Error string
	Policy                          *agentToolPolicy
	Rows                            []agentManagedTool
}

func (h *handlers) managedToolsEditor(w http.ResponseWriter, r *http.Request) {
	h.managedToolsEditorUsing(w, r, connectionHTTPClient())
}
func (h *handlers) managedToolsEditorUsing(w http.ResponseWriter, r *http.Request, client *http.Client) {
	w.Header().Set("Cache-Control", "no-store")
	key, ok := h.agentBundleAccess(w, r)
	if !ok {
		return
	}
	b, e := currentAgentBundle(r.Context(), h.d.DB, key.UserID, key.ID)
	if e != nil {
		http.Error(w, "Bundle needs review", 409)
		return
	}
	page := managedToolsPage{ID: key.ID, Name: key.Name, Version: b.Version, Policy: b.Tools}
	code := 200
	if r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if r.ParseForm() != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		fail := func() { code = 422; page.Error = "managedtools.unavailable" }
		if r.PostForm.Get("version") != b.Version {
			code = 409
			page.Error = "agentbundles.conflict"
		} else {
			switch r.PostForm.Get("action") {
			case "disable":
				b.Tools = nil
				if _, e = h.saveAgentBundle(r.Context(), key.ID, b.Version, b); e != nil {
					fail()
				} else {
					httpx.SetFlash(w, "success", "managedtools.disabled")
					redirect(w, r, r.URL.Path)
					return
				}
			case "discover":
				model, err := h.loadAgentModel(r.Context(), key.ID)
				if err != nil || model.Mode != "managed" {
					fail()
					break
				}
				conn, err := h.loadConnection(r.Context(), model.ConnectionID)
				if err != nil || conn.Version != model.ConnectionVersion || conn.Kind != "ai" || conn.Provider != AIProviderOpenAI || validateConnection(conn) != nil {
					fail()
					break
				}
				ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
				defer cancel()
				for _, ref := range b.MCP {
					c, err := h.loadConnection(ctx, ref.ID)
					if err != nil || c.Version != ref.Version || c.Kind != "mcp" {
						e = errManagedTools
						break
					}
					_, tools, hash, err := h.managedCatalog(ctx, client, c)
					if err != nil {
						e = err
						break
					}
					for _, t := range tools {
						if _, err = resolveManagedSchema(t.InputSchema); err != nil {
							continue
						}
						row := agentManagedTool{ConnectionID: c.ID, ConnectionVersion: c.Version, Name: t.Name, Description: t.Description, Schema: t.InputSchema, CatalogHash: hash}
						row.Alias = managedToolAlias(row)
						page.Rows = append(page.Rows, row)
					}
					if len(page.Rows) > 64 {
						e = errManagedTools
						break
					}
				}
				providerSecrets, secretErr := h.connectionSecrets(conn)
				if secretErr != nil || managedContainsSecret(canvasEncode(page.Rows), []string{providerSecrets.Token, providerSecrets.AccessKey, providerSecrets.SecretKey, conn.Secret, conn.Endpoint}) {
					e = errManagedTools
				}
				if e != nil || len(page.Rows) == 0 {
					page.Rows = nil
					fail()
					break
				}
				current, err := currentAgentBundle(r.Context(), h.d.DB, key.UserID, key.ID)
				if err != nil || current.Version != b.Version {
					page.Rows = nil
					fail()
					break
				}
				page.Token = db.NewID()
				expires := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
				e = db.InTx(r.Context(), h.d.DB, func(tx *sql.Tx) error {
					if !agentBundleOwnerCurrent(r.Context(), tx, key.UserID, key.ID) {
						return errManagedTools
					}
					if _, err := tx.ExecContext(r.Context(), `DELETE FROM admin_agent_tool_catalogs WHERE expires_at<?`, db.Now()); err != nil {
						return err
					}
					var n int
					if err := tx.QueryRowContext(r.Context(), `SELECT count(*) FROM admin_agent_tool_catalogs WHERE owner_id=?`, key.UserID).Scan(&n); err != nil || n >= 10 {
						return errManagedTools
					}
					_, err := tx.ExecContext(r.Context(), `INSERT INTO admin_agent_tool_catalogs(id,owner_id,key_id,bundle_version,document,expires_at) VALUES(?,?,?,?,?,?)`, page.Token, key.UserID, key.ID, b.Version, canvasEncode(managedToolCatalogSnapshot{Rows: page.Rows, ModelVersion: model.Version}), expires)
					return err
				})
				if e != nil {
					page.Rows = nil
					page.Token = ""
					fail()
				}
			case "save":
				var raw string
				if r.PostForm.Get("reviewed") != "1" || h.d.DB.QueryRowContext(r.Context(), `SELECT document FROM admin_agent_tool_catalogs WHERE id=? AND owner_id=? AND key_id=? AND bundle_version=? AND expires_at>?`, r.PostForm.Get("catalog"), key.UserID, key.ID, b.Version, db.Now()).Scan(&raw) != nil {
					fail()
					break
				}
				var snapshot managedToolCatalogSnapshot
				if json.Unmarshal([]byte(raw), &snapshot) != nil {
					fail()
					break
				}
				model, err := h.loadAgentModel(r.Context(), key.ID)
				if err != nil || model.Mode != "managed" || model.Version != snapshot.ModelVersion {
					fail()
					break
				}
				lookup := map[string]agentManagedTool{}
				for _, t := range snapshot.Rows {
					lookup[t.Alias] = t
				}
				policy := agentToolPolicy{Enabled: true, ModelVersion: snapshot.ModelVersion}
				for _, alias := range r.PostForm["allow"] {
					t, found := lookup[alias]
					if !found {
						e = errManagedTools
						break
					}
					policy.Allow = append(policy.Allow, t)
				}
				if e != nil || validateAgentToolPolicy(policy, b.MCP) != nil {
					fail()
					break
				}
				b.Tools = &policy
				if _, e = h.saveAgentBundle(r.Context(), key.ID, b.Version, b); e != nil {
					fail()
				} else {
					httpx.SetFlash(w, "success", "managedtools.saved")
					redirect(w, r, r.URL.Path)
					return
				}
			default:
				fail()
			}
		}
	}
	if !agentBundleOwnerCurrent(r.Context(), h.d.DB, key.UserID, key.ID) {
		http.NotFound(w, r)
		return
	}
	h.tpl.RenderWith(w, r, code, "admin", "pages/agents/tools.html", page)
}
