package content

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

type PageAccess struct {
	ContentID    string `json:"content_id"`
	Mode         string `json:"mode"`
	Message      string `json:"message"`
	Revision     int64  `json:"revision"`
	PasswordHash string `json:"-"`
}
type PageAccessInput struct {
	Mode, Message, Password string
	ClearPassword           bool
}
type PageAccessService interface {
	PageAccess(context.Context, string) (PageAccess, error)
	SavePageAccess(context.Context, string, PageAccessInput, int64) (PageAccess, error)
}

func (s *service) PageAccess(ctx context.Context, id string) (PageAccess, error) {
	return readPageAccess(ctx, s.db, id)
}
func readPageAccess(ctx context.Context, runner sqlRunner, id string) (PageAccess, error) {
	p := PageAccess{ContentID: id, Mode: "public"}
	err := runner.QueryRowContext(ctx, `SELECT mode,message,password_hash,revision FROM content_page_access WHERE content_id=?`, id).Scan(&p.Mode, &p.Message, &p.PasswordHash, &p.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	return p, err
}

// PubliclyReadable is separate from staff read authorization. A visitor's
// unlock grants the direct page, never searchable excerpts or shared caches.
func PubliclyReadable(ctx context.Context, svc Service, id string) bool {
	for _, excluded := range publicExclusions(ctx) {
		if strings.EqualFold(excluded, id) {
			return false
		}
	}
	policies, ok := svc.(PageAccessService)
	if !ok {
		return true
	}
	p, err := policies.PageAccess(ctx, id)
	return err == nil && p.Mode == "public"
}

func (s *service) SavePageAccess(ctx context.Context, id string, in PageAccessInput, expected int64) (PageAccess, error) {
	if !auth.Can(ctx, "settings.manage") {
		return PageAccess{}, ErrForbidden
	}
	if expected < 0 || expected == math.MaxInt64 {
		return PageAccess{}, ErrConflict
	}
	if (in.Mode != "public" && in.Mode != "blocked" && in.Mode != "password") || utf8.RuneCountInString(in.Message) > 2000 || len(in.Message) > 8000 || strings.ContainsRune(in.Message, 0) || len(in.Password) > 256 || in.Password != "" && len(in.Password) < 12 {
		return PageAccess{}, invalid(FieldErrors{"access": "content.validation.page_access"})
	}
	var newHash string
	if in.Password != "" && !in.ClearPassword {
		var err error
		newHash, err = auth.HashPassword(in.Password)
		if err != nil {
			return PageAccess{}, err
		}
	}
	var out PageAccess
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var public bool
		err := tx.QueryRowContext(ctx, `SELECT t.public FROM content c JOIN content_types t ON t.slug=c.type WHERE c.id=? AND c.status!='trash'`, id).Scan(&public)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !public {
			return ErrForbidden
		}
		old, err := readPageAccess(ctx, tx, id)
		if err != nil {
			return err
		}
		if old.Revision != expected {
			return ErrConflict
		}
		out = PageAccess{ContentID: id, Mode: in.Mode, Message: in.Message, Revision: old.Revision + 1, PasswordHash: old.PasswordHash}
		if in.ClearPassword {
			out.PasswordHash = ""
		} else if newHash != "" {
			out.PasswordHash = newHash
		}
		if out.Mode == "password" && out.PasswordHash == "" {
			return invalid(FieldErrors{"password": "content.validation.required"})
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO content_page_access(content_id,mode,message,password_hash,revision,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(content_id) DO UPDATE SET mode=excluded.mode,message=excluded.message,password_hash=excluded.password_hash,revision=excluded.revision,updated_at=excluded.updated_at`, id, out.Mode, out.Message, out.PasswordHash, out.Revision, db.Now())
		if err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "content.page_access", "content", id, map[string]any{"mode": out.Mode, "revision": out.Revision})
		return nil
	})
	return out, err
}

// WithPublicExclusions adds trusted host restrictions before public pagination.
type publicExclusionsKey struct{}

func WithPublicExclusions(ctx context.Context, ids []string) context.Context {
	normalized := make([]string, len(ids))
	for i, id := range ids {
		normalized[i] = strings.ToUpper(id)
	}
	return context.WithValue(ctx, publicExclusionsKey{}, normalized)
}
func publicExclusions(ctx context.Context) []string {
	ids, _ := ctx.Value(publicExclusionsKey{}).([]string)
	return ids
}

// WithDirectPageRead is a request-local grant after the public controller has
// enforced the page gate. It never widens listing/query access.
type directPageReadKey struct{}

func WithDirectPageRead(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, directPageReadKey{}, id)
}
func DirectPageReadAllowed(ctx context.Context, id string) bool {
	granted, _ := ctx.Value(directPageReadKey{}).(string)
	return id != "" && granted == id
}
