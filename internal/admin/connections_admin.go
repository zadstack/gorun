package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/observe"
)

type connectionView struct {
	ID, Version, Name, Kind, Endpoint, Host, Provider, Model, Bucket, Region, TestedAt, TestStatus, TestProtocol string
	TestCount                                                                                                    int
	SecretSet                                                                                                    bool
}

func viewConnection(c connectionRecord) connectionView {
	host := ""
	if u, e := url.Parse(c.Endpoint); e == nil {
		host = u.Host
	}
	return connectionView{Host: host, ID: c.ID, Version: c.Version, Name: c.Name, Kind: c.Kind, Endpoint: c.Endpoint, Provider: c.Provider, Model: c.Model, Bucket: c.Bucket, Region: c.Region, TestedAt: c.TestedAt, TestStatus: c.TestStatus, TestCount: c.TestCount, TestProtocol: c.TestProtocol, SecretSet: c.Secret != ""}
}

type metricoolConnectionSummary struct {
	State, TestedAt    string
	CanDrafts, Present bool
}

type connectionsPage struct {
	Metricool   metricoolConnectionSummary
	Rows        []connectionView
	Form        connectionView
	Error       string
	AIEditToken string
	ReloadURL   string
}

func (h *handlers) renderConnections(w http.ResponseWriter, r *http.Request, status int, form connectionRecord, message string) {
	rows, err := h.listConnections(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	snap, err := aiSnapshot(r.Context(), h.d.DB)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	metricool, err := h.metricoolConfig(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	mv := metricoolView(metricool)
	summary := metricoolConnectionSummary{Present: metricool.Version != "" || metricool.Secret != "" || metricool.UserID != "" || metricool.BlogID != "", State: "not_configured", CanDrafts: auth.Can(r.Context(), "content.publish")}
	if mv.SecretSet {
		summary.State = "untested"
		if mv.TestStatus == "pass" || mv.TestStatus == "fail" {
			summary.State = mv.TestStatus
			summary.TestedAt = mv.TestedAt
		}
	}
	data := connectionsPage{Metricool: summary, Form: viewConnection(form), Error: message, AIEditToken: generalEditToken(snap)}
	if strings.HasPrefix(r.URL.Path, "/admin/settings/storage/") {
		data.ReloadURL = "/admin/settings/storage"
		if status == 409 {
			data.Error = "connectionsetup.storage_conflict"
		}
	}
	for _, row := range rows {
		data.Rows = append(data.Rows, viewConnection(row))
	}
	w.Header().Set("Cache-Control", "no-store")
	h.tpl.RenderWith(w, r, status, "admin", "pages/settings/connections.html", data)
}
func (h *handlers) connectionsList(w http.ResponseWriter, r *http.Request) {
	c := connectionRecord{Kind: "mcp", Provider: "openai", Region: "auto"}
	if id := r.URL.Query().Get("edit"); id != "" {
		var err error
		c, err = h.loadConnection(r.Context(), id)
		if err != nil {
			h.connectionError(w, r, err)
			return
		}
	}
	h.renderConnections(w, r, 200, c, "")
}
func (h *handlers) connectionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errConnectionConflict) || errors.Is(err, errAIConflict) {
		h.renderConnections(w, r, 409, connectionRecord{}, "admin.connections.conflict")
		return
	}
	if connectionMissing(err) {
		h.renderConnections(w, r, 422, connectionRecord{}, "admin.connections.invalid")
		return
	}
	h.serverError(w, r, err)
}
func (h *handlers) connectionsSave(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	id := r.PostForm.Get("id")
	expected := r.PostForm.Get("version")
	proposed := connectionRecord{ID: id, Version: expected, Name: strings.TrimSpace(r.PostForm.Get("name")), Kind: r.PostForm.Get("kind"), Endpoint: strings.TrimSpace(r.PostForm.Get("endpoint")), Provider: r.PostForm.Get("provider"), Model: strings.TrimSpace(r.PostForm.Get("model")), Bucket: strings.TrimSpace(r.PostForm.Get("bucket")), Region: strings.TrimSpace(r.PostForm.Get("region"))}
	var previous connectionRecord
	var err error
	if id != "" {
		previous, err = h.loadConnection(r.Context(), id)
		if err != nil {
			h.connectionError(w, r, err)
			return
		}
		if previous.Version != expected {
			proposed.Kind = previous.Kind
			h.renderConnections(w, r, 409, proposed, "admin.connections.conflict")
			return
		}
	} else {
		id = db.NewID()
		if expected != "" {
			h.connectionError(w, r, errConnectionConflict)
			return
		}
	}
	c := connectionRecord{ID: id, Name: strings.TrimSpace(r.PostForm.Get("name")), Kind: r.PostForm.Get("kind"), Endpoint: strings.TrimSpace(r.PostForm.Get("endpoint")), Provider: r.PostForm.Get("provider"), Model: strings.TrimSpace(r.PostForm.Get("model")), Bucket: strings.TrimSpace(r.PostForm.Get("bucket")), Region: strings.TrimSpace(r.PostForm.Get("region"))}
	c.Endpoint, err = normalizeConnectionEndpoint(c.Endpoint)
	if c.Region == "" {
		c.Region = "auto"
	}
	if err != nil || validateConnection(c) != nil || (previous.ID != "" && previous.Kind != c.Kind) {
		c.ID = previous.ID
		c.Version = expected
		h.renderConnections(w, r, 422, c, "admin.connections.invalid")
		return
	}
	s, err := h.connectionSecrets(previous)
	if err != nil {
		h.connectionError(w, r, err)
		return
	}
	if r.PostForm.Get("clear_secret") != "" {
		s = connectionSecrets{}
	}
	fields := []struct {
		name  string
		value *string
	}{{"token", &s.Token}, {"access_key", &s.AccessKey}, {"secret_key", &s.SecretKey}}
	for _, field := range fields {
		v := r.PostForm.Get(field.name)
		if !validConnectionSecret(v) {
			c.ID = previous.ID
			c.Version = expected
			h.renderConnections(w, r, 422, c, "admin.connections.invalid")
			return
		}
		if v != "" {
			*field.value = v
		}
	}
	c.Secret, err = h.encryptConnectionSecrets(s)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	c = changedConnection(c)
	if err = h.writeConnection(r.Context(), c, expected, "connections.save", false); err != nil {
		if errors.Is(err, errConnectionConflict) {
			h.renderConnections(w, r, 409, proposed, "admin.connections.conflict")
			return
		}
		h.connectionError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.connections.saved")
	redirect(w, r, "/admin/settings/connections")
}
func (h *handlers) connectionAction(w http.ResponseWriter, r *http.Request) (connectionRecord, bool) {
	if r.ParseForm() != nil {
		http.Error(w, "Invalid form", 400)
		return connectionRecord{}, false
	}
	c, err := h.loadConnection(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.connectionError(w, r, err)
		return c, false
	}
	if c.Version != r.PostForm.Get("version") {
		h.connectionError(w, r, errConnectionConflict)
		return c, false
	}
	return c, true
}
func (h *handlers) connectionsRemove(w http.ResponseWriter, r *http.Request) {
	c, ok := h.connectionAction(w, r)
	if !ok {
		return
	}
	if err := h.writeConnection(r.Context(), c, c.Version, "connections.remove", true); err != nil {
		h.connectionError(w, r, err)
		return
	}
	redirect(w, r, "/admin/settings/connections")
}
func (h *handlers) connectionsTest(w http.ResponseWriter, r *http.Request) {
	c, ok := h.connectionAction(w, r)
	if !ok {
		return
	}
	s, err := h.connectionSecrets(c)
	if err != nil {
		h.connectionError(w, r, err)
		return
	}
	started := time.Now()
	result, probeErr := probeConnection(r.Context(), c, s)
	if err = h.writeConnection(r.Context(), recordConnectionProbe(c, result, probeErr), c.Version, "connections.test", false); err != nil {
		h.connectionError(w, r, err)
		return
	}
	// The same evidence feeds the capability report, bound to this revision.
	if err := h.observe.Record(r.Context(), kindConnection, c.ID, c.Version, observe.Result{Scope: "connectivity", Data: map[string]any{"count": result.Count, "protocol": result.Protocol}}, probeErr, time.Since(started)); err != nil {
		h.d.Log.Warn("admin: record connection observation", "err", err)
	}
	redirect(w, r, "/admin/settings/connections")
}
func (h *handlers) connectionsUseAI(w http.ResponseWriter, r *http.Request) {
	c, ok := h.connectionAction(w, r)
	if !ok {
		return
	}
	if c.Kind != "ai" || validateConnection(c) != nil || (c.Provider == "anthropic" && c.Endpoint != "https://api.anthropic.com/v1") {
		h.connectionError(w, r, errConnectionInvalid)
		return
	}
	s, err := h.connectionSecrets(c)
	if err != nil {
		h.connectionError(w, r, err)
		return
	}
	vals, fields := AIConfig(c.Provider, c.Endpoint, c.Model, s.Token, false, h.d.Config.SecretKey)
	if len(fields) > 0 {
		h.connectionError(w, r, errConnectionInvalid)
		return
	}
	if s.Token == "" {
		vals[aiSettingAPIKey] = ""
	}
	err = db.InTx(r.Context(), h.d.DB, func(tx *sql.Tx) error {
		if err := checkAISnapshot(r.Context(), tx, r.PostForm.Get("ai_edit_token")); err != nil {
			return err
		}
		var version string
		if err := tx.QueryRowContext(r.Context(), `SELECT json_extract(value_json,'$.version') FROM settings WHERE key=?`, connectionPrefix+c.ID).Scan(&version); err != nil {
			return err
		}
		if version != c.Version {
			return errConnectionConflict
		}
		for k, v := range vals {
			if err := h.d.Settings.SetTx(r.Context(), tx, k, v); err != nil {
				return err
			}
		}
		h.d.Audit.RecordTx(r.Context(), tx, "connections.activate_ai", "connection", c.ID, map[string]any{"provider": c.Provider})
		return nil
	})
	if err != nil {
		h.connectionError(w, r, err)
		return
	}
	httpx.SetFlash(w, "success", "admin.connections.activated")
	redirect(w, r, "/admin/settings/ai")
}
