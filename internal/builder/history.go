package builder

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
)

const (
	RevisionKindSave     = "save"
	RevisionKindPublish  = "publish"
	RevisionKindRestore  = "restore"
	RevisionKindReset    = "reset"
	defaultRevisionsKeep = 25
)

// LayoutRevision is one stored page-builder layout snapshot.
type LayoutRevision struct {
	ID           string   `json:"id"`
	ContentID    string   `json:"content_id"`
	Kind         string   `json:"kind"`
	AuthorID     string   `json:"author_id"`
	CreatedAt    string   `json:"created_at"`
	SectionCount int      `json:"section_count"`
	Summary      string   `json:"summary"`
	Document     Document `json:"document"`
}

func revisionSummary(doc Document) (int, string) {
	summary := ""
	for _, section := range doc.Sections {
		if section.Hidden {
			continue
		}
		if title := strings.TrimSpace(section.Title); title != "" {
			summary = title
			break
		}
	}
	if len(summary) > 120 {
		summary = summary[:120]
	}
	return len(doc.Sections), summary
}

func (s *service) revisionsKeep(ctx context.Context) int {
	n := defaultRevisionsKeep
	if s.d.Settings != nil {
		var v int
		if s.d.Settings.Get(ctx, "builder.revisions_keep", &v) == nil && v > 0 {
			n = v
		}
	}
	return n
}

func (s *service) addRevision(ctx context.Context, tx *sql.Tx, contentID, kind string, doc Document) error {
	if err := Validate(doc); err != nil {
		return err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("builder: encode revision: %w", err)
	}
	author := ""
	if u := auth.UserFromContext(ctx); u != nil {
		author = u.ID
	}
	count, summary := revisionSummary(doc)
	id := db.NewID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO builder_revisions(content_id,id,kind,document_json,author_id,created_at,section_count,summary)
		VALUES(?,?,?,?,?,?,?,?)`, contentID, id, kind, string(raw), author, db.Now(), count, summary); err != nil {
		return fmt.Errorf("builder: add revision: %w", err)
	}
	keep := s.revisionsKeep(ctx)
	if _, err := tx.ExecContext(ctx, `DELETE FROM builder_revisions WHERE content_id=? AND id NOT IN
		(SELECT id FROM builder_revisions WHERE content_id=? ORDER BY id DESC LIMIT ?)`, contentID, contentID, keep); err != nil {
		return fmt.Errorf("builder: prune revisions: %w", err)
	}
	return nil
}

// ListRevisions returns layout history for a page, newest first.
func (s *service) ListRevisions(ctx context.Context, id string) ([]LayoutRevision, error) {
	if _, err := s.editable(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.d.DB.QueryContext(ctx, `SELECT id,content_id,kind,author_id,created_at,section_count,summary,document_json
		FROM builder_revisions WHERE content_id=? ORDER BY id DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("builder: list revisions: %w", err)
	}
	defer rows.Close()
	out := []LayoutRevision{}
	for rows.Next() {
		var r LayoutRevision
		var raw string
		if err := rows.Scan(&r.ID, &r.ContentID, &r.Kind, &r.AuthorID, &r.CreatedAt, &r.SectionCount, &r.Summary, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &r.Document); err != nil {
			return nil, fmt.Errorf("builder: decode revision %s: %w", r.ID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRevision returns one layout revision for a page the caller may edit.
func (s *service) GetRevision(ctx context.Context, id, revisionID string) (*LayoutRevision, error) {
	if _, err := s.editable(ctx, id); err != nil {
		return nil, err
	}
	var r LayoutRevision
	var raw string
	err := s.d.DB.QueryRowContext(ctx, `SELECT id,content_id,kind,author_id,created_at,section_count,summary,document_json
		FROM builder_revisions WHERE content_id=? AND id=?`, id, revisionID).Scan(
		&r.ID, &r.ContentID, &r.Kind, &r.AuthorID, &r.CreatedAt, &r.SectionCount, &r.Summary, &raw)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("builder: get revision: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &r.Document); err != nil {
		return nil, fmt.Errorf("builder: decode revision: %w", err)
	}
	return &r, nil
}

// RestoreRevision snapshots the current draft, then replaces it with the
// chosen revision. Public HTML is unchanged until the next publish. expected
// is the current draft updated_at token (required for conflict detection).
func (s *service) RestoreRevision(ctx context.Context, id, revisionID, expected string) (*Draft, error) {
	if _, err := s.editable(ctx, id); err != nil {
		return nil, err
	}
	if expected == "" {
		return nil, ErrConflict
	}
	rev, err := s.GetRevision(ctx, id, revisionID)
	if err != nil {
		return nil, err
	}
	if err := Validate(rev.Document); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(rev.Document)
	if err != nil {
		return nil, err
	}
	out := &Draft{Document: rev.Document, UpdatedAt: db.NewID()}
	err = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		var owner, status, typ, token, currentJSON, published, base string
		var public bool
		if err := tx.QueryRowContext(ctx, `SELECT c.author_id,c.status,c.type,t.public FROM content c
			JOIN content_types t ON t.slug=c.type WHERE c.id=?`, id).Scan(&owner, &status, &typ, &public); err != nil {
			if err == sql.ErrNoRows {
				return ErrNotFound
			}
			return err
		}
		u := auth.UserFromContext(ctx)
		if !public || status == "trash" || typ != "page" || (u != nil && (u.Role == "author" || u.Role == "contributor") && owner != u.ID) {
			return ErrForbidden
		}
		err := tx.QueryRowContext(ctx, `SELECT document_json,published_document_json,base_updated_at,updated_at
			FROM builder_documents WHERE content_id=?`, id).Scan(&currentJSON, &published, &base, &token)
		if err == sql.ErrNoRows {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if token != expected {
			return ErrConflict
		}
		var current Document
		if err := json.Unmarshal([]byte(currentJSON), &current); err != nil {
			return fmt.Errorf("builder: decode current draft: %w", err)
		}
		if err := s.addRevision(ctx, tx, id, RevisionKindRestore, current); err != nil {
			return err
		}
		contentToken, err := content.EditTokenInTx(ctx, tx, id)
		if err != nil {
			return err
		}
		out.BaseUpdatedAt = base
		out.ContentToken = contentToken
		out.HasPublished = published != "null"
		out.ContentChanged = base != contentToken
		res, err := tx.ExecContext(ctx, `UPDATE builder_documents SET document_json=?,updated_at=? WHERE content_id=? AND updated_at=?`,
			string(raw), out.UpdatedAt, id, expected)
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
		if s.d.Audit != nil {
			s.d.Audit.RecordTx(ctx, tx, "builder.restore", "content", id, map[string]any{
				"revision_id": revisionID, "kind": rev.Kind,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
