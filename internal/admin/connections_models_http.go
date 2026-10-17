package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/zadstack/gorun/internal/auth"
)

func (h *handlers) connectionsModels(w http.ResponseWriter, r *http.Request) {
	h.connectionsModelsWithClient(w, r, connectionHTTPClient())
}
func (h *handlers) connectionsModelsWithClient(w http.ResponseWriter, r *http.Request, client *http.Client) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json")
	fail := func(e error) {
		code, status := connectionModelFailure(e)
		if status >= 500 {
			status = http.StatusUnprocessableEntity
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": code, "message": h.tr(r, "connectionmodels."+code)})
	}
	if !auth.Can(r.Context(), "settings.manage") {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		return
	}
	if r.ParseForm() != nil {
		fail(modelError("invalid", 422))
		return
	}
	id, version := r.PostForm.Get("id"), r.PostForm.Get("version")
	c := connectionRecord{ID: id, Version: version, Kind: r.PostForm.Get("kind"), Provider: r.PostForm.Get("provider")}
	var e error
	c.Endpoint, e = normalizeConnectionEndpoint(r.PostForm.Get("endpoint"))
	token := r.PostForm.Get("token")
	if e != nil || c.Kind != "ai" || (c.Provider != "openai" && c.Provider != "anthropic") || !validConnectionSecret(token) {
		fail(modelError("invalid", 422))
		return
	}
	var previous connectionRecord
	s := connectionSecrets{Token: token}
	if id != "" {
		previous, e = h.loadConnection(r.Context(), id)
		if e != nil {
			fail(modelError("missing", 404))
			return
		}
		if version == "" || version != previous.Version {
			fail(errConnectionConflict)
			return
		}
		if previous.Kind != "ai" {
			fail(modelError("invalid", 422))
			return
		}
		if token == "" && r.PostForm.Get("clear_secret") == "" {
			if previous.Endpoint != c.Endpoint || previous.Provider != c.Provider {
				fail(modelError("destination_changed", 422))
				return
			}
			s, e = h.connectionSecrets(previous)
			if e != nil {
				fail(modelError("credential_unavailable", 422))
				return
			}
		}
	} else if version != "" {
		fail(errConnectionConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	catalog, e := discoverAIModelCatalog(ctx, client, c, s)
	if e != nil {
		fail(e)
		return
	}
	// A provider response must not attach to an edited or removed saved profile.
	if id != "" {
		current, err := h.loadConnection(r.Context(), id)
		if err != nil || current.Version != version {
			fail(errConnectionConflict)
			return
		}
	}
	json.NewEncoder(w).Encode(struct {
		Models        []connectionModel `json:"models"`
		ExcludedCount int               `json:"excluded_count"`
		ProfileID     string            `json:"profile_id"`
		Version       string            `json:"version"`
	}{catalog.Models, catalog.ExcludedCount, id, version})
}
