package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

const (
	keyPrefixLen = 8
	keySecretLen = 32
	keyAlphabet  = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// APIKey is a bearer key: plaintext "gp_" + Prefix (8) + secret (32), stored
// as SHA-256. Scopes are permission strings; "*" grants all of the owner's
// permissions.
type APIKey struct {
	ID, Name, Prefix, UserID                    string
	Kind                                        string // KeyKindAPI or KeyKindAgent
	Scopes                                      []string
	RatePerMin                                  int
	ExpiresAt, LastUsedAt, CreatedAt, RevokedAt string
	UseCount                                    int64  // approximate: flushed at most once a minute
	RotatedFrom                                 string // id of the key this one replaced
}

// API key kinds. API keys authenticate on /api (and /mcp); agent keys only
// on /mcp, so an AI agent can never reach the REST API (for example to
// approve its own sensitive calls).
const (
	KeyKindAPI   = "api"
	KeyKindAgent = "agent"
)

// KeyService manages API keys.
type KeyService interface {
	// Create creates an API key of kind KeyKindAPI.
	Create(ctx context.Context, name, userID string, scopes []string, rate int, expires string) (plaintext string, k *APIKey, err error)
	// CreateKind is Create with an explicit kind (KeyKindAPI or KeyKindAgent).
	CreateKind(ctx context.Context, kind, name, userID string, scopes []string, rate int, expires string) (plaintext string, k *APIKey, err error)
	List(ctx context.Context) ([]APIKey, error)
	Get(ctx context.Context, id string) (*APIKey, error)
	Revoke(ctx context.Context, id string) error
	// Rotate issues a new key with the same name, kind, owner, scopes, rate
	// and expiry, and retires the old one: revoked now when grace is 0,
	// otherwise expiring after grace (so clients can switch without downtime).
	Rotate(ctx context.Context, id string, grace time.Duration) (plaintext string, k *APIKey, err error)
}

// MaxRotateGrace bounds how long a rotated key keeps working.
const MaxRotateGrace = 7 * 24 * time.Hour

type keyService struct {
	db    *sql.DB
	audit *audit.Writer
	perms *kernel.PermissionRegistry
	now   func() time.Time

	mu   sync.Mutex
	uses map[string]int64 // authenticated requests not yet flushed, by key id
}

// NewKeyService returns the API key service on d. Create validates scopes
// against perms (plus "*"; nil skips the check); creation and revocation are
// recorded through a (nil: not audited). rate <= 0 means 60/min; expires is
// RFC3339 (normalised to UTC) or "" for never.
func NewKeyService(d *sql.DB, a *audit.Writer, perms *kernel.PermissionRegistry) KeyService {
	return &keyService{db: d, audit: a, perms: perms, now: time.Now, uses: map[string]int64{}}
}

func (s *keyService) record(ctx context.Context, action, id string, meta any) {
	if s.audit != nil {
		s.audit.Record(ctx, action, "api_key", id, meta)
	}
}

// unknownScopes returns scopes that are neither "*" nor declared permissions.
func (s *keyService) unknownScopes(scopes []string) []string {
	if s.perms == nil {
		return nil
	}
	known := map[string]bool{"*": true}
	for _, p := range s.perms.All() {
		known[p.Name] = true
	}
	var bad []string
	for _, sc := range scopes {
		if !known[sc] {
			bad = append(bad, sc)
		}
	}
	return bad
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	for i := range b {
		b[i] = keyAlphabet[int(b[i])%len(keyAlphabet)] // 256 % 36 bias is negligible for 32+ chars
	}
	return string(b)
}

func (s *keyService) Create(ctx context.Context, name, userID string, scopes []string, rate int, expires string) (string, *APIKey, error) {
	return s.CreateKind(ctx, KeyKindAPI, name, userID, scopes, rate, expires)
}

func (s *keyService) CreateKind(ctx context.Context, kind, name, userID string, scopes []string, rate int, expires string) (string, *APIKey, error) {
	fields := map[string]string{}
	if kind != KeyKindAPI && kind != KeyKindAgent {
		fields["kind"] = "auth.error.key_kind_invalid"
	}
	if strings.TrimSpace(name) == "" {
		fields["name"] = "core.validation.required"
	}
	if len(scopes) == 0 {
		fields["scopes"] = "core.validation.required"
	} else if len(s.unknownScopes(scopes)) > 0 {
		fields["scopes"] = "auth.error.scope_invalid"
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = ?`, userID).Scan(&n); err != nil {
		return "", nil, fmt.Errorf("auth: check key owner: %w", err)
	}
	if n == 0 {
		fields["user_id"] = "auth.error.user_invalid"
	}
	if expires != "" {
		if ts, err := time.Parse(time.RFC3339, expires); err != nil {
			fields["expires_at"] = "auth.error.date_invalid"
		} else {
			expires = db.FormatTime(ts)
		}
	}
	if len(fields) > 0 {
		return "", nil, &ValidationError{Fields: fields}
	}
	if rate <= 0 {
		rate = defaultKeyRate
	}
	scopesJSON, _ := json.Marshal(scopes)
	k := &APIKey{ID: db.NewID(), Name: strings.TrimSpace(name), Prefix: randomString(keyPrefixLen), UserID: userID, Kind: kind,
		Scopes: scopes, RatePerMin: rate, ExpiresAt: expires, CreatedAt: db.FormatTime(s.now())}
	plaintext := "gp_" + k.Prefix + randomString(keySecretLen)
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_keys (id, name, prefix, hash, user_id, kind, scopes_json, rate_per_min, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, k.ID, k.Name, k.Prefix, hashToken(plaintext), userID, kind, string(scopesJSON), rate, expires, k.CreatedAt)
	if err != nil {
		return "", nil, fmt.Errorf("auth: create api key: %w", err)
	}
	s.record(ctx, "api_key.create", k.ID, map[string]any{"name": k.Name, "user_id": userID, "scopes": scopes, "kind": kind})
	return plaintext, k, nil
}

const keyColumns = `id, name, prefix, user_id, kind, scopes_json, rate_per_min, expires_at, last_used_at, created_at, revoked_at, use_count, rotated_from`

func scanKey(row interface{ Scan(...any) error }, extra ...any) (*APIKey, error) {
	var k APIKey
	var scopes string
	dest := append([]any{&k.ID, &k.Name, &k.Prefix, &k.UserID, &k.Kind, &scopes, &k.RatePerMin, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt, &k.RevokedAt, &k.UseCount, &k.RotatedFrom}, extra...)
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("auth: scan api key: %w", err)
	}
	if err := json.Unmarshal([]byte(scopes), &k.Scopes); err != nil {
		return nil, fmt.Errorf("auth: api key scopes: %w", err)
	}
	return &k, nil
}

func (s *keyService) List(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+keyColumns+` FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("auth: list api keys: %w", err)
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func (s *keyService) Revoke(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at = ''`, db.FormatTime(s.now()), id)
	if err != nil {
		return fmt.Errorf("auth: revoke api key: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.record(ctx, "api_key.revoke", id, nil)
	return nil
}

// authenticate resolves a plaintext key to the key and its active owner.
func (s *keyService) authenticate(ctx context.Context, plaintext string) (*APIKey, *User, error) {
	if !strings.HasPrefix(plaintext, "gp_") || len(plaintext) != 3+keyPrefixLen+keySecretLen {
		return nil, nil, ErrInvalidToken
	}
	var hash string
	k, err := scanKey(s.db.QueryRowContext(ctx, `SELECT `+keyColumns+`, hash FROM api_keys WHERE prefix = ?`,
		plaintext[3:3+keyPrefixLen]), &hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil, ErrInvalidToken
		}
		return nil, nil, err
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(plaintext)), []byte(hash)) != 1 || k.RevokedAt != "" {
		return nil, nil, ErrInvalidToken
	}
	if k.ExpiresAt != "" {
		if exp, err := db.ParseTime(k.ExpiresAt); err != nil || !s.now().Before(exp) {
			return nil, nil, ErrInvalidToken
		}
	}
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, k.UserID))
	if err != nil || u.Status != StatusActive {
		return nil, nil, ErrInvalidToken
	}
	return k, u, nil
}

// touch counts a use and records last use (with the pending count) at most
// once a minute per key, so authentication does not write on every request.
func (s *keyService) touch(ctx context.Context, k *APIKey) {
	now := s.now()
	s.mu.Lock()
	if s.uses == nil {
		s.uses = map[string]int64{}
	}
	s.uses[k.ID]++
	if last, err := db.ParseTime(k.LastUsedAt); err == nil && now.Sub(last) < time.Minute {
		s.mu.Unlock()
		return
	}
	n := s.uses[k.ID]
	delete(s.uses, k.ID)
	s.mu.Unlock()
	_, _ = s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE api_keys SET last_used_at = ?, use_count = use_count + ? WHERE id = ?`, db.FormatTime(now), n, k.ID)
}

func (s *keyService) Get(ctx context.Context, id string) (*APIKey, error) {
	return scanKey(s.db.QueryRowContext(ctx, `SELECT `+keyColumns+` FROM api_keys WHERE id = ?`, id))
}

func (s *keyService) Rotate(ctx context.Context, id string, grace time.Duration) (string, *APIKey, error) {
	if grace < 0 || grace > MaxRotateGrace {
		return "", nil, &ValidationError{Fields: map[string]string{"grace": "auth.error.grace_invalid"}}
	}
	old, err := s.Get(ctx, id)
	if err != nil {
		return "", nil, err
	}
	now := s.now()
	if old.RevokedAt != "" {
		return "", nil, ErrKeyRevoked
	}
	if old.ExpiresAt != "" {
		if exp, err := db.ParseTime(old.ExpiresAt); err == nil && !now.Before(exp) {
			return "", nil, ErrKeyExpired
		}
	}
	scopesJSON, _ := json.Marshal(old.Scopes)
	k := &APIKey{ID: db.NewID(), Name: old.Name, Prefix: randomString(keyPrefixLen), UserID: old.UserID, Kind: old.Kind,
		Scopes: old.Scopes, RatePerMin: old.RatePerMin, ExpiresAt: old.ExpiresAt, CreatedAt: db.FormatTime(now), RotatedFrom: old.ID}
	plaintext := "gp_" + k.Prefix + randomString(keySecretLen)
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_keys (id, name, prefix, hash, user_id, kind, scopes_json, rate_per_min, expires_at, created_at, rotated_from)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, k.ID, k.Name, k.Prefix, hashToken(plaintext), k.UserID, k.Kind, string(scopesJSON), k.RatePerMin, k.ExpiresAt, k.CreatedAt, k.RotatedFrom); err != nil {
			return err
		}
		if grace == 0 {
			_, err := tx.ExecContext(ctx, `UPDATE api_keys SET revoked_at = ? WHERE id = ?`, db.FormatTime(now), old.ID)
			return err
		}
		retire := now.Add(grace)
		if old.ExpiresAt != "" {
			if exp, err := db.ParseTime(old.ExpiresAt); err == nil && exp.Before(retire) {
				retire = exp
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE api_keys SET expires_at = ? WHERE id = ?`, db.FormatTime(retire), old.ID)
		return err
	})
	if err != nil {
		return "", nil, fmt.Errorf("auth: rotate api key: %w", err)
	}
	s.record(ctx, "api_key.rotate", k.ID, map[string]any{"replaces": old.ID, "grace_seconds": int(grace.Seconds())})
	return plaintext, k, nil
}

// Errors from WithAPIKeyActor (besides ErrNotFound for an unknown key).
var (
	ErrKeyRevoked       = errors.New("auth: api key revoked")
	ErrKeyExpired       = errors.New("auth: api key expired")
	ErrKeyOwnerInactive = errors.New("auth: api key owner is not active")
)

// WithAPIKeyActor returns ctx acting as the API key keyID, exactly as if the
// key had authenticated the request: Can checks the key's scopes and its
// owner's role, and audit records the key as the actor. It is for work done
// later on a key's behalf (e.g. an MCP call executed once a human approves
// it). The key must exist (ErrNotFound), not be revoked (ErrKeyRevoked) or
// expired (ErrKeyExpired), and its owner must be active
// (ErrKeyOwnerInactive).
func WithAPIKeyActor(ctx context.Context, d *kernel.Deps, keyID string) (context.Context, error) {
	k, err := scanKey(d.DB.QueryRowContext(ctx, `SELECT `+keyColumns+` FROM api_keys WHERE id = ?`, keyID))
	if err != nil {
		return nil, err
	}
	if k.RevokedAt != "" {
		return nil, ErrKeyRevoked
	}
	if k.ExpiresAt != "" {
		if exp, err := db.ParseTime(k.ExpiresAt); err != nil || !time.Now().Before(exp) {
			return nil, ErrKeyExpired
		}
	}
	u, err := scanUser(d.DB.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, k.UserID))
	if errors.Is(err, ErrNotFound) || (err == nil && u.Status != StatusActive) {
		return nil, ErrKeyOwnerInactive
	}
	if err != nil {
		return nil, err
	}
	return withPrincipal(ctx, &principal{kind: "api_key", user: u, key: k, scopes: k.Scopes}), nil
}
