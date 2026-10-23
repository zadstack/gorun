package admin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

const settingEdgeInstances = "edge.instances"
const maxEdgeInstances = 100

// edgeInstance is the public view. Credentials have a separate storage type.
type edgeInstance struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Origin    string `json:"origin"`
	AccountID string `json:"account_id,omitempty"`
	Worker    string `json:"worker,omitempty"`
	SiteID    string `json:"site_id"`
	ManagerID string `json:"manager_id"`
	State     string `json:"state"`
	CheckedAt string `json:"checked_at,omitempty"`
	Error     string `json:"error,omitempty"`
	KeySet    bool   `json:"key_set"`
}

type storedEdgeInstance struct {
	Instance edgeInstance `json:"instance"`
	Cipher   string       `json:"manager_key_cipher"`
}

type edgeInstancesSnapshot struct {
	Records []storedEdgeInstance
	raw     string
	present bool
}

func (s edgeInstancesSnapshot) revision() string {
	sum := sha256.Sum256([]byte(settingEdgeInstances + "\x00" + s.raw))
	return hex.EncodeToString(sum[:])
}
func (s edgeInstancesSnapshot) public() []edgeInstance {
	out := make([]edgeInstance, 0, len(s.Records))
	for _, v := range s.Records {
		i := v.Instance
		i.KeySet = v.Cipher != ""
		out = append(out, i)
	}
	return out
}
func edgeInstanceAAD(id string) string { return settingEdgeInstances + "." + id + ".manager_key" }

func (h *handlers) edgeInstances(ctx context.Context) (edgeInstancesSnapshot, error) {
	var raw string
	err := h.d.DB.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", settingEdgeInstances).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return edgeInstancesSnapshot{Records: []storedEdgeInstance{}}, nil
	}
	if err != nil {
		return edgeInstancesSnapshot{}, err
	}
	return parseEdgeInstances(raw)
}

func parseEdgeInstances(raw string) (edgeInstancesSnapshot, error) {
	s := edgeInstancesSnapshot{raw: raw, present: true}
	if err := json.Unmarshal([]byte(raw), &s.Records); err != nil || s.Records == nil || len(s.Records) > maxEdgeInstances {
		return edgeInstancesSnapshot{}, errEdgeSettings
	}
	seen := map[string]bool{}
	for _, v := range s.Records {
		if !edgeIDPattern.MatchString(v.Instance.ID) || seen[v.Instance.ID] || v.Cipher == "" {
			return edgeInstancesSnapshot{}, errEdgeSettings
		}
		seen[v.Instance.ID] = true
		origin, err := validateEdgeOrigin(v.Instance.Origin)
		if err != nil || origin != v.Instance.Origin {
			return edgeInstancesSnapshot{}, errEdgeSettings
		}
	}
	return s, nil
}
func (h *handlers) saveEdgeInstances(ctx context.Context, s edgeInstancesSnapshot, records []storedEdgeInstance, guards ...cfSnapshot) error {
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		for _, guard := range guards {
			for _, key := range []string{settingCFToken, settingCFAccount} {
				var current string
				e := tx.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", key).Scan(&current)
				expected, exists := guard.raw[key]
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return e
				}
				if (e == nil) != exists || current != expected {
					return errEdgeConflict
				}
			}
		}
		var raw string
		err := tx.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", settingEdgeInstances).Scan(&raw)
		present := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if present != s.present || raw != s.raw {
			return errEdgeConflict
		}
		return h.d.Settings.SetTx(ctx, tx, settingEdgeInstances, records)
	})
}

type edgeInstanceInput struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Origin    string `json:"origin"`
	Key       string `json:"key"`
	AccountID string `json:"account_id"`
	Worker    string `json:"worker"`
	Revision  string `json:"revision"`
}

func edgeInstanceLabel(label string) bool {
	return label != "" && utf8.RuneCountInString(label) <= 80 && !strings.ContainsAny(label, "\r\n\x00<>")
}

// Remote verification precedes the short writer transaction. No failed check
// creates a binding or changes the old credential, identity or registry.
func (h *handlers) connectEdgeInstance(ctx context.Context, client *http.Client, in edgeInstanceInput) (edgeInstance, string, error) {
	s, err := h.edgeInstances(ctx)
	if err != nil {
		return edgeInstance{}, "", err
	}
	if in.Revision == "" || in.Revision != s.revision() {
		return edgeInstance{}, "", errEdgeConflict
	}
	origin, err := validateEdgeOrigin(in.Origin)
	if err != nil {
		return edgeInstance{}, "", err
	}
	in.Label = strings.TrimSpace(in.Label)
	in.Key = strings.TrimSpace(in.Key)
	if !edgeInstanceLabel(in.Label) || !edgeKeyPattern.MatchString(in.Key) {
		return edgeInstance{}, "", errEdgeSettings
	}
	index := -1
	for i, v := range s.Records {
		if v.Instance.ID == in.ID {
			index = i
		}
	}
	if in.ID != "" && index < 0 {
		return edgeInstance{}, "", errEdgeSettings
	}
	if index < 0 && len(s.Records) >= maxEdgeInstances {
		return edgeInstance{}, "", errEdgeSettings
	}
	// An inventory name does not prove that this origin serves that Worker.
	// External connections are identity-only; only provisioning receipts bind resources.
	if in.AccountID != "" || in.Worker != "" {
		return edgeInstance{}, "", errEdgeSettings
	}
	identity, err := edgeWhoami(ctx, client, origin, in.Key)
	if err != nil {
		return edgeInstance{}, "", errEdgeCheck
	}
	if index >= 0 && (s.Records[index].Instance.SiteID != identity.SiteID || s.Records[index].Instance.ManagerID != identity.ManagerID) {
		return edgeInstance{}, "", errEdgeConflict
	}
	for i, v := range s.Records {
		if i != index && (v.Instance.Origin == origin || v.Instance.SiteID == identity.SiteID) {
			return edgeInstance{}, "", errEdgeConflict
		}
	}
	id := in.ID
	if index < 0 {
		id = db.NewID()
	}
	cipher, err := db.Encrypt(h.d.Config.SecretKey, []byte(in.Key), edgeInstanceAAD(id))
	if err != nil {
		return edgeInstance{}, "", errEdgeSettings
	}
	result := edgeInstance{ID: id, Label: in.Label, Origin: origin, AccountID: in.AccountID, Worker: in.Worker, SiteID: identity.SiteID, ManagerID: identity.ManagerID, State: "connected", CheckedAt: time.Now().UTC().Format(time.RFC3339), KeySet: true}
	if index >= 0 && s.Records[index].Instance.Origin == origin {
		result.AccountID = s.Records[index].Instance.AccountID
		result.Worker = s.Records[index].Instance.Worker
	}
	record := storedEdgeInstance{Instance: result, Cipher: cipher}
	if index < 0 {
		s.Records = append(s.Records, record)
	} else {
		s.Records[index] = record
	}
	if err := h.saveEdgeInstances(ctx, s, s.Records); err != nil {
		return edgeInstance{}, "", err
	}
	current, err := h.edgeInstances(ctx)
	if err != nil {
		return edgeInstance{}, "", err
	}
	h.d.Audit.Record(ctx, "edge.instance.connect", "edge_instance", id, map[string]any{"origin": origin, "site_id": identity.SiteID})
	return result, current.revision(), nil
}

func (h *handlers) checkEdgeInstance(ctx context.Context, client *http.Client, id, revision string) (edgeInstance, string, error) {
	s, err := h.edgeInstances(ctx)
	if err != nil {
		return edgeInstance{}, "", err
	}
	if revision == "" || revision != s.revision() {
		return edgeInstance{}, "", errEdgeConflict
	}
	index := -1
	for i, v := range s.Records {
		if v.Instance.ID == id {
			index = i
		}
	}
	if index < 0 {
		return edgeInstance{}, "", errEdgeSettings
	}
	record := s.Records[index]
	key, err := db.Decrypt(h.d.Config.SecretKey, record.Cipher, edgeInstanceAAD(id))
	if err != nil {
		return edgeInstance{}, "", errEdgeSettings
	}
	info, checkErr := edgeWhoami(ctx, client, record.Instance.Origin, string(key))
	if checkErr == nil && (info.SiteID != record.Instance.SiteID || info.ManagerID != record.Instance.ManagerID) {
		checkErr = errEdgeCheck
	}
	record.Instance.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.Instance.State = "connected"
	record.Instance.Error = ""
	if checkErr != nil {
		record.Instance.State = "error"
		record.Instance.Error = "manager_identity_check_failed"
	}
	s.Records[index] = record
	if err := h.saveEdgeInstances(ctx, s, s.Records); err != nil {
		return edgeInstance{}, "", err
	}
	current, err := h.edgeInstances(ctx)
	if err != nil {
		return edgeInstance{}, "", err
	}
	h.d.Audit.Record(ctx, "edge.instance.check", "edge_instance", id, map[string]any{"ok": checkErr == nil})
	return record.Instance, current.revision(), nil
}

func (h *handlers) edgeInstancesList(w http.ResponseWriter, r *http.Request) {
	if !auth.Can(r.Context(), "settings.manage") {
		edgeInstanceAPIError(w, 403, "forbidden")
		return
	}
	s, err := h.edgeInstances(r.Context())
	if err != nil {
		h.edgeInstancesError(w, r, err)
		return
	}
	h.aiJSON(w, 200, map[string]any{"ok": true, "data": s.public(), "revision": s.revision()})
}
func (h *handlers) edgeInstancesConnect(w http.ResponseWriter, r *http.Request) {
	h.edgeInstancesConnectWithClient(w, r, edgeHTTPClient)
}
func (h *handlers) edgeInstancesConnectWithClient(w http.ResponseWriter, r *http.Request, client *http.Client) {
	if !auth.Can(r.Context(), "settings.manage") {
		edgeInstanceAPIError(w, 403, "forbidden")
		return
	}
	var in edgeInstanceInput
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.Decode(new(any)) != io.EOF {
		edgeInstanceAPIError(w, 400, "invalid_request")
		return
	}
	item, revision, err := h.connectEdgeInstance(r.Context(), client, in)
	if err != nil {
		h.edgeInstancesError(w, r, err)
		return
	}
	h.aiJSON(w, 200, map[string]any{"ok": true, "data": item, "revision": revision})
}
func (h *handlers) edgeInstancesCheck(w http.ResponseWriter, r *http.Request) {
	h.edgeInstancesCheckWithClient(w, r, edgeHTTPClient)
}
func (h *handlers) edgeInstancesCheckWithClient(w http.ResponseWriter, r *http.Request, client *http.Client) {
	if !auth.Can(r.Context(), "settings.manage") {
		edgeInstanceAPIError(w, 403, "forbidden")
		return
	}
	var in struct {
		Revision string `json:"revision"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.Decode(new(any)) != io.EOF {
		edgeInstanceAPIError(w, 400, "invalid_request")
		return
	}
	item, revision, err := h.checkEdgeInstance(r.Context(), client, chi.URLParam(r, "id"), in.Revision)
	if err != nil {
		h.edgeInstancesError(w, r, err)
		return
	}
	h.aiJSON(w, 200, map[string]any{"ok": true, "data": item, "revision": revision})
}
func (h *handlers) edgeInstancesError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "edge_registry_unavailable"
	switch {
	case errors.Is(err, errEdgeConflict):
		status, code = 409, "connection_changed"
	case errors.Is(err, errEdgeOrigin):
		status, code = 422, "origin_rejected"
	case errors.Is(err, errEdgeCheck):
		status, code = 422, "manager_identity_check_failed"
	case errors.Is(err, errEdgeSettings):
		status, code = 422, "invalid_connection"
	}
	edgeInstanceAPIError(w, status, code)
}

func edgeInstanceAPIError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Cache-Control", "no-store")
	api.WriteError(w, status, code, strings.ReplaceAll(code, "_", " "), nil)
}
