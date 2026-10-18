package site

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Submission statuses and delivery states (form_submissions.status, .delivery).
const (
	statusNew  = "new"
	statusRead = "read"
	statusSpam = "spam"

	deliveryPending = "pending"
	deliverySent    = "sent"
	deliveryFailed  = "failed"
	deliveryOff     = "off"

	submissionsPerPage = 50
)

// Submission is one stored form_submissions row: the submission as posted
// (SubmittedAt is the row's created_at) plus its inbox state.
type Submission struct {
	FormSubmission
	Source                          formSource
	Status, Delivery, DeliveryError string
}

// SizeText is the attachment size for display.
func (s Submission) SizeText() string { return humanSize(s.AttachmentSize) }

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

const submissionCols = `id, form, name, email, message, page, ip, created_at, status, delivery, delivery_error,
	attachment_name, attachment_type, attachment_size`

func scanSubmission(sc interface{ Scan(...any) error }) (Submission, error) {
	var s Submission
	err := sc.Scan(&s.ID, &s.Form, &s.Name, &s.Email, &s.Message, &s.Page, &s.IP, &s.SubmittedAt, &s.Status, &s.Delivery,
		&s.DeliveryError, &s.AttachmentName, &s.AttachmentType, &s.AttachmentSize)
	return s, err
}

// setDelivery records the mail outcome; delivered mirrors delivery = sent.
func (h *handlers) setDelivery(ctx context.Context, id, state, detail string) {
	if id == "" {
		return
	}
	if _, err := h.d.DB.ExecContext(ctx, `UPDATE form_submissions SET delivery = ?, delivery_error = ?, delivered = ? WHERE id = ?`,
		state, detail, state == deliverySent, id); err != nil {
		h.d.Log.Error("site: record submission delivery", "id", id, "err", err)
	}
}

// inboxWhere filters a view: inbox (everything but spam), new, spam.
func inboxWhere(view string) string {
	switch view {
	case statusNew:
		return `status = 'new'`
	case statusSpam:
		return `status = 'spam'`
	}
	return `status <> 'spam'`
}

func normalizeView(v string) string {
	if v == statusNew || v == statusSpam {
		return v
	}
	return "inbox"
}

// countSubmissions returns the size of every view in one scan.
func (h *handlers) countSubmissions(ctx context.Context) (map[string]int, error) {
	var inbox, unread, spam int
	err := h.d.DB.QueryRowContext(ctx, `SELECT coalesce(sum(status <> 'spam'), 0), coalesce(sum(status = 'new'), 0),
		coalesce(sum(status = 'spam'), 0) FROM form_submissions`).Scan(&inbox, &unread, &spam)
	return map[string]int{"inbox": inbox, statusNew: unread, statusSpam: spam}, err
}

// eachSubmission calls fn for a view, newest first; perPage 0 means all rows.
func (h *handlers) eachSubmission(ctx context.Context, view string, page, perPage int, fn func(Submission) error) error {
	q := `SELECT ` + submissionCols + ` FROM form_submissions WHERE ` + inboxWhere(view) + ` ORDER BY created_at DESC, id DESC`
	args := []any{}
	if perPage > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, perPage, (max(page, 1)-1)*perPage)
	}
	rows, err := h.d.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return err
		}
		if err := fn(s); err != nil {
			return err
		}
	}
	return rows.Err()
}

var errNoSubmission = errors.New("site: submission not found")

func (h *handlers) getSubmission(ctx context.Context, id string) (Submission, error) {
	s, err := scanSubmission(h.d.DB.QueryRowContext(ctx, `SELECT `+submissionCols+` FROM form_submissions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, errNoSubmission
	}
	return s, err
}

func (h *handlers) setStatus(ctx context.Context, id, status string) error {
	if status != statusNew && status != statusRead && status != statusSpam {
		return fmt.Errorf("site: invalid status %q", status)
	}
	res, err := h.d.DB.ExecContext(ctx, `UPDATE form_submissions SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSubmission
	}
	return nil
}

// deleteSubmission removes the row, then its attachment file.
func (h *handlers) deleteSubmission(ctx context.Context, id string) error {
	res, err := h.d.DB.ExecContext(ctx, `DELETE FROM form_submissions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSubmission
	}
	return h.removeUpload(id)
}

// storeContactRequest atomically stores the submission and its replay key.
func (h *handlers) storeContactRequest(ctx context.Context, s FormSubmission, key string) (bool, error) {
	tx, err := h.d.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Ten-minute replay window; a later intentional identical request is allowed.
	if _, err = tx.ExecContext(ctx, `DELETE FROM form_request_keys WHERE request_key = ? AND submission_id IN (SELECT id FROM form_submissions WHERE created_at < ?)`, key, time.Now().UTC().Add(-10*time.Minute).Format(time.RFC3339)); err != nil {
		return false, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, "SELECT submission_id FROM form_request_keys WHERE request_key = ?", key).Scan(&existing)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO form_submissions (id, form, name, email, message, page, ip, created_at, attachment_name, attachment_type, attachment_size) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.ID, s.Form, s.Name, s.Email, s.Message, s.Page, s.IP, s.SubmittedAt, s.AttachmentName, s.AttachmentType, s.AttachmentSize)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO form_request_keys (request_key, submission_id) VALUES (?, ?)", key, s.ID)
	if err != nil {
		return false, err
	}
	return false, tx.Commit()
}
