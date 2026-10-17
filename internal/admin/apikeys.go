package admin

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
)

const maxKeyRate = 10000

// keyInput is the API key create form.
type keyInput struct {
	Kind    string   `form:"kind"` // auth.KeyKindAgent (default) or auth.KeyKindAPI
	Name    string   `form:"name"`
	Scopes  []string `form:"scopes"`
	All     bool     `form:"all"`
	Rate    int      `form:"rate"`
	Expires string   `form:"expires"` // YYYY-MM-DD, valid through that day (UTC)
}

type scopeOption struct {
	Name, Label string
	Checked     bool
}

// scopeGroup groups permissions by module prefix ("content.*").
type scopeGroup struct {
	Module string
	Perms  []scopeOption
}

type keyView struct {
	auth.APIKey
	Owner            string
	Active           bool
	Expired          bool
	CanModel         bool
	CanRotate        bool // active and owned by the viewer
	Model, ModelMode string
}

type keysPage struct {
	Keys       []keyView
	Groups     []scopeGroup
	Form       keyInput
	Fields     map[string]string
	Filter     string // "", "active" or "inactive"
	Plaintext  string // shown once, only in the create and rotate responses
	Created    *auth.APIKey
	AgentModel *agentModelPage
}

func (h *handlers) scopeGroups(r *http.Request, checked []string) []scopeGroup {
	locale := i18n.Locale(r.Context())
	var out []scopeGroup
	for _, p := range h.d.Perms.All() {
		mod, _, _ := strings.Cut(p.Name, ".")
		label := p.Label[locale]
		if label == "" {
			label = p.Label["en"]
		}
		opt := scopeOption{Name: p.Name, Label: label, Checked: slices.Contains(checked, p.Name)}
		if n := len(out); n > 0 && out[n-1].Module == mod {
			out[n-1].Perms = append(out[n-1].Perms, opt)
			continue
		}
		out = append(out, scopeGroup{Module: mod, Perms: []scopeOption{opt}})
	}
	return out
}

func (h *handlers) keyViews(r *http.Request) ([]keyView, error) {
	list, err := h.keys.List(r.Context())
	if err != nil {
		return nil, err
	}
	owners := map[string]string{}
	now := time.Now()
	out := make([]keyView, len(list))
	for i, k := range list {
		if _, seen := owners[k.UserID]; !seen {
			owners[k.UserID] = k.UserID
			if u, err := h.users.Get(r.Context(), k.UserID); err == nil {
				owners[k.UserID] = u.Email
			}
		}
		expired := false
		if k.ExpiresAt != "" {
			if t, err := db.ParseTime(k.ExpiresAt); err == nil && !now.Before(t) {
				expired = true
			}
		}
		out[i] = keyView{APIKey: k, Owner: owners[k.UserID], Expired: expired, Active: k.RevokedAt == "" && !expired}
		u := auth.UserFromContext(r.Context())
		out[i].CanRotate = out[i].Active && u != nil && k.UserID == u.ID
		out[i].CanModel = out[i].Active && k.Kind == auth.KeyKindAgent && u != nil && k.UserID == u.ID && auth.Can(r.Context(), "settings.manage")
		if out[i].CanModel {
			cfg, err := h.loadAgentModel(r.Context(), k.ID)
			if err != nil {
				return nil, err
			}
			out[i].Model, out[i].ModelMode = cfg.Model, cfg.Mode
		}
	}
	return out, nil
}

func (h *handlers) renderKeys(w http.ResponseWriter, r *http.Request, status int, data keysPage) {
	keys, err := h.keyViews(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	switch f := r.URL.Query().Get("status"); f {
	case "active", "inactive":
		kept := keys[:0]
		for _, k := range keys {
			if k.Active == (f == "active") {
				kept = append(kept, k)
			}
		}
		keys = kept
		data.Filter = f
	}
	data.Keys = keys
	if data.AgentModel == nil && r.URL.Query().Get("agent_model") != "" {
		data.AgentModel, err = h.agentModelPage(r.Context(), r.URL.Query().Get("agent_model"))
		if err != nil {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
	}
	data.Groups = h.scopeGroups(r, data.Form.Scopes)
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.RenderWith(w, r, status, "admin", "pages/agents/keys.html", data)
}

func (h *handlers) keysList(w http.ResponseWriter, r *http.Request) {
	h.renderKeys(w, r, http.StatusOK, keysPage{Form: newKeyForm()})
}

// newKeyForm is the empty create form: this screen lives under AI Agents,
// so new keys are agent keys (MCP only) unless the admin picks API.
func newKeyForm() keyInput { return keyInput{Kind: auth.KeyKindAgent, Rate: 60} }

// keyExpiry converts a YYYY-MM-DD date to the end of that day (UTC).
func keyExpiry(date string) (string, bool) {
	if date == "" {
		return "", true
	}
	t, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return "", false
	}
	end := t.Add(24*time.Hour - time.Second)
	if !end.After(time.Now()) {
		return "", false
	}
	return end.UTC().Format(time.RFC3339), true
}

// keyCreate creates a key and renders the list with the plaintext shown
// once. The plaintext is never stored, logged, flashed or redirected.
func (h *handlers) keyCreate(w http.ResponseWriter, r *http.Request) {
	var in keyInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	fields := map[string]string{}
	scopes := in.Scopes
	if in.All {
		scopes = []string{"*"}
	}
	expires, ok := keyExpiry(strings.TrimSpace(in.Expires))
	if !ok {
		fields["expires"] = "admin.keys.error_expires"
	}
	if in.Rate < 0 || in.Rate > maxKeyRate {
		fields["rate"] = "admin.keys.error_rate"
	}
	if !auth.CanGrant(r.Context(), scopes) {
		fields["scopes"] = "admin.keys.error_grant"
	}
	if len(fields) > 0 {
		h.renderKeys(w, r, http.StatusUnprocessableEntity, keysPage{Form: in, Fields: fields})
		return
	}
	u := auth.UserFromContext(r.Context())
	if in.Kind == "" {
		in.Kind = auth.KeyKindAgent
	}
	plain, k, err := h.keys.CreateKind(r.Context(), in.Kind, in.Name, u.ID, scopes, in.Rate, expires)
	if err != nil {
		var ve *auth.ValidationError
		if errors.As(err, &ve) {
			h.renderKeys(w, r, http.StatusUnprocessableEntity, keysPage{Form: in, Fields: ve.Fields})
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.renderKeys(w, r, http.StatusCreated, keysPage{Form: newKeyForm(), Plaintext: plain, Created: k})
}

func (h *handlers) keyRevoke(w http.ResponseWriter, r *http.Request) {
	err := h.keys.Revoke(r.Context(), urlID(r))
	switch {
	case errors.Is(err, auth.ErrNotFound):
		httpx.SetFlash(w, "error", "admin.keys.revoke_missing")
	case err != nil:
		h.serverError(w, r, err)
		return
	default:
		httpx.SetFlash(w, "success", "admin.keys.revoked")
	}
	redirect(w, r, "/admin/agents/keys")
}

// rotateGraces are the grace periods offered by the console (seconds).
var rotateGraces = map[string]time.Duration{"0": 0, "3600": time.Hour, "86400": 24 * time.Hour}

// keyRotate replaces one of the viewer's own keys and shows the new
// plaintext once; the old key is revoked or kept for the chosen grace.
func (h *handlers) keyRotate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	grace, ok := rotateGraces[r.PostFormValue("grace")]
	old, err := h.keys.Get(r.Context(), urlID(r))
	u := auth.UserFromContext(r.Context())
	if !ok || err != nil || u == nil || old.UserID != u.ID || !auth.CanGrant(r.Context(), old.Scopes) {
		httpx.SetFlash(w, "error", "admin.keys.rotate_denied")
		redirect(w, r, "/admin/agents/keys")
		return
	}
	plain, k, err := h.keys.Rotate(r.Context(), old.ID, grace)
	if err != nil {
		httpx.SetFlash(w, "error", "admin.keys.rotate_denied")
		redirect(w, r, "/admin/agents/keys")
		return
	}
	h.renderKeys(w, r, http.StatusCreated, keysPage{Form: newKeyForm(), Plaintext: plain, Created: k})
}
