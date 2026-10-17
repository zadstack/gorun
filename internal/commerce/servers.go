package commerce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

// Server statuses.
const (
	ServerUnknown = "unknown"
	ServerOK      = "ok"
	ServerError   = "error"
	protocolMCBB  = "mcbb_v1"
	maxErrorLen   = 500
)

const serverColumns = `id, name, base_url, client_id, secret_enc, protocol, status, last_check_at, last_error`

type serverRow struct {
	Server
	secretEnc string
}

func scanServer(row interface{ Scan(...any) error }) (*serverRow, error) {
	var r serverRow
	if err := row.Scan(&r.ID, &r.Name, &r.BaseURL, &r.ClientID, &r.secretEnc, &r.Protocol, &r.Status, &r.LastCheckAt, &r.LastError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("commerce: scan server: %w", err)
	}
	r.SecretSet = r.secretEnc != ""
	return &r, nil
}

func (s *service) server(ctx context.Context, id string) (*serverRow, error) {
	return scanServer(s.db.QueryRowContext(ctx, `SELECT `+serverColumns+` FROM billing_servers WHERE id = ?`, id))
}

// Server returns one billing server (never the secret).
func (s *service) Server(ctx context.Context, id string) (*Server, error) {
	r, err := s.server(ctx, id)
	if err != nil {
		return nil, err
	}
	return &r.Server, nil
}

func (s *service) Servers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverColumns+` FROM billing_servers ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("commerce: list servers: %w", err)
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		r, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r.Server)
	}
	return out, rows.Err()
}

func (s *service) SaveServer(ctx context.Context, srv Server) error {
	_, err := s.saveServer(ctx, srv)
	return err
}

// saveServer creates (empty ID) or updates a server and returns it. An
// empty Secret keeps the stored one; a new server needs a secret.
func (s *service) saveServer(ctx context.Context, srv Server) (*Server, error) {
	srv.Name, srv.ClientID = strings.TrimSpace(srv.Name), strings.TrimSpace(srv.ClientID)
	srv.BaseURL = strings.TrimSpace(srv.BaseURL)
	if srv.Protocol == "" {
		srv.Protocol = protocolMCBB
	}
	fields := map[string]string{}
	if srv.Name == "" {
		fields["name"] = "commerce.error.required"
	}
	if srv.ClientID == "" {
		fields["client_id"] = "commerce.error.required"
	}
	if base, err := validateBaseURL(srv.BaseURL, s.d.Config.AllowPrivateNet); err != nil {
		fields["base_url"] = "commerce.error.url_https"
	} else {
		srv.BaseURL = base
	}
	if srv.Protocol != protocolMCBB {
		fields["protocol"] = "commerce.error.protocol_invalid"
	}
	create := srv.ID == ""
	var existing *serverRow
	if !create {
		var err error
		if existing, err = s.server(ctx, srv.ID); err != nil {
			return nil, err
		}
	}
	if create && srv.Secret == "" {
		fields["secret"] = "commerce.error.required"
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	if create {
		srv.ID = db.NewID()
	}
	enc := ""
	if existing != nil {
		enc = existing.secretEnc
	}
	if srv.Secret != "" {
		var err error
		if enc, err = db.Encrypt(s.d.Config.SecretKey, []byte(srv.Secret), srv.ID); err != nil {
			return nil, err
		}
	}
	now := s.nowString()
	var err error
	if create {
		_, err = s.db.ExecContext(ctx, `INSERT INTO billing_servers (id, name, base_url, client_id, secret_enc, protocol, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'unknown', ?, ?)`, srv.ID, srv.Name, srv.BaseURL, srv.ClientID, enc, srv.Protocol, now, now)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE billing_servers SET name = ?, base_url = ?, client_id = ?, secret_enc = ?, protocol = ?, updated_at = ?
			WHERE id = ?`, srv.Name, srv.BaseURL, srv.ClientID, enc, srv.Protocol, now, srv.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("commerce: save server: %w", err)
	}
	s.health.forget(srv.ID)
	s.audit.Record(ctx, "commerce.server.save", "billing_server", srv.ID, map[string]any{"name": srv.Name, "created": create, "secret_changed": srv.Secret != ""})
	return s.Server(ctx, srv.ID)
}

// DeleteServer removes a server; products mapped to it lose the mapping.
func (s *service) DeleteServer(ctx context.Context, id string) error {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM entitlements WHERE server_id = ? AND status IN ('pending','active')`, id).Scan(&n)
	if n > 0 {
		return &ValidationError{Fields: map[string]string{"server": "commerce.error.server_in_use"}}
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM billing_servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("commerce: delete server: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.health.forget(id)
	s.audit.Record(ctx, "commerce.server.delete", "billing_server", id, nil)
	return nil
}

// client builds the signed protocol client for a stored server.
func (s *service) client(r *serverRow) (*mcbbClient, error) {
	secret, err := db.Decrypt(s.d.Config.SecretKey, r.secretEnc, r.ID)
	if err != nil {
		return nil, fmt.Errorf("commerce: server %s secret: %w", r.ID, err)
	}
	return newMCBBClient(r.BaseURL, r.ClientID, string(secret), s.d.Config.AllowPrivateNet)
}

func (s *service) clientFor(ctx context.Context, serverID string) (*mcbbClient, error) {
	r, err := s.server(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return s.client(r)
}

// TestServer calls GET /catalog, records the outcome on the server row
// (status, last_check_at, last_error, catalog) and returns the catalog.
func (s *service) TestServer(ctx context.Context, id string) (Catalog, error) {
	r, err := s.server(ctx, id)
	if err != nil {
		return Catalog{}, err
	}
	cat, callErr := s.fetchCatalog(ctx, r)
	status, lastErr, raw := ServerOK, "", string(cat.Raw)
	if callErr != nil {
		status, lastErr, raw = ServerError, truncate(callErr.Error(), maxErrorLen), "{}"
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE billing_servers SET status = ?, last_check_at = ?, last_error = ?, catalog_json = ? WHERE id = ?`,
		status, s.nowString(), lastErr, raw, id); err != nil {
		return Catalog{}, fmt.Errorf("commerce: record server check: %w", err)
	}
	s.health.put(id, callErr)
	s.audit.Record(ctx, "commerce.server.test", "billing_server", id, map[string]any{"status": status, "error": lastErr, "plans": len(cat.Plans)})
	return cat, callErr
}

func (s *service) fetchCatalog(ctx context.Context, r *serverRow) (Catalog, error) {
	c, err := s.client(r)
	if err != nil {
		return Catalog{}, err
	}
	data, err := c.Do(ctx, "GET", "/catalog", nil, "")
	if err != nil {
		return Catalog{}, err
	}
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return Catalog{}, &MCBBError{Status: 200, Message: "catalog is not an object: " + err.Error()}
	}
	if cat.Plans == nil {
		cat.Plans = []Plan{}
	}
	cat.Raw = data
	return cat, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
