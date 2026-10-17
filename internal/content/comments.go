package content

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

// ServiceComments is the kernel service name of the CommentService.
const ServiceComments = "content.comments"

// PermModerateComments lets a user approve, mark as spam, trash and delete comments; their own
// comments are approved at once.
const PermModerateComments = "comments.moderate"

// Comment statuses.
const (
	CommentPending  = "pending"
	CommentApproved = "approved"
	CommentSpam     = "spam"
	CommentTrash    = "trash"
)

// CommentStatuses are the comment statuses, in moderation order.
var CommentStatuses = []string{CommentPending, CommentApproved, CommentSpam, CommentTrash}

const (
	maxCommentName  = 100
	maxCommentEmail = 254
	maxCommentBody  = 5000
)

// Comment is a visitor comment. AuthorEmail is for moderators only: never render it publicly.
type Comment struct {
	ID           string `json:"id"`
	ContentID    string `json:"content_id"`
	ContentTitle string `json:"content_title,omitempty"` // filled by Comments
	ContentType  string `json:"content_type,omitempty"`  // filled by Comments
	ParentID     string `json:"parent_id"`
	AuthorName   string `json:"author_name"`
	AuthorEmail  string `json:"author_email"`
	UserID       string `json:"user_id"`
	Body         string `json:"body"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// CommentInput is a submitted comment.
type CommentInput struct {
	ContentID, ParentID, AuthorName, AuthorEmail, Body string
}

// CommentQuery filters the moderation list (newest first).
type CommentQuery struct {
	ContentID string `json:"content_id"`
	Status    string `json:"status"`
	Page      int    `json:"page"`
	PerPage   int    `json:"per_page"`
}

// CommentService is the comments API of the content module.
type CommentService interface {
	// SubmitComment stores a comment on a published item of a public type that supports
	// comments: pending, or approved when the user holds comments.moderate.
	SubmitComment(ctx context.Context, in CommentInput) (*Comment, error)
	// ApprovedComments lists the approved comments of an item, oldest first, at most limit.
	ApprovedComments(ctx context.Context, contentID string, limit int) ([]Comment, error)
	Comments(ctx context.Context, q CommentQuery) (Page[Comment], error)
	GetComment(ctx context.Context, id string) (*Comment, error)
	SetCommentStatus(ctx context.Context, id, status string) error
	DeleteComment(ctx context.Context, id string) error
	CommentCounts(ctx context.Context) (map[string]int, error)
}

var _ CommentService = (*service)(nil)

const commentColumns = `m.id, m.content_id, COALESCE(m.parent_id, ''), m.author_name, m.author_email, m.user_id, m.body,
	m.status, m.created_at, m.updated_at`

func scanComment(sc interface{ Scan(...any) error }, extra ...any) (*Comment, error) {
	var c Comment
	dest := append([]any{&c.ID, &c.ContentID, &c.ParentID, &c.AuthorName, &c.AuthorEmail, &c.UserID, &c.Body, &c.Status,
		&c.CreatedAt, &c.UpdatedAt}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *service) SubmitComment(ctx context.Context, in CommentInput) (*Comment, error) {
	in.AuthorName = strings.TrimSpace(in.AuthorName)
	in.AuthorEmail = strings.TrimSpace(in.AuthorEmail)
	in.Body = strings.TrimSpace(strings.ReplaceAll(in.Body, "\r\n", "\n"))
	if err := s.commentsOpen(ctx, in.ContentID); err != nil {
		return nil, err
	}
	errs := FieldErrors{}
	switch {
	case in.AuthorName == "":
		errs["author_name"] = "content.validation.comment_required"
	case utf8.RuneCountInString(in.AuthorName) > maxCommentName:
		errs["author_name"] = "content.validation.comment_too_long"
	}
	if a, err := mail.ParseAddress(in.AuthorEmail); err != nil || a.Address != in.AuthorEmail || len(in.AuthorEmail) > maxCommentEmail {
		errs["author_email"] = "content.validation.comment_email"
	}
	switch {
	case in.Body == "":
		errs["body"] = "content.validation.comment_required"
	case utf8.RuneCountInString(in.Body) > maxCommentBody:
		errs["body"] = "content.validation.comment_too_long"
	}
	if in.ParentID != "" {
		p, err := s.GetComment(ctx, in.ParentID)
		if err != nil || p.ContentID != in.ContentID || p.Status != CommentApproved || p.ParentID != "" {
			errs["parent_id"] = "content.validation.comment_parent"
		}
	}
	if len(errs) > 0 {
		return nil, invalid(errs)
	}
	now := s.nowString()
	c := &Comment{ID: db.NewID(), ContentID: in.ContentID, ParentID: in.ParentID, AuthorName: in.AuthorName,
		AuthorEmail: in.AuthorEmail, Body: in.Body, Status: CommentPending, CreatedAt: now, UpdatedAt: now}
	if u := auth.UserFromContext(ctx); u != nil {
		c.UserID = u.ID
		if auth.Can(ctx, PermModerateComments) {
			c.Status = CommentApproved
		}
	}
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO comments (id, content_id, parent_id, author_name, author_email, user_id, body,
			status, ip, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, c.ID, c.ContentID, nullable(c.ParentID),
			c.AuthorName, c.AuthorEmail, c.UserID, c.Body, c.Status, audit.IPFromContext(ctx), c.CreatedAt, c.UpdatedAt); err != nil {
			return fmt.Errorf("content: comment: %w", err)
		}
		s.audit.RecordTx(ctx, tx, "content.comment", "comment", c.ID, map[string]any{"content": c.ContentID, "status": c.Status})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.hooks != nil {
		s.hooks.Emit(ctx, "content.comment_submitted", *c)
	}
	return c, nil
}

// commentsOpen checks that id is a published item of a public type that supports comments.
func (s *service) commentsOpen(ctx context.Context, id string) error {
	closed := invalid(FieldErrors{"comment": "content.validation.comments_closed"})
	it, err := s.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return closed
	}
	if err != nil {
		return err
	}
	t, err := s.GetType(ctx, it.Type)
	if err != nil || it.Status != "published" || !t.Public || !slices.Contains(t.Supports, "comments") {
		return closed
	}
	return nil
}

func (s *service) ApprovedComments(ctx context.Context, contentID string, limit int) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+commentColumns+` FROM comments m
		WHERE m.content_id = ? AND m.status = 'approved' ORDER BY m.created_at, m.id LIMIT ?`, contentID, max(limit, 1))
	if err != nil {
		return nil, fmt.Errorf("content: comments of %s: %w", contentID, err)
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *service) Comments(ctx context.Context, q CommentQuery) (Page[Comment], error) {
	page := Page[Comment]{Items: []Comment{}, Page: max(q.Page, 1), PerPage: q.PerPage}
	if page.PerPage <= 0 {
		page.PerPage = defaultPerPage
	}
	page.PerPage = min(page.PerPage, maxPerPage)
	where, args := []string{"1 = 1"}, []any{}
	if q.Status != "" {
		if !slices.Contains(CommentStatuses, q.Status) {
			return page, invalid(FieldErrors{"status": "content.validation.comment_status"})
		}
		where, args = append(where, "m.status = ?"), append(args, q.Status)
	}
	if q.ContentID != "" {
		where, args = append(where, "m.content_id = ?"), append(args, q.ContentID)
	}
	cond := " WHERE " + strings.Join(where, " AND ")
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM comments m`+cond, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("content: count comments: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+commentColumns+`, c.title, c.type FROM comments m JOIN content c ON c.id = m.content_id`+
		cond+` ORDER BY m.created_at DESC, m.id DESC LIMIT ? OFFSET ?`, append(args, page.PerPage, (page.Page-1)*page.PerPage)...)
	if err != nil {
		return page, fmt.Errorf("content: comments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var title, typ string
		c, err := scanComment(rows, &title, &typ)
		if err != nil {
			return page, err
		}
		c.ContentTitle, c.ContentType = title, typ
		page.Items = append(page.Items, *c)
	}
	return page, rows.Err()
}

func (s *service) GetComment(ctx context.Context, id string) (*Comment, error) {
	c, err := scanComment(s.db.QueryRowContext(ctx, `SELECT `+commentColumns+` FROM comments m WHERE m.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *service) SetCommentStatus(ctx context.Context, id, status string) error {
	if !slices.Contains(CommentStatuses, status) {
		return invalid(FieldErrors{"status": "content.validation.comment_status"})
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE comments SET status = ?, updated_at = ? WHERE id = ?`, status, s.nowString(), id)
		if err != nil {
			return fmt.Errorf("content: comment status: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		s.audit.RecordTx(ctx, tx, "content.comment_status", "comment", id, map[string]any{"status": status})
		return nil
	})
}

func (s *service) DeleteComment(ctx context.Context, id string) error {
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM comments WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("content: delete comment: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		s.audit.RecordTx(ctx, tx, "content.comment_delete", "comment", id, nil)
		return nil
	})
}

func (s *service) CommentCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, count(*) FROM comments GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("content: comment counts: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}
