package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
)

// User statuses and the customer role.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	RoleAdmin      = "admin"
	RoleCustomer   = "customer"

	minPasswordLen = 10
	defaultPerPage = 20
	maxPerPage     = 100
)

// Errors returned by Service.
var (
	ErrNotFound           = errors.New("auth: not found")
	ErrEmailTaken         = errors.New("auth: email already registered")
	ErrInvalidCredentials = errors.New("auth: invalid email or password")
	ErrLocked             = errors.New("auth: too many failed attempts, try again later")
	ErrInvalidToken       = errors.New("auth: invalid or expired token")
	ErrSetupDone          = errors.New("auth: setup already completed")
)

// ValidationError lists invalid fields; values are i18n keys.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return fmt.Sprintf("auth: invalid input: %v", e.Fields) }

// User is an account. Customers have role "customer".
type User struct {
	ID, Email, Name, Role, Locale, Status string
	LastLoginAt, CreatedAt, UpdatedAt     string
}

// ListQuery filters Service.List. Sort: email, name, role, created_at,
// last_login_at (default created_at); Order: asc or desc (default desc).
type ListQuery struct {
	Search        string
	Page, PerPage int
	Sort, Order   string
}

// UserPatch updates only its non-nil fields.
type UserPatch struct {
	Name, Email, Role, Locale, Status, Password *string
}

// Service manages users.
type Service interface {
	Create(ctx context.Context, email, name, password, role string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	Get(ctx context.Context, id string) (*User, error)
	List(ctx context.Context, q ListQuery) ([]User, int, error)
	Update(ctx context.Context, id string, patch UserPatch) (*User, error)
	Delete(ctx context.Context, id string) error
	CheckPassword(ctx context.Context, email, password string) (*User, error) // rate-limited per IP (audit.IPFromContext) + email
	Count(ctx context.Context) (int, error)
}

type service struct {
	securityAudit securitySamples
	db            *sql.DB
	audit         *audit.Writer
	now           func() time.Time
	limiter       *loginLimiter
	verify        func(hash, password string) bool
}

// NewService returns the user service on d (auth migrations applied). Every
// change and every failed login is recorded through a (nil: not audited).
func NewService(d *sql.DB, a *audit.Writer) Service { return newService(d, a) }

func newService(d *sql.DB, a *audit.Writer) *service {
	return &service{db: d, audit: a, now: time.Now, limiter: newLoginLimiter(d), verify: VerifyPassword}
}

// record writes an audit entry about user id (after the change committed).
func (s *service) record(ctx context.Context, action, id string, meta any) {
	if s.audit != nil {
		s.audit.Record(ctx, action, "user", id, meta)
	}
}

const userColumns = `id, email, name, role, locale, status, last_login_at, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Locale, &u.Status, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: scan user: %w", err)
	}
	return &u, nil
}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s[strings.LastIndex(s, "@")+1:], ".")
}

func (s *service) roleExists(ctx context.Context, role string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM roles WHERE slug = ?`, role).Scan(&n)
	return n == 1
}

// validate checks a new account; customers may have no password.
func (s *service) validate(ctx context.Context, email, password, role string) error {
	fields := map[string]string{}
	if !validEmail(email) {
		fields["email"] = "auth.error.email_invalid"
	}
	if !(password == "" && role == RoleCustomer) && utf8.RuneCountInString(password) < minPasswordLen {
		fields["password"] = "auth.error.password_short"
	}
	if !s.roleExists(ctx, role) {
		fields["role"] = "auth.error.role_invalid"
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func (s *service) Create(ctx context.Context, email, name, password, role string) (*User, error) {
	return s.create(ctx, email, name, password, role, false)
}

// createFirstAdmin creates an admin only while no user exists (atomic).
func (s *service) createFirstAdmin(ctx context.Context, email, name, password string) (*User, error) {
	return s.create(ctx, email, name, password, RoleAdmin, true)
}

func (s *service) create(ctx context.Context, email, name, password, role string, onlyIfEmpty bool) (*User, error) {
	email, name = normalizeEmail(email), strings.TrimSpace(name)
	if err := s.validate(ctx, email, password, role); err != nil {
		return nil, err
	}
	hash := ""
	if password != "" {
		var err error
		if hash, err = HashPassword(password); err != nil {
			return nil, err
		}
	}
	id, now := db.NewID(), db.FormatTime(s.now())
	q := `INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?, 'active', ?, ?`
	if onlyIfEmpty {
		q = `INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at, login_name)
		SELECT ?, ?, ?, ?, ?, 'active', ?, ?, 'admin' WHERE NOT EXISTS (SELECT 1 FROM users)`
	}
	res, err := s.db.ExecContext(ctx, q, id, email, name, hash, role, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("auth: create user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrSetupDone
	}
	s.record(ctx, "user.create", id, map[string]string{"email": email, "role": role})
	return s.Get(ctx, id)
}

func (s *service) Get(ctx context.Context, id string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

func (s *service) GetByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, normalizeEmail(email)))
}

var sortColumns = map[string]string{"email": "email", "name": "name", "role": "role", "created_at": "created_at", "last_login_at": "last_login_at"}

func (s *service) List(ctx context.Context, q ListQuery) ([]User, int, error) {
	where, args := "", []any{}
	if q.Search != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q.Search) + "%"
		where = ` WHERE (email LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("auth: count users: %w", err)
	}
	col, ok := sortColumns[q.Sort]
	if !ok {
		col = "created_at"
	}
	dir := "DESC"
	if strings.EqualFold(q.Order, "asc") {
		dir = "ASC"
	}
	perPage := q.PerPage
	if perPage <= 0 {
		perPage = defaultPerPage
	}
	perPage = min(perPage, maxPerPage)
	page := max(q.Page, 1)
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users`+where+
		` ORDER BY `+col+` `+dir+`, id `+dir+` LIMIT ? OFFSET ?`, append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("auth: list users: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *u)
	}
	return out, total, rows.Err()
}

func (s *service) Update(ctx context.Context, id string, p UserPatch) (*User, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	sets, args, fields, changed := []string{}, []any{}, map[string]string{}, []string{}
	set := func(col string, v any) {
		sets, args, changed = append(sets, col+" = ?"), append(args, v), append(changed, col)
	}
	if p.Name != nil {
		set("name", strings.TrimSpace(*p.Name))
	}
	if p.Email != nil {
		e := normalizeEmail(*p.Email)
		if !validEmail(e) {
			fields["email"] = "auth.error.email_invalid"
		}
		set("email", e)
	}
	if p.Role != nil {
		if !s.roleExists(ctx, *p.Role) {
			fields["role"] = "auth.error.role_invalid"
		}
		set("role", *p.Role)
	}
	if p.Locale != nil {
		set("locale", *p.Locale)
	}
	if p.Status != nil {
		if *p.Status != StatusActive && *p.Status != StatusDisabled {
			fields["status"] = "auth.error.status_invalid"
		}
		set("status", *p.Status)
	}
	if p.Password != nil {
		if utf8.RuneCountInString(*p.Password) < minPasswordLen {
			fields["password"] = "auth.error.password_short"
		} else {
			h, err := HashPassword(*p.Password)
			if err != nil {
				return nil, err
			}
			set("password_hash", h)
			changed[len(changed)-1] = "password"
		}
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	if len(sets) == 0 {
		return cur, nil
	}
	sets, args = append(sets, "updated_at = ?"), append(args, db.FormatTime(s.now()), id)
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("auth: update user: %w", err)
	}
	if p.Password != nil || (p.Status != nil && *p.Status == StatusDisabled) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id) // force re-login
	}
	s.record(ctx, "user.update", id, map[string][]string{"fields": changed})
	return s.Get(ctx, id)
}

func (s *service) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("auth: delete user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.record(ctx, "user.delete", id, nil)
	return nil
}

func (s *service) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("auth: count users: %w", err)
	}
	return n, nil
}

// CheckPassword verifies credentials for an active user. Every attempt
// first increments the IP + email counter atomically; once it exceeds 5
// within 15 minutes the attempt is refused with ErrLocked before any password
// check, so parallel requests cannot bypass the limit. Success clears the
// email counter. A separate IP counter bounds attempts across emails,
// and at most four password verifications run concurrently. Failures are
// audited as user.login_failed.
func (s *service) CheckPassword(ctx context.Context, email, password string) (*User, error) {
	email = normalizeEmail(email)
	// Resolve the persistent alias before rate limiting so alternating email
	// and username cannot double the account's attempt allowance.
	if !strings.Contains(email, "@") {
		var canonical string
		err := s.db.QueryRowContext(ctx, `SELECT email FROM users WHERE login_name = ?`, email).Scan(&canonical)
		if err == nil {
			email = canonical
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	ip := audit.IPFromContext(ctx)
	ipCount, err := s.limiter.hit(ctx, "ip:"+ip)
	if err != nil {
		return nil, err
	}
	if ipCount > maxLoginIPAttempts {
		s.failedLogin(ctx, "", email, "ip_locked")
		return nil, ErrLocked
	}
	key := ip + "|" + email
	count, err := s.limiter.hit(ctx, key)
	if err != nil {
		return nil, err
	}
	var hash string
	u, err := scanUserWithHash(s.db.QueryRowContext(ctx, `SELECT `+userColumns+`, password_hash FROM users WHERE email = ?`, email), &hash)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	uid := ""
	if u != nil {
		uid = u.ID
	}
	if count > maxLoginFailures {
		s.failedLogin(ctx, uid, email, "locked")
		return nil, ErrLocked
	}
	select {
	case passwordChecks <- struct{}{}:
		defer func() { <-passwordChecks }()
	default:
		s.failedLogin(ctx, uid, email, "overloaded")
		return nil, ErrLocked
	}
	if u == nil || hash == "" {
		s.verify(dummyHash, password) // equalise timing
		s.failedLogin(ctx, uid, email, "invalid")
		return nil, ErrInvalidCredentials
	}
	if !s.verify(hash, password) || u.Status != StatusActive {
		s.failedLogin(ctx, uid, email, "invalid")
		return nil, ErrInvalidCredentials
	}
	if wordpressPasswordHash(hash) {
		upgraded, err := HashPassword(password)
		if err != nil {
			return nil, err
		}
		// Compare-and-swap prevents a concurrent reset from being overwritten.
		res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ? AND password_hash = ? AND status = 'active'`, upgraded, db.FormatTime(s.now()), u.ID, hash)
		if err != nil {
			return nil, fmt.Errorf("auth: upgrade imported password: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return nil, fmt.Errorf("auth: upgrade imported password result: %w", err)
		} else if n != 1 {
			return nil, ErrInvalidCredentials
		}
		s.record(ctx, "user.password_upgraded", u.ID, nil)
	}
	if err := s.limiter.reset(ctx, key); err != nil {
		return nil, err
	}
	return u, nil
}

func scanUserWithHash(row *sql.Row, hash *string) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Locale, &u.Status, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("auth: scan user: %w", err)
	}
	return &u, nil
}

// touchLogin records a successful login.
func (s *service) touchLogin(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, db.FormatTime(s.now()), id)
}

// BootstrapAdmin creates the first admin atomically, assigning the durable admin
// login alias. ErrSetupDone means an account already exists; it never resets it.
func BootstrapAdmin(ctx context.Context, d *sql.DB, a *audit.Writer, password string) (*User, error) {
	return newService(d, a).createFirstAdmin(ctx, "admin@localhost.invalid", "Administrator", password)
}
