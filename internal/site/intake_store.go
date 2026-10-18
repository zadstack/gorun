package site

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/zadstack/gorun/internal/db"
)

var intakeID = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
var errIntakeConflict = errors.New("site: private intake changed")

type intakeNote struct{ By, At, Status, Text string }
type intakePayload struct {
	Category, Area, Date, Description, Contact, Reference string
	Notes                                                 []intakeNote
}
type intakeRecord struct {
	ID, Kind, Status, Version, CreatedAt, UpdatedAt string
	SourcePath                                      string
	Source                                          formSource
	Data                                            intakePayload
}

func (h *handlers) sealIntake(id string, data intakePayload) (string, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return db.Encrypt(h.d.Config.SecretKey, raw, "private-intake:"+id)
}

func (h *handlers) createIntakeFrom(ctx context.Context, id, kind, source string, data intakePayload) error {
	if source != "" && h.intakeSource(ctx, source, kind).Path == "" {
		return errIntakeConflict
	}
	encrypted, err := h.sealIntake(id, data)
	if err != nil {
		return err
	}
	now := db.Now()
	result, err := h.d.DB.ExecContext(ctx, `INSERT INTO private_intake(id,kind,payload,version,created_at,updated_at,source_path) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, id, kind, encrypted, db.NewID(), now, now, source)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		existing, err := h.loadIntake(ctx, id)
		if err != nil {
			return err
		}
		existing.Data.Notes = nil
		before, err := json.Marshal(existing.Data)
		if err != nil {
			return err
		}
		after, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if existing.Kind != kind || existing.SourcePath != source || string(before) != string(after) {
			return errIntakeConflict
		}
		return nil
	}
	h.d.Audit.Record(ctx, "intake.received", "private_intake", id, nil)
	return nil
}

func (h *handlers) loadIntake(ctx context.Context, id string) (intakeRecord, error) {
	var row intakeRecord
	var encrypted string
	err := h.d.DB.QueryRowContext(ctx, `SELECT id,kind,status,payload,version,created_at,updated_at,source_path FROM private_intake WHERE id=?`, id).Scan(&row.ID, &row.Kind, &row.Status, &encrypted, &row.Version, &row.CreatedAt, &row.UpdatedAt, &row.SourcePath)
	if err != nil {
		return row, err
	}
	row.Source = h.intakeSource(ctx, row.SourcePath, row.Kind)
	raw, err := db.Decrypt(h.d.Config.SecretKey, encrypted, "private-intake:"+id)
	if err != nil {
		return row, err
	}
	err = json.Unmarshal(raw, &row.Data)
	return row, err
}

func (h *handlers) updateIntake(ctx context.Context, row intakeRecord, expected string) error {
	payload, err := h.sealIntake(row.ID, row.Data)
	if err != nil {
		return err
	}
	result, err := h.d.DB.ExecContext(ctx, `UPDATE private_intake SET status=?,payload=?,version=?,updated_at=? WHERE id=? AND version=?`, row.Status, payload, db.NewID(), db.Now(), row.ID, expected)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errIntakeConflict
	}
	h.d.Audit.Record(ctx, "intake.reviewed", "private_intake", row.ID, map[string]any{"status": row.Status})
	return nil
}
