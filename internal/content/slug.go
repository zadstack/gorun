package content

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const maxSlugRunes = 200

// latinFold maps common accented Latin letters to ASCII for asciiOnly slugs.
var latinFold = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'æ': "ae", 'ç': "c", 'è': "e", 'é': "e", 'ê': "e", 'ë': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ñ': "n", 'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ù': "u",
	'ú': "u", 'û': "u", 'ü': "u", 'ý': "y", 'ÿ': "y", 'ß': "ss", 'œ': "oe", 'ğ': "g", 'ş': "s", 'ı': "i",
}

// Slugify builds a URL slug: lowercase, spaces/underscores/dashes collapsed
// to single "-", and only letters and digits of any script kept, so Arabic
// stays UTF-8 ("مرحبا بالعالم" → "مرحبا-بالعالم"); marks, punctuation and
// bidi controls are dropped. With asciiOnly (setting
// content.transliterate_slugs) accented Latin letters are folded to ASCII
// and other non-ASCII letters dropped.
func Slugify(s string, asciiOnly bool) string {
	var b strings.Builder
	dash, n := false, 0
	emit := func(r string) {
		if dash && b.Len() > 0 {
			b.WriteByte('-')
		}
		dash = false
		b.WriteString(r)
		n++
	}
	for _, r := range strings.ToLower(s) {
		if n >= maxSlugRunes {
			break
		}
		switch {
		case r == ' ' || r == '_' || r == '-' || unicode.IsSpace(r):
			dash = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !asciiOnly || r < unicode.MaxASCII {
				emit(string(r))
			} else if f, ok := latinFold[r]; ok {
				emit(f)
			}
		}
	}
	return b.String()
}

// transliterate reports the content.transliterate_slugs setting.
func (s *service) transliterate(ctx context.Context) bool {
	var v bool
	if s.settings == nil || s.settings.Get(ctx, "content.transliterate_slugs", &v) != nil {
		return false
	}
	return v
}

// uniqueSlug returns base, or base-2, base-3, ... — the first slug free for
// (type, locale) other than excludeID.
func uniqueSlug(ctx context.Context, tx *sql.Tx, typ, locale, base, excludeID string) (string, error) {
	for i := 1; i < 10000; i++ {
		cand := base
		if i > 1 {
			cand = base + "-" + strconv.Itoa(i)
		}
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM content WHERE type = ? AND locale = ? AND slug = ? AND id != ?`,
			typ, locale, cand, excludeID).Scan(&one)
		if err == sql.ErrNoRows {
			return cand, nil
		}
		if err != nil {
			return "", fmt.Errorf("content: slug check: %w", err)
		}
	}
	return "", fmt.Errorf("content: no free slug for %q", base)
}
