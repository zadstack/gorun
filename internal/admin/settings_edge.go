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
	edgeKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	edgeHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	edgeIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type edgePage struct {
	Origin, Hint, Label, ManagerID, SiteID, CheckedAt string
	KeySet                                            bool
	Fields                                            map[string]string
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

func (h *handlers) currentEdge(r *http.Request) edgePage {
	ctx := r.Context()
	get := func(k string) string { return h.d.Settings.GetString(ctx, k, "") }
	key := get(settingEdgeKey)
	return edgePage{Origin: get(settingEdgeOrigin), Hint: get(settingEdgeHint), Label: get(settingEdgeLabel), ManagerID: get(settingEdgeManager), SiteID: get(settingEdgeSite), CheckedAt: get(settingEdgeChecked), KeySet: key != ""}
}

func (h *handlers) renderEdge(w http.ResponseWriter, r *http.Request, status int, page edgePage, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	page.Fields = fields
	h.tpl.RenderWith(w, r, status, "admin", "pages/settings/edge.html", page)
}

func (h *handlers) settingsEdge(w http.ResponseWriter, r *http.Request) {
	h.renderEdge(w, r, http.StatusOK, h.currentEdge(r), nil)
}

func (h *handlers) edgeValues(r *http.Request, in edgeInput) (map[string]any, map[string]string, bool) {
	ctx := r.Context()
	fields := map[string]string{}
	origin, err := validateEdgeOrigin(in.Origin)
	if err != nil {
		fields["origin"] = "admin.settings.edge.error.origin"
	}
	key := strings.TrimSpace(in.Key)
	stored := h.d.Settings.GetString(ctx, settingEdgeKey, "")
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
	if replaced || (origin != "" && origin != h.d.Settings.GetString(ctx, settingEdgeOrigin, "")) {
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
	vals, fields, replaced := h.edgeValues(r, in)
	page := h.currentEdge(r)
	page.Origin = strings.TrimSpace(in.Origin)
	if len(fields) > 0 {
		h.renderEdge(w, r, http.StatusUnprocessableEntity, page, fields)
		return
	}
	if err := h.saveEdgeKeys(r.Context(), vals); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(r.Context(), "settings.edge", "settings", "edge.origin", map[string]any{"origin": vals[settingEdgeOrigin], "key_replaced": replaced})
	httpx.SetFlash(w, "success", "admin.settings.saved")
	redirect(w, r, "/admin/settings/edge")
}

// saveEdgeKeys writes every Edge setting in one transaction. A failure rolls
// the whole set back, so a stored key cannot be left pointing at a new origin.
func (h *handlers) saveEdgeKeys(ctx context.Context, vals map[string]any) error {
	keys := slices.Sorted(maps.Keys(vals))
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		for _, k := range keys {
			if err := h.d.Settings.SetTx(ctx, tx, k, vals[k]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (h *handlers) settingsEdgeCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page := h.currentEdge(r)
	origin, err := validateEdgeOrigin(page.Origin)
	enc := h.d.Settings.GetString(ctx, settingEdgeKey, "")
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
	info, err := edgeWhoami(ctx, edgeHTTPClient, origin, string(plain))
	if err != nil {
		h.renderEdge(w, r, http.StatusUnprocessableEntity, page, map[string]string{"check": "admin.settings.edge.error.check"})
		return
	}
	checked := time.Now().UTC().Format(time.RFC3339)
	if err := h.saveEdgeKeys(ctx, map[string]any{settingEdgeManager: info.ManagerID, settingEdgeLabel: info.Label, settingEdgeSite: info.SiteID, settingEdgeChecked: checked}); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.d.Audit.Record(ctx, "settings.edge_check", "settings", info.SiteID, map[string]any{"manager_id": info.ManagerID, "origin": origin})
	httpx.SetFlash(w, "success", "admin.settings.edge.checked")
	redirect(w, r, "/admin/settings/edge")
}
