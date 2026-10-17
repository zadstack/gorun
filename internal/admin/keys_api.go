package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

// KeyDTO is an API key as the key console API returns it (never the secret).
type KeyDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Prefix      string   `json:"prefix" doc:"first characters after gp_, shown to identify the key"`
	Kind        string   `json:"kind" doc:"api | agent"`
	UserID      string   `json:"user_id"`
	Scopes      []string `json:"scopes"`
	RatePerMin  int      `json:"rate_per_min"`
	ExpiresAt   string   `json:"expires_at"`
	LastUsedAt  string   `json:"last_used_at"`
	UseCount    int64    `json:"use_count" doc:"approximate; flushed at most once a minute"`
	CreatedAt   string   `json:"created_at"`
	RevokedAt   string   `json:"revoked_at"`
	RotatedFrom string   `json:"rotated_from"`
	Status      string   `json:"status" doc:"active | expired | revoked"`
}

// KeyCreateRequest is the POST /v1/keys body.
type KeyCreateRequest struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind" doc:"api (default) | agent"`
	Scopes     []string `json:"scopes" doc:"permission names, or [\"*\"]; never more than the caller holds (session callers only)"`
	RatePerMin int      `json:"rate_per_min" doc:"default 60"`
	ExpiresAt  string   `json:"expires_at" doc:"RFC3339, empty for never"`
}

// KeyRotateRequest is the POST /v1/keys/{id}/rotate body.
type KeyRotateRequest struct {
	GraceSeconds int `json:"grace_seconds" doc:"0 revokes the old key now; up to 604800 keeps it working that long"`
}

// KeySecretResponse is returned once by create and rotate: token is the
// plaintext key and is never shown again.
type KeySecretResponse struct {
	Key   KeyDTO `json:"key"`
	Token string `json:"token"`
}

func keyStatus(k auth.APIKey, now time.Time) string {
	switch {
	case k.RevokedAt != "":
		return "revoked"
	case k.ExpiresAt != "":
		if t, err := time.Parse(time.RFC3339, k.ExpiresAt); err == nil && !now.Before(t) {
			return "expired"
		}
	}
	return "active"
}

func keyDTO(k auth.APIKey, now time.Time) KeyDTO {
	scopes := k.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return KeyDTO{ID: k.ID, Name: k.Name, Prefix: k.Prefix, Kind: k.Kind, UserID: k.UserID, Scopes: scopes, RatePerMin: k.RatePerMin,
		ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt, UseCount: k.UseCount, CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt,
		RotatedFrom: k.RotatedFrom, Status: keyStatus(k, now)}
}

func (h *handlers) keyAPIOperations() {
	tags, scopes := []string{"keys"}, []string{PermAPIKeys}
	idParam := []kernel.APIParam{{Name: "id", In: "path", Type: "string", Required: true}}
	for _, op := range []kernel.APIOperation{
		{Method: "GET", Path: "/v1/keys", Summary: "List API and agent keys (never their secrets)", Tags: tags, Scopes: scopes,
			Params: []kernel.APIParam{{Name: "status", In: "query", Type: "string", Description: "active | expired | revoked"},
				{Name: "kind", In: "query", Type: "string", Description: "api | agent"}},
			Response: []KeyDTO{}, Handler: h.apiKeysList},
		{Method: "POST", Path: "/v1/keys", Summary: "Create a key; the token is returned once", Tags: tags, Scopes: scopes,
			Request: KeyCreateRequest{}, Response: KeySecretResponse{}, Handler: h.apiKeyCreate},
		{Method: "POST", Path: "/v1/keys/{id}/rotate", Summary: "Rotate one of your keys; the new token is returned once", Tags: tags, Scopes: scopes,
			Params: idParam, Request: KeyRotateRequest{}, Response: KeySecretResponse{}, Handler: h.apiKeyRotate},
		{Method: "DELETE", Path: "/v1/keys/{id}", Summary: "Revoke a key", Tags: tags, Scopes: scopes,
			Params: idParam, Response: opResult{}, Handler: h.apiKeyRevoke},
	} {
		h.d.API.Add(op)
	}
}

func (h *handlers) apiKeysList(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermAPIKeys) {
		return
	}
	list, err := h.keys.List(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	now := time.Now()
	status, kind := r.URL.Query().Get("status"), r.URL.Query().Get("kind")
	out := []KeyDTO{}
	for _, k := range list {
		d := keyDTO(k, now)
		if (status == "" || d.Status == status) && (kind == "" || d.Kind == kind) {
			out = append(out, d)
		}
	}
	httpx.JSON(w, http.StatusOK, out, nil)
}

// apiKeyCreate is session-only: an API key cannot mint keys, so a leaked key
// can never spawn children that outlive its own revocation or limits.
func (h *handlers) apiKeyCreate(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermAPIKeys) {
		return
	}
	if auth.ActorFromContext(r.Context()).Kind == "api_key" {
		httpx.Error(w, http.StatusForbidden, "session_required", "keys can only be created from a signed-in session, not with an API key", nil)
		return
	}
	var in KeyCreateRequest
	if err := decodeJSON(r, &in); err != nil {
		h.badRequest(w, r, err)
		return
	}
	if in.Kind == "" {
		in.Kind = auth.KeyKindAPI
	}
	if in.RatePerMin < 0 || in.RatePerMin > maxKeyRate {
		h.validationFailed(w, r, map[string]string{"rate_per_min": "admin.keys.error_rate"})
		return
	}
	if !auth.CanGrant(r.Context(), in.Scopes) {
		httpx.Error(w, http.StatusForbidden, "scope_exceeds_caller", "a key cannot grant scopes its creator does not hold", nil)
		return
	}
	u := auth.UserFromContext(r.Context())
	plain, k, err := h.keys.CreateKind(r.Context(), in.Kind, strings.TrimSpace(in.Name), u.ID, in.Scopes, in.RatePerMin, in.ExpiresAt)
	if h.keyWriteErr(w, r, err) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusCreated, KeySecretResponse{Key: keyDTO(*k, time.Now()), Token: plain}, nil)
}

func (h *handlers) apiKeyRotate(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermAPIKeys) {
		return
	}
	var in KeyRotateRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			h.badRequest(w, r, err)
			return
		}
	}
	old, err := h.keys.Get(r.Context(), urlID(r))
	if h.keyWriteErr(w, r, err) {
		return
	}
	if u := auth.UserFromContext(r.Context()); u == nil || old.UserID != u.ID || !auth.CanGrant(r.Context(), old.Scopes) {
		httpx.Error(w, http.StatusForbidden, "not_key_owner", "only the key's owner can rotate it, within their own scopes", nil)
		return
	}
	plain, k, err := h.keys.Rotate(r.Context(), old.ID, time.Duration(in.GraceSeconds)*time.Second)
	if h.keyWriteErr(w, r, err) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusCreated, KeySecretResponse{Key: keyDTO(*k, time.Now()), Token: plain}, nil)
}

func (h *handlers) apiKeyRevoke(w http.ResponseWriter, r *http.Request) {
	if h.apiDeny(w, r, PermAPIKeys) {
		return
	}
	if h.keyWriteErr(w, r, h.keys.Revoke(r.Context(), urlID(r))) {
		return
	}
	httpx.JSON(w, http.StatusOK, opResult{ID: urlID(r), Status: "revoked"}, nil)
}

// keyWriteErr writes the API error for err and reports whether it did.
func (h *handlers) keyWriteErr(w http.ResponseWriter, r *http.Request, err error) bool {
	var ve *auth.ValidationError
	switch {
	case err == nil:
		return false
	case errors.As(err, &ve):
		h.validationFailed(w, r, ve.Fields)
	case errors.Is(err, auth.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", h.tr(r, "core.error.not_found"), nil)
	case errors.Is(err, auth.ErrKeyRevoked), errors.Is(err, auth.ErrKeyExpired):
		httpx.Error(w, http.StatusConflict, "key_inactive", "the key is revoked or expired", nil)
	default:
		h.serverError(w, r, err)
	}
	return true
}
