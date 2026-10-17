package redirects

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

const maxImportRows = 20000

// RowError is one rejected CSV line; Key is an i18n key.
type RowError struct {
	Line int    `json:"line"`
	Key  string `json:"key"`
}

// ImportResult summarizes a CSV import.
type ImportResult struct {
	Imported int        `json:"imported"`
	Errors   []RowError `json:"errors"`
}

// ImportCSV upserts redirects from CSV rows "from,to[,status]" (an optional
// header row starting with "from" is skipped; status defaults to 301).
// Existing from paths are updated and re-enabled. Valid rows are written in
// one transaction; invalid rows are reported and skipped.
func (s *service) ImportCSV(ctx context.Context, r io.Reader) (ImportResult, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	var res ImportResult
	var valid []Redirect
	seen := map[string]bool{}
	host := s.siteHost(ctx)
	for line := 1; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			res.Errors = append(res.Errors, RowError{Line: line, Key: "redirects.import.error_syntax"})
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				continue
			}
			break
		}
		if line == 1 && len(rec) > 0 && strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(rec[0], "\ufeff")), "from") {
			continue
		}
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		if len(valid) >= maxImportRows {
			res.Errors = append(res.Errors, RowError{Line: line, Key: "redirects.import.error_too_many"})
			break
		}
		if len(rec) < 2 || len(rec) > 3 {
			res.Errors = append(res.Errors, RowError{Line: line, Key: "redirects.import.error_columns"})
			continue
		}
		rd := Redirect{FromPath: strings.TrimPrefix(rec[0], "\ufeff"), ToURL: rec[1], Enabled: true}
		if len(rec) == 3 && strings.TrimSpace(rec[2]) != "" {
			n, err := strconv.Atoi(strings.TrimSpace(rec[2]))
			if err != nil {
				res.Errors = append(res.Errors, RowError{Line: line, Key: "redirects.error.status_invalid"})
				continue
			}
			rd.Status = n
		}
		rd, err = clean(rd, host)
		if err != nil {
			var ve *ValidationError
			key := "redirects.import.error_row"
			if errors.As(err, &ve) {
				for _, f := range []string{"from_path", "to_url", "status"} {
					if k, ok := ve.Fields[f]; ok {
						key = k
						break
					}
				}
			}
			res.Errors = append(res.Errors, RowError{Line: line, Key: key})
			continue
		}
		if seen[rd.FromPath] {
			res.Errors = append(res.Errors, RowError{Line: line, Key: "redirects.import.error_duplicate"})
			continue
		}
		seen[rd.FromPath] = true
		valid = append(valid, rd)
	}
	if len(valid) == 0 {
		return res, nil
	}
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO redirects (id, from_path, to_url, status, enabled, created_at)
			VALUES (?, ?, ?, ?, 1, ?)
			ON CONFLICT (from_path) DO UPDATE SET to_url = excluded.to_url, status = excluded.status, enabled = 1`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		now := db.Now()
		for _, rd := range valid {
			if _, err := stmt.ExecContext(ctx, db.NewID(), rd.FromPath, rd.ToURL, rd.Status, now); err != nil {
				return err
			}
		}
		s.audit.RecordTx(ctx, tx, "redirects.import", "redirect", "", map[string]any{"imported": len(valid), "rejected": len(res.Errors)})
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("redirects: import: %w", err)
	}
	res.Imported = len(valid)
	s.invalidate()
	s.hooks.Emit(ctx, HookSaved, SavedEvent{Action: "import", Count: res.Imported})
	return res, nil
}
