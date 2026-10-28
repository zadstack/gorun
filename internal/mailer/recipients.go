package mailer

import (
	"errors"
	"net/mail"
	"strings"
)

const (
	maxRecipients    = 10
	maxAddressLength = 254
)

// ValidPlainAddress reports a plain address without a display name, up to 254 bytes.
func ValidPlainAddress(address string) bool {
	parsed, err := mail.ParseAddress(address)
	return err == nil && parsed.Address == address && len(address) <= maxAddressLength
}

// ParseRecipients normalizes the native form-recipient list: comma, semicolon or
// newline separated plain addresses, with duplicates dropped and at most ten recipients.
func ParseRecipients(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }) {
		v := strings.TrimSpace(f)
		if v == "" {
			continue
		}
		if !ValidPlainAddress(v) {
			return nil, errors.New("not a plain email address: " + v)
		}
		if k := strings.ToLower(v); !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	if len(out) > maxRecipients {
		return nil, errors.New("at most 10 recipients")
	}
	return out, nil
}
