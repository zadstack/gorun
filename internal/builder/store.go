package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

var ErrConflict = errors.New("builder: document changed; reload before saving")
var ErrForbidden = content.ErrForbidden
var ErrNotFound = content.ErrNotFound

type Draft struct {
	ContentToken   string   `json:"content_token"`
	HasPublished   bool     `json:"has_published"`
	ContentChanged bool     `json:"content_changed"`
	Document       Document `json:"document"`
	BaseUpdatedAt  string   `json:"base_updated_at" format:"-"`
	UpdatedAt      string   `json:"updated_at" format:"-"`
}
type service struct{ d *kernel.Deps }

func (s *service) content() (content.Service, error) {
	cs, ok := kernel.Get[content.Service](s.d, kernel.ServiceContent)
	if !ok {
		return nil, errors.New("builder: content service unavailable")
	}
	return cs, nil
}
func (s *service) editable(ctx context.Context, id string) (*content.Item, error) {
	if !auth.Can(ctx, "content.edit") {
		return nil, ErrForbidden
	}
	cs, err := s.content()
	if err != nil {
		return nil, err
	}
	it, err := cs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if it.Type != "page" || it.Status == "trash" {
		return nil, ErrForbidden
	}
	typ, err := cs.GetType(ctx, it.Type)
	if err != nil {
		return nil, err
	}
	if !typ.Public {
		return nil, ErrForbidden
	}
	u := auth.UserFromContext(ctx)
	if u != nil && (u.Role == "author" || u.Role == "contributor") && it.AuthorID != u.ID {
		return nil, ErrForbidden
	}
	return it, nil
}
func (s *service) Load(ctx context.Context, id string) (*Draft, error) {
	it, err := s.editable(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &Draft{ContentToken: content.EditToken(it), Document: Document{Version: 1, Sections: []Section{}}, BaseUpdatedAt: content.EditToken(it), UpdatedAt: content.EditToken(it)}
	var raw, published string
	err = s.d.DB.QueryRowContext(ctx, `SELECT document_json,published_document_json,base_updated_at,updated_at FROM builder_documents WHERE content_id=?`, id).Scan(&raw, &published, &out.BaseUpdatedAt, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("builder: load: %w", err)
	}
	out.HasPublished = published != "null"
	out.ContentChanged = out.BaseUpdatedAt != content.EditToken(it)
	if err = json.Unmarshal([]byte(raw), &out.Document); err != nil {
		return nil, fmt.Errorf("builder: decode: %w", err)
	}
	if err = Validate(out.Document); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *service) Save(ctx context.Context, id string, doc Document, expected string) (*Draft, error) {
	it, err := s.editable(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = Validate(doc); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	out := &Draft{Document: doc, BaseUpdatedAt: content.EditToken(it), UpdatedAt: db.NewID()}
	err = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		var owner, status, typ, fresh string
		var public bool
		if err := tx.QueryRowContext(ctx, `SELECT c.author_id,c.status,c.type,c.updated_at,t.public FROM content c JOIN content_types t ON t.slug=c.type WHERE c.id=?`, id).Scan(&owner, &status, &typ, &fresh, &public); err != nil {
			return err
		}
		u := auth.UserFromContext(ctx)
		if !public || status == "trash" || typ != "page" || (u != nil && (u.Role == "author" || u.Role == "contributor") && owner != u.ID) {
			return ErrForbidden
		}
		var oldToken, base, published string
		err := tx.QueryRowContext(ctx, `SELECT updated_at,base_updated_at,published_document_json FROM builder_documents WHERE content_id=?`, id).Scan(&oldToken, &base, &published)
		if err == sql.ErrNoRows {
			if expected != content.EditToken(it) {
				return ErrConflict
			}
			freshToken, err := content.EditTokenInTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if freshToken != expected {
				return ErrConflict
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO builder_documents(content_id,document_json,base_updated_at,updated_at) VALUES(?,?,?,?)`, id, string(raw), out.BaseUpdatedAt, out.UpdatedAt)
			return err
		}
		if err != nil {
			return err
		}
		if expected != oldToken {
			return ErrConflict
		}
		out.BaseUpdatedAt = base
		out.HasPublished = published != "null"
		out.ContentChanged = base != content.EditToken(it)
		_, err = tx.ExecContext(ctx, `UPDATE builder_documents SET document_json=?,updated_at=? WHERE content_id=? AND updated_at=?`, string(raw), out.UpdatedAt, id, oldToken)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (s *service) Publish(ctx context.Context, id, expected string) error {
	if !auth.Can(ctx, "content.publish") {
		return ErrForbidden
	}
	it, err := s.editable(ctx, id)
	if err != nil {
		return err
	}
	draft, err := s.Load(ctx, id)
	if err != nil {
		return err
	}
	if draft.UpdatedAt != expected {
		return ErrConflict
	}
	if err := ValidatePublication(draft.Document); err != nil {
		return err
	}
	body, err := Render(draft.Document)
	if err != nil {
		return err
	}
	cs, err := s.content()
	if err != nil {
		return err
	}
	atomic, ok := cs.(content.AtomicUpdater)
	if !ok {
		return errors.New("builder: atomic content updates unavailable")
	}
	in := content.InputFromItem(it)
	in.Body = body
	in.Template = "builder"
	in.Status = "published"
	in.PublishAt = ""
	_, err = atomic.UpdateAtomic(ctx, id, in, draft.BaseUpdatedAt, func(tx *sql.Tx, saved *content.Item) error {
		result, err := tx.ExecContext(ctx, `UPDATE builder_documents SET published_document_json=document_json,base_updated_at=?,updated_at=? WHERE content_id=? AND updated_at=?`, content.EditToken(saved), db.NewID(), id, expected)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
	if errors.Is(err, content.ErrConflict) {
		return ErrConflict
	}
	return err
}

// Reset restores published sections or discards a legacy draft without changing the page.
func (s *service) Reset(ctx context.Context, id, expected string) error {
	return s.ResetGuarded(ctx, id, expected, nil)
}

func (s *service) ResetGuarded(ctx context.Context, id, expected string, expectedContent *string) error {
	if _, err := s.editable(ctx, id); err != nil {
		return err
	}
	return db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		var owner, status, typ string
		var public bool
		if err := tx.QueryRowContext(ctx, `SELECT c.author_id,c.status,c.type,t.public FROM content c JOIN content_types t ON t.slug=c.type WHERE c.id=?`, id).Scan(&owner, &status, &typ, &public); err != nil {
			return err
		}
		u := auth.UserFromContext(ctx)
		if !public || status == "trash" || typ != "page" || (u != nil && (u.Role == "author" || u.Role == "contributor") && owner != u.ID) {
			return ErrForbidden
		}
		if expectedContent != nil {
			current, err := content.EditTokenInTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if *expectedContent == "" || current != *expectedContent {
				return ErrConflict
			}
		}
		var published, token string
		err := tx.QueryRowContext(ctx, `SELECT published_document_json,updated_at FROM builder_documents WHERE content_id=?`, id).Scan(&published, &token)
		if err == sql.ErrNoRows {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if token != expected {
			return ErrConflict
		}
		var res sql.Result
		if published == "null" {
			res, err = tx.ExecContext(ctx, `DELETE FROM builder_documents WHERE content_id=? AND updated_at=?`, id, expected)
		} else {
			var document Document
			if err = json.Unmarshal([]byte(published), &document); err != nil {
				return err
			}
			if err = Validate(document); err != nil {
				return err
			}
			base, err := content.EditTokenInTx(ctx, tx, id)
			if err != nil {
				return err
			}
			res, err = tx.ExecContext(ctx, `UPDATE builder_documents SET document_json=published_document_json,base_updated_at=?,updated_at=? WHERE content_id=? AND updated_at=?`, base, db.NewID(), id, expected)
			if err != nil {
				return err
			}
		}
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
