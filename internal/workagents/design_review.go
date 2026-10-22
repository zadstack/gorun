package workagents

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

const designReviewJobKind = "workagents.design_review"
const reviewContextLimit = 12000
const reviewTimeout = 45 * time.Second
const reviewMaxAttempts = 3

var errReviewForbidden = errors.New("design review permission denied")
var errReviewApproval = errors.New("review requires explicit approval of the current context and destination")
var reviewSecrets = regexp.MustCompile(`(?i)(bearer\s+|(?:api[_-]?key|token|password|secret)["']?\s*[:=]\s*["']?)[^\s<"']+`)
var reviewCredentialTokens = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----|\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[A-Z0-9]{16})\b`)
var reviewThemeSlug = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

type DesignReviewPreview struct {
	ID             string `json:"id"`
	PageID         string `json:"page_id"`
	AgentID        string `json:"agent_id"`
	Context        string `json:"context"`
	Destination    string `json:"destination"`
	ConfigRevision string `json:"config_revision"`
	CreatedAt      string `json:"created_at"`
}
type DesignReviewEnqueue struct {
	PreviewID  string `json:"preview_id"`
	RequestKey string `json:"request_key"`
	Approved   bool   `json:"approved"`
}
type DesignReviewFinding struct {
	Severity   string `json:"severity"`
	Location   string `json:"location"`
	Suggestion string `json:"suggestion"`
	Evidence   string `json:"evidence"`
}
type DesignReview struct {
	ID             string                `json:"id"`
	RequestID      string                `json:"request_id"`
	Destination    string                `json:"destination"`
	ConfigRevision string                `json:"config_revision"`
	AgentName      string                `json:"agent_name"`
	PageID         string                `json:"page_id"`
	AgentID        string                `json:"agent_id"`
	Status         string                `json:"status"`
	ErrorCode      string                `json:"error_code"`
	CreatedAt      string                `json:"created_at"`
	UpdatedAt      string                `json:"updated_at"`
	Attempts       int                   `json:"attempts"`
	Findings       []DesignReviewFinding `json:"findings"`
}

func reviewAllowed(ctx context.Context) bool {
	return owner(ctx) != "" && auth.Can(ctx, "workagents.run") && auth.Can(ctx, "content.view")
}
func reviewReadable(ctx context.Context) bool {
	return owner(ctx) != "" && auth.Can(ctx, "content.view") && (auth.Can(ctx, "workagents.read") || auth.Can(ctx, "workagents.run"))
}
func reviewHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func (s *service) reviewContext(ctx context.Context, pageID string) (string, string, error) {
	if !reviewAllowed(ctx) {
		return "", "", errReviewForbidden
	}
	svc, ok := kernel.Get[content.Service](s.d, kernel.ServiceContent)
	if !ok {
		return "", "", errUnavailable
	}
	page, err := svc.Get(ctx, pageID)
	if err != nil {
		return "", "", errNotFound
	}
	if page.Type != "page" || page.Status == "trash" {
		return "", "", errInvalid
	}
	// Only the selected page's visible copy and presentation metadata are eligible;
	// fields, SEO, account records, settings, media URLs and credentials are excluded.
	text := reviewVisibleText(page.Title + "\n" + page.Body)
	text = reviewSecrets.ReplaceAllString(text, "[REDACTED]")
	text = reviewCredentialTokens.ReplaceAllString(text, "[REDACTED]")
	if len(text) > reviewContextLimit {
		text = text[:reviewContextLimit]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	themeSlug := "unavailable"
	if ts, ok := kernel.Get[theme.Service](s.d, kernel.ServiceTheme); ok {
		slug := ts.Active(ctx).Slug
		if reviewThemeSlug.MatchString(slug) {
			themeSlug = slug
		}
	}
	preview := fmt.Sprintf("Page copy (untrusted):\n%s\nLocale: %s\nTemplate: %s\nActive theme identifier: %s (rendering unverified)", text, page.Locale, page.Template, themeSlug)
	return preview, reviewHash(preview + page.UpdatedAt), nil
}
func (s *service) previewDesignReview(ctx context.Context, pageID, agentID string) (DesignReviewPreview, error) {
	p := DesignReviewPreview{ID: db.NewID(), PageID: pageID, AgentID: agentID, CreatedAt: db.FormatTime(s.now())}
	text, snapshot, err := s.reviewContext(ctx, pageID)
	if err != nil {
		return p, err
	}
	a, err := s.agent(ctx, agentID)
	if err != nil || a.OwnerID != owner(ctx) || !a.Enabled || a.ExecutionKind != "text" || len(a.UnsupportedTools) > 0 {
		return p, errUnavailable
	}
	if s.complete == nil {
		return p, errUnavailable
	}
	p.Destination, p.ConfigRevision, err = admin.WorkAgentReviewBinding(ctx, s.d, a.ConnectionID, a.Model)
	if err != nil {
		return p, errUnavailable
	}
	p.Context = text
	_, err = s.d.DB.ExecContext(ctx, `INSERT INTO workagents_review_previews(id,owner_id,page_id,agent_id,context,snapshot,destination,config_revision,created_at)VALUES(?,?,?,?,?,?,?,?,?)`, p.ID, owner(ctx), pageID, agentID, text, snapshot, p.Destination, p.ConfigRevision, p.CreatedAt)
	return p, err
}
func (s *service) reviewPreview(ctx context.Context, id string) (DesignReviewPreview, string, error) {
	var p DesignReviewPreview
	var snapshot string
	err := s.d.DB.QueryRowContext(ctx, `SELECT id,page_id,agent_id,context,snapshot,destination,config_revision,created_at FROM workagents_review_previews WHERE id=? AND owner_id=?`, id, owner(ctx)).Scan(&p.ID, &p.PageID, &p.AgentID, &p.Context, &snapshot, &p.Destination, &p.ConfigRevision, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = errNotFound
	}
	return p, snapshot, err
}
func (s *service) validateReviewPreview(ctx context.Context, p DesignReviewPreview, snapshot string) (Agent, error) {
	_, current, err := s.reviewContext(ctx, p.PageID)
	if err != nil {
		return Agent{}, err
	}
	a, err := s.agent(ctx, p.AgentID)
	if err != nil || a.OwnerID != owner(ctx) || !a.Enabled || a.ExecutionKind != "text" || len(a.UnsupportedTools) > 0 {
		return a, errUnavailable
	}
	destination, revision, err := admin.WorkAgentReviewBinding(ctx, s.d, a.ConnectionID, a.Model)
	if err != nil {
		return a, errUnavailable
	}
	if current != snapshot || destination != p.Destination || revision != p.ConfigRevision {
		return a, errReviewApproval
	}
	return a, nil
}
func (s *service) enqueueDesignReview(ctx context.Context, in DesignReviewEnqueue) (DesignReview, error) {
	if !reviewAllowed(ctx) {
		return DesignReview{}, errReviewForbidden
	}
	if !in.Approved {
		return DesignReview{}, errReviewApproval
	}
	if len(in.RequestKey) < 1 || len(in.RequestKey) > 160 {
		return DesignReview{}, errInvalid
	}
	p, snapshot, err := s.reviewPreview(ctx, in.PreviewID)
	if err != nil {
		return DesignReview{}, err
	}
	if _, err = s.validateReviewPreview(ctx, p, snapshot); err != nil {
		return DesignReview{}, err
	}
	id := db.NewID()
	now := db.FormatTime(s.now())
	requestID := httpx.RequestIDFromContext(ctx)
	if requestID == "" {
		requestID = db.NewID()
	}
	err = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		var oldPreview string
		e := tx.QueryRowContext(ctx, `SELECT id,preview_id FROM workagents_design_reviews WHERE owner_id=? AND request_key=?`, owner(ctx), in.RequestKey).Scan(&id, &oldPreview)
		if e == nil {
			if oldPreview != p.ID {
				return errConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var pending int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM workagents_design_reviews WHERE owner_id=? AND status IN ('queued','running')`, owner(ctx)).Scan(&pending); e != nil {
			return e
		}
		if pending >= 5 {
			return errBusy
		}
		jobID, e := s.d.Jobs.EnqueueTx(ctx, tx, designReviewJobKind, map[string]any{"review_id": id, "attempt": 1}, jobs.Idempotency("design-review:"+id))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO workagents_design_reviews(id,owner_id,preview_id,request_key,created_at,updated_at,request_id,job_id)VALUES(?,?,?,?,?,?,?,?)`, id, owner(ctx), p.ID, in.RequestKey, now, now, requestID, jobID)
		return e
	})
	if err != nil {
		return DesignReview{}, err
	}
	s.reviewAudit(ctx, "approved", id)
	return s.designReview(ctx, id)
}

const reviewSelect = `SELECT r.id,r.request_id,p.destination,p.config_revision,COALESCE((SELECT name FROM workagents_agents WHERE id=p.agent_id),''),p.page_id,p.agent_id,r.status,r.error_code,r.created_at,r.updated_at,r.attempts,r.findings FROM workagents_design_reviews r JOIN workagents_review_previews p ON p.id=r.preview_id`

func scanDesignReview(row interface{ Scan(...any) error }) (DesignReview, error) {
	var r DesignReview
	var findings string
	err := row.Scan(&r.ID, &r.RequestID, &r.Destination, &r.ConfigRevision, &r.AgentName, &r.PageID, &r.AgentID, &r.Status, &r.ErrorCode, &r.CreatedAt, &r.UpdatedAt, &r.Attempts, &findings)
	if errors.Is(err, sql.ErrNoRows) {
		return r, errNotFound
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(findings), &r.Findings)
	return r, err
}
func (s *service) designReview(ctx context.Context, id string) (DesignReview, error) {
	if !reviewReadable(ctx) {
		return DesignReview{}, errReviewForbidden
	}
	return scanDesignReview(s.d.DB.QueryRowContext(ctx, reviewSelect+` WHERE r.id=? AND r.owner_id=?`, id, owner(ctx)))
}
func (s *service) designReviews(ctx context.Context) ([]DesignReview, error) {
	if !reviewReadable(ctx) {
		return nil, errReviewForbidden
	}
	rows, err := s.d.DB.QueryContext(ctx, reviewSelect+` WHERE r.owner_id=? ORDER BY r.created_at DESC LIMIT 100`, owner(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DesignReview{}
	for rows.Next() {
		r, err := scanDesignReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *service) cancelDesignReview(ctx context.Context, id string) (DesignReview, error) {
	if !reviewAllowed(ctx) {
		return DesignReview{}, errReviewForbidden
	}
	if _, err := s.designReview(ctx, id); err != nil {
		return DesignReview{}, err
	}
	res, err := s.d.DB.ExecContext(ctx, `UPDATE workagents_design_reviews SET status='cancelled',updated_at=? WHERE id=? AND owner_id=? AND status IN ('queued','running')`, db.FormatTime(s.now()), id, owner(ctx))
	if err != nil {
		return DesignReview{}, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return DesignReview{}, err
	}
	if changed != 1 {
		return DesignReview{}, errConflict
	}
	s.reviewAudit(ctx, "cancelled", id)
	return s.designReview(ctx, id)
}
func (s *service) retryDesignReview(ctx context.Context, id string) (DesignReview, error) {
	if !reviewAllowed(ctx) {
		return DesignReview{}, errReviewForbidden
	}
	r, err := s.designReview(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Status != "failed" || r.Attempts >= reviewMaxAttempts {
		return r, errConflict
	}
	var previewID string
	if err = s.d.DB.QueryRowContext(ctx, `SELECT preview_id FROM workagents_design_reviews WHERE id=?`, id).Scan(&previewID); err != nil {
		return r, err
	}
	p, snapshot, err := s.reviewPreview(ctx, previewID)
	if err != nil {
		return r, err
	}
	if _, err = s.validateReviewPreview(ctx, p, snapshot); err != nil {
		return r, err
	}
	err = db.InTx(ctx, s.d.DB, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, `UPDATE workagents_design_reviews SET status='queued',error_code='',attempts=attempts+1,updated_at=? WHERE id=? AND status='failed' AND attempts=?`, db.FormatTime(s.now()), id, r.Attempts)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return errConflict
		}
		jobID, e := s.d.Jobs.EnqueueTx(ctx, tx, designReviewJobKind, map[string]any{"review_id": id, "attempt": r.Attempts + 1}, jobs.Idempotency(fmt.Sprintf("design-review:%s:%d", id, r.Attempts+1)))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE workagents_design_reviews SET job_id=? WHERE id=?`, jobID, id)
		return e
	})
	if err != nil {
		return r, err
	}
	s.reviewAudit(ctx, "retried", id)
	return s.designReview(ctx, id)
}
func (s *service) reviewAudit(ctx context.Context, action, id string) {
	requestID := httpx.RequestIDFromContext(ctx)
	_ = s.d.DB.QueryRowContext(context.WithoutCancel(ctx), `SELECT request_id FROM workagents_design_reviews WHERE id=?`, id).Scan(&requestID)
	if s.d.Log != nil {
		s.d.Log.Info("design review stage", "review_id", id, "request_id", requestID, "stage", action)
	}
	if s.d.Audit != nil {
		s.d.Audit.Record(ctx, "workagent.review."+action, "design_review", id, map[string]string{"request_id": requestID, "stage": action})
	}
}
