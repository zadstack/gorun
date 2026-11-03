package analytics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// saltSize is the number of random bytes in a day's salt.
const saltSize = 32

// dayKey is the UTC calendar day of t, as stored in salts.day and daily.day.
func dayKey(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// VisitorHash returns hex(sha256(salt || siteID || ip || ua))[:16], where
// salt is the 32 random bytes of day's UTC date. It creates that salt on
// first use and then deletes the salts of earlier days, so a visitor cannot
// be linked across days and the raw IP and user agent are never stored.
// Pass the time of the request: an event hashed just before midnight keeps
// that day's salt however late it is flushed.
func (s *Store) VisitorHash(ctx context.Context, day time.Time, siteID, ip, ua string) (string, error) {
	salt, err := s.salt(ctx, dayKey(day))
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(siteID))
	h.Write([]byte(ip))
	h.Write([]byte(ua))
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// salt gets or creates the salt for day (YYYY-MM-DD). Salts are cached in
// memory; Reopen clears the cache with the file.
func (s *Store) salt(ctx context.Context, day string) ([]byte, error) {
	d, err := s.ensure() // before saltMu: a reopen takes saltMu to clear the cache
	if err != nil {
		return nil, err
	}
	s.saltMu.Lock()
	defer s.saltMu.Unlock()
	if v, ok := s.salts[day]; ok {
		return v, nil
	}
	var salt []byte
	err = d.QueryRowContext(ctx, `SELECT salt FROM salts WHERE day = ?`, day).Scan(&salt)
	if errors.Is(err, sql.ErrNoRows) {
		salt, err = s.rotate(ctx, d, day)
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: salt for %s: %w", day, err)
	}
	for k := range s.salts {
		if k < day {
			delete(s.salts, k)
		}
	}
	s.salts[day] = salt
	return salt, nil
}

// rotate stores a new random salt for day and deletes the salts of earlier
// days. INSERT OR IGNORE plus the re-read keeps one salt per day when two
// processes race.
func (s *Store) rotate(ctx context.Context, d *sql.DB, day string) ([]byte, error) {
	fresh := make([]byte, saltSize)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	if _, err := d.ExecContext(ctx, `INSERT OR IGNORE INTO salts (day, salt) VALUES (?, ?)`, day, fresh); err != nil {
		return nil, err
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM salts WHERE day < ?`, day); err != nil {
		return nil, err
	}
	var salt []byte
	if err := d.QueryRowContext(ctx, `SELECT salt FROM salts WHERE day = ?`, day).Scan(&salt); err != nil {
		return nil, err
	}
	return salt, nil
}
