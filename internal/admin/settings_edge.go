package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
)

const (
	settingEdgeOrigin  = "edge.origin"
	settingEdgeKey     = "edge.manager_key"
	settingEdgeHint    = "edge.manager_hint"
	settingEdgeManager = "edge.manager_id"
	settingEdgeLabel   = "edge.manager_label"
	settingEdgeSite    = "edge.manager_site"
	settingEdgeChecked = "edge.checked_at"
	edgeKeyAAD         = "edge.manager_key"
)

var (
	errEdgeOrigin   = errors.New("edge: origin rejected")
	errEdgeCheck    = errors.New("edge: manager check failed")
	errEdgeConflict = errors.New("edge: connection settings changed")
	errEdgeSettings = errors.New("edge: invalid stored settings")
	edgeKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	edgeHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	edgeIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type edgePage struct {
	Origin, Hint, Label, ManagerID, SiteID, CheckedAt string
	KeySet                                            bool
	Fields                                            map[string]string
}

// edgeSnapshot keeps the page and credentials from the same settings read.
// connection retains exact JSON and presence for comparison at commit time.
type edgeSnapshot struct {
	Page       edgePage
	Cipher     string
	connection map[string]string
}

type edgeInput struct {
	Origin string `form:"origin"`
	Key    string `form:"key"`
}

type edgeIdentity struct {
	SiteID, ManagerID, Label string
}

func publicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1]&0xc0 == 64 {
		return false
	}
	return true
}

func dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			return nil, errors.New("edge: blocked address")
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("edge: no address")
	}
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

var edgeHTTPClient = &http.Client{
	Timeout: 8 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         dialPublic,
		TLSHandshakeTimeout: 5 * time.Second,
		ForceAttemptHTTP2:   true,
	},
}

func validateEdgeOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" && u.Port() != "443" {
		return "", errEdgeOrigin
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || net.ParseIP(host) != nil || !edgeHostPattern.MatchString(host) {
		return "", errEdgeOrigin
	}
	return "https://" + host, nil
}

func edgeWhoami(ctx context.Context, client *http.Client, origin, key string) (edgeIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/v1/manager", nil)
	if err != nil {
		return edgeIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return edgeIdentity{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return edgeIdentity{}, errEdgeCheck
	}
	var payload struct {
		OK   bool `json:"ok"`
		Data struct {
			SiteID    string `json:"site_id"`
			ManagerID string `json:"manager_id"`
			Label     string `json:"label"`
			Primary   bool   `json:"primary"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload); err != nil || !payload.OK || !payload.Data.Primary || !slugRe.MatchString(payload.Data.SiteID) || !edgeIDPattern.MatchString(payload.Data.ManagerID) {
		return edgeIdentity{}, errEdgeCheck
	}
	label := strings.TrimSpace(payload.Data.Label)
	if label == "" || len(label) > 80 || strings.ContainsAny(label, "\r\n\x00<>") {
		return edgeIdentity{}, errEdgeCheck
	}
	return edgeIdentity{SiteID: payload.Data.SiteID, ManagerID: payload.Data.ManagerID, Label: label}, nil
}

func (h *handlers) currentEdge(ctx context.Context) (edgeSnapshot, error) {
	raw, err := h.d.Settings.All(ctx, "edge.")
	if err != nil {
		return edgeSnapshot{}, err
	}
	values := make(map[string]string, 7)
	for _, key := range []string{settingEdgeOrigin, settingEdgeKey, settingEdgeHint, settingEdgeManager, settingEdgeLabel, settingEdgeSite, settingEdgeChecked} {
		encoded, exists := raw[key]
		if !exists {
			continue
		}
		var value *string
		if err := json.Unmarshal(encoded, &value); err != nil || value == nil {
			return edgeSnapshot{}, errEdgeSettings
		}
		values[key] = *value
	}
	connection := make(map[string]string, 2)
	for _, key := range []string{settingEdgeOrigin, settingEdgeKey} {
		if value, exists := raw[key]; exists {
			connection[key] = string(value)
		}
	}
	return edgeSnapshot{
		Page:   edgePage{Origin: values[settingEdgeOrigin], Hint: values[settingEdgeHint], Label: values[settingEdgeLabel], ManagerID: values[settingEdgeManager], SiteID: values[settingEdgeSite], CheckedAt: values[settingEdgeChecked], KeySet: values[settingEdgeKey] != ""},
		Cipher: values[settingEdgeKey], connection: connection,
	}, nil
}

func (h *handlers) renderEdge(w http.ResponseWriter, r *http.Request, status int, page edgePage, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	page.Fields = fields
	h.tpl.RenderWith(w, r, status, "admin", "pages/settings/edge.html", page)
}

func (h *handlers) settingsEdge(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.currentEdge(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.renderEdge(w, r, http.StatusOK, snapshot.Page, nil)
}

func (h *handlers) edgeValues(in edgeInput, snapshot edgeSnapshot) (map[string]any, map[string]string, bool) {
	fields := map[string]string{}
	origin, err := validateEdgeOrigin(in.Origin)
	if err != nil {
		fields["origin"] = "admin.settings.edge.error.origin"
	}
	key := strings.TrimSpace(in.Key)
	stored := snapshot.Cipher
	replaced := false
	vals := map[string]any{}
	if origin != "" {
		vals[settingEdgeOrigin] = origin
	}
	switch {
	case key == "" && stored == "":
		fields["key"] = "admin.settings.edge.error.key"
	case key != "":
		if !edgeKeyPattern.MatchString(key) {
			fields["key"] = "admin.settings.edge.error.key"
			break
		}
		enc, err := db.Encrypt(h.d.Config.SecretKey, []byte(key), edgeKeyAAD)
		if err != nil {
			fields["key"] = "admin.settings.edge.error.encrypt"
			break
		}
		vals[settingEdgeKey] = enc
		vals[settingEdgeHint] = key[len(key)-4:]
		replaced = true
	}
	if replaced || (origin != "" && origin != snapshot.Page.Origin) {
		vals[settingEdgeChecked] = ""
		vals[settingEdgeManager] = ""
		vals[settingEdgeLabel] = ""
		vals[settingEdgeSite] = ""
	}
	return vals, fields, replaced
}

func (h *handlers) settingsEdgeSave(w http.ResponseWriter, r *http.Request) {
	var in edgeInput
	if err := httpx.Bind(r, &in); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	snapshot, err := h.currentEdge(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	vals, fields, replaced := h.edgeValues(in, snapshot)
	page := snapshot.Page
	page.Origin = strings.TrimSpace(in.Origin)
	if len(fields) > 0 {
		h.renderEdge(w, r, http.StatusUnprocessableEntity, page, fields)
		return
	}
	if err := h.saveEdgeKeys(r.Context(), snapshot, vals); err != nil {
		h.edgeWriteError(w, r, err, "key")
		return
	}
	h.d.Audit.Record(r.Context(), "settings.edge", "settings", "edge.origin", map[string]any{"origin": vals[settingEdgeOrigin], "key_replaced": replaced})
	httpx.SetFlash(w, "success", "admin.settings.saved")
	redirect(w, r, "/admin/settings/edge")
}

// saveEdgeKeys compares the connection used to prepare this operation before
// writing. The transaction prevents another save between comparison and writes;
// remote checks and encryption run before acquiring this writer transaction.
func (h *handlers) saveEdgeKeys(ctx context.Context, snapshot edgeSnapshot, vals map[string]any) error {
	keys := slices.Sorted(maps.Keys(vals))
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT key,value_json FROM settings WHERE key IN (?,?)`, settingEdgeOrigin, settingEdgeKey)
		if err != nil {
			return err
		}
		defer rows.Close()
		current := make(map[string]string, 2)
		for rows.Next() {
			var key, raw string
			if err := rows.Scan(&key, &raw); err != nil {
				return err
			}
			current[key] = raw
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if !maps.Equal(snapshot.connection, current) {
			return errEdgeConflict
		}
		for _, k := range keys {
			if err := h.d.Settings.SetTx(ctx, tx, k, vals[k]); err != nil {
				return err
			}
		}
		return nil
	})
}

// edgeWriteError renders the current configuration on a conflict, never the
// obsolete form or remote identity from the request that lost the comparison.
func (h *handlers) edgeWriteError(w http.ResponseWriter, r *http.Request, err error, field string) {
	if !errors.Is(err, errEdgeConflict) {
		h.serverError(w, r, err)
		return
	}
	snapshot, err := h.currentEdge(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.renderEdge(w, r, http.StatusConflict, snapshot.Page, map[string]string{field: "admin.settings.edge.error.conflict"})
}

func (h *handlers) settingsEdgeCheck(w http.ResponseWriter, r *http.Request) {
	h.settingsEdgeCheckWithClient(w, r, edgeHTTPClient)
}

func (h *handlers) settingsEdgeCheckWithClient(w http.ResponseWriter, r *http.Request, client *http.Client) {
	ctx := r.Context()
	snapshot, err := h.currentEdge(ctx)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	page := snapshot.Page
	origin, err := validateEdgeOrigin(page.Origin)
	enc := snapshot.Cipher
	if err != nil || enc == "" {
		fields := map[string]string{}
		if err != nil {
			fields["origin"] = "admin.settings.edge.error.origin"
		}
		if enc == "" {
			fields["key"] = "admin.settings.edge.error.key"
		}
		h.renderEdge(w, r, http.StatusUnprocessableEntity, page, fields)
		return
	}
	plain, err := db.Decrypt(h.d.Config.SecretKey, enc, edgeKeyAAD)
	if err != nil || !edgeKeyPattern.MatchString(string(plain)) {
		h.renderEdge(w, r, http.StatusUnprocessableEntity, page, map[string]string{"check": "admin.settings.edge.error.check"})
		return
	}
	info, err := edgeWhoami(ctx, client, origin, string(plain))
	if err != nil {
		h.renderEdge(w, r, http.StatusUnprocessableEntity, page, map[string]string{"check": "admin.settings.edge.error.check"})
		return
	}
	checked := time.Now().UTC().Format(time.RFC3339)
	if err := h.saveEdgeKeys(ctx, snapshot, map[string]any{settingEdgeManager: info.ManagerID, settingEdgeLabel: info.Label, settingEdgeSite: info.SiteID, settingEdgeChecked: checked}); err != nil {
		h.edgeWriteError(w, r, err, "check")
		return
	}
	h.d.Audit.Record(ctx, "settings.edge_check", "settings", info.SiteID, map[string]any{"manager_id": info.ManagerID, "origin": origin})
	httpx.SetFlash(w, "success", "admin.settings.edge.checked")
	redirect(w, r, "/admin/settings/edge")
}
