package admin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/settings"
)

func (h *handlers) metricoolConfig(ctx context.Context) (metricoolConfig, error) {
	var c metricoolConfig
	err := h.d.Settings.Get(ctx, metricoolConfigKey, &c)
	if errors.Is(err, settings.ErrNotFound) {
		err = nil
	}
	return c, err
}
func (h *handlers) saveMetricool(ctx context.Context, c metricoolConfig, expected string) error {
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, metricoolConfigKey).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			if expected != "" {
				return errMetricoolConflict
			}
		} else if err != nil {
			return err
		} else {
			var old metricoolConfig
			if json.Unmarshal([]byte(raw), &old) != nil || old.Version != expected {
				return errMetricoolConflict
			}
		}
		c.Version = db.NewID()
		if err = h.d.Settings.SetTx(ctx, tx, metricoolConfigKey, c); err != nil {
			return err
		}
		h.d.Audit.RecordTx(ctx, tx, "metricool.configure", "integration", "metricool", nil)
		return nil
	})
}
func (h *handlers) metricoolToken(c metricoolConfig) (string, error) {
	return mailer.DecryptSecret(h.d.Config.SecretKey, c.Secret)
}
func metricoolHash(c metricoolConfig, in metricoolInput) string {
	raw, _ := json.Marshal(struct {
		Version string
		Input   metricoolInput
	}{c.Version, in})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (h *handlers) claimMetricool(ctx context.Context, id, actor string, c metricoolConfig, in metricoolInput) (metricoolDraft, bool, error) {
	entry := metricoolDraft{ID: id, Actor: actor, Hash: metricoolHash(c, in), Status: "uncertain", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	fresh := false
	if !connectionIDPattern.MatchString(id) || actor == "" {
		return entry, false, errMetricoolInvalid
	}
	err := db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, metricoolDraftPrefix+id).Scan(&raw)
		if err == nil {
			var prior metricoolDraft
			if json.Unmarshal([]byte(raw), &prior) != nil || prior.Actor != actor || prior.Hash != entry.Hash {
				return errMetricoolConflict
			}
			entry = prior
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var configRaw string
		if err = tx.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, metricoolConfigKey).Scan(&configRaw); err != nil {
			return err
		}
		var current metricoolConfig
		if json.Unmarshal([]byte(configRaw), &current) != nil || current.Version != c.Version || current.TestStatus != "pass" {
			return errMetricoolConflict
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE key LIKE 'metricool.draft.%' AND json_extract(value_json,'$.actor')=? AND json_extract(value_json,'$.created_at')>=?`, actor, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)).Scan(&count); err != nil {
			return err
		}
		if count >= 20 {
			return errMetricoolInvalid
		}
		if err = h.d.Settings.SetTx(ctx, tx, metricoolDraftPrefix+id, entry); err != nil {
			return err
		}
		h.d.Audit.RecordTx(ctx, tx, "metricool.draft_requested", "metricool_draft", id, nil)
		fresh = true
		return nil
	})
	return entry, fresh, err
}
func (h *handlers) finishMetricool(ctx context.Context, entry metricoolDraft, providerID string) error {
	entry.Status = "confirmed"
	entry.ProviderID = providerID
	return db.InTx(ctx, h.d.DB, func(tx *sql.Tx) error {
		if err := h.d.Settings.SetTx(ctx, tx, metricoolDraftPrefix+entry.ID, entry); err != nil {
			return err
		}
		h.d.Audit.RecordTx(ctx, tx, "metricool.draft_confirmed", "metricool_draft", entry.ID, map[string]any{"provider_id": providerID})
		return nil
	})
}
