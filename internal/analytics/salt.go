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
// Pass the time of the request. A day older than the newest stored salt
// (a request straddling midnight) uses the newest salt: a past day's salt
// is never recreated once it has been rotated out.
func (s *Store) VisitorHash(ctx context.Context, day time.Time, siteID, ip, ua string) (string, error) {
	d, release, err := s.acquire()
	if err != nil {
		return "", err
	}
	defer release()
	salt, err := s.salt(ctx, d, dayKey(day))
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

// salt returns the salt to use for day (YYYY-MM-DD): day's own, the newest
// one when day is older, or a new one when day is newer than every stored
// salt. The caller holds the read lock. The newest salt is cached; Reopen
// clears the cache with the file.
func (s *Store) salt(ctx context.Context, d *sql.DB, day string) ([]byte, error) {
	s.saltMu.Lock()
	defer s.saltMu.Unlock()
	if s.saltDay != "" && day <= s.saltDay {
		return s.saltData, nil
	}
	// Not cached, or a new day: another process may already have rotated.
	var newest string
	var salt []byte
	err := d.QueryRowContext(ctx, `SELECT day, salt FROM salts ORDER BY day DESC LIMIT 1`).Scan(&newest, &salt)
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && newest < day):
		newest = day
		salt, err = s.rotate(ctx, d, day)
	}
	if err != nil {
		return nil, fmt.Errorf("analytics: salt for %s: %w", day, err)
	}
	s.saltDay, s.saltData = newest, salt
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
