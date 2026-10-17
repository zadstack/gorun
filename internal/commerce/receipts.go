package commerce

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// deliverRequestReceipts uses an at-most-once dispatch claim. SMTP cannot promise
// exactly-once delivery after a lost acknowledgement. Never retry a sending or
// uncertain receipt automatically; an operator must investigate provider logs.
func (s *service) deliverRequestReceipts(ctx context.Context) error {
	cutoff := s.now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `UPDATE commerce_request_receipts SET status='uncertain',last_error='dispatch_interrupted_check_provider',updated_at=? WHERE status='sending' AND updated_at<?`, s.nowString(), cutoff); err != nil {
		return err
	}
	for range 10 {
		var id, to, subject, html, text string
		err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
			err := tx.QueryRowContext(ctx, `SELECT order_id,recipient,subject,html_body,text_body FROM commerce_request_receipts WHERE status='pending' ORDER BY created_at,order_id LIMIT 1`).Scan(&id, &to, &subject, &html, &text)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE commerce_request_receipts SET status='sending',updated_at=? WHERE order_id=? AND status='pending'`, s.nowString(), id)
			return err
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err = s.sendReceipt(sendCtx, to, subject, html, text)
		cancel()
		status, code, sentAt := "sent", "", s.nowString()
		if err != nil {
			status, code, sentAt = "uncertain", "delivery_not_confirmed_check_provider", ""
		}
		// Persist the outcome even when a shutdown cancels the job context.
		saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, saveErr := s.db.ExecContext(saveCtx, `UPDATE commerce_request_receipts SET status=?,last_error=?,sent_at=?,updated_at=? WHERE order_id=? AND status='sending'`, status, code, sentAt, s.nowString(), id)
		saveCancel()
		if saveErr != nil {
			return saveErr
		}
	}
	return nil
}
