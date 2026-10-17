package db

import (
	"regexp"
	"strings"
)

// Destructive statement rules reported by DestructiveStatements.
const (
	RuleDropTable  = "DROP TABLE"
	RuleDropColumn = "DROP COLUMN"
	RuleDropIndex  = "DROP INDEX"
	RuleRename     = "RENAME"
)

// destructiveRules are matched, in this order, against SQL with comments and
// string literals removed. A table rebuild (CREATE x_new, copy, DROP x,
// RENAME x_new TO x) reports both DROP TABLE and RENAME.
var destructiveRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{RuleDropTable, regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`)},
	{RuleDropColumn, regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+\S+\s+DROP\b`)}, // COLUMN is optional in SQLite
	{RuleDropIndex, regexp.MustCompile(`(?i)\bDROP\s+INDEX\b`)},
	{RuleRename, regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+\S+\s+RENAME\b`)}, // table, COLUMN or bare column
}

// DestructiveStatements returns the destructive rules (DROP TABLE, DROP
// COLUMN, DROP INDEX, RENAME) the migration body matches, ignoring comments
// and string literals. Nil means the migration is additive. Matching is
// case-insensitive and whitespace-tolerant.
func DestructiveStatements(body string) []string {
	code := StripSQL(body)
	var out []string
	for _, r := range destructiveRules {
		if r.re.MatchString(code) {
			out = append(out, r.name)
		}
	}
	return out
}

// StripSQL removes "--" line comments and "/* */" block comments (each
// becomes a space, newlines kept) and empties single-quoted string literals
// (doubled-quote escapes included), in one pass so an apostrophe inside a
// comment or "--" inside a string cannot confuse the other. Double-quoted
// identifiers are kept.
func StripSQL(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "--"):
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				return b.String()
			}
			b.WriteByte(' ')
			i += end // keep the newline
		case strings.HasPrefix(s[i:], "/*"):
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			b.WriteByte(' ')
			i += 2 + end + 2
		case s[i] == '\'':
			b.WriteString("''")
			i++
			for i < len(s) {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}
