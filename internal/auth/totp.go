package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP uses HMAC-SHA-1; authenticator apps expect it.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters (RFC 6238 defaults understood by every authenticator app).
const (
	totpPeriod     = 30 // seconds per time step
	totpDigits     = 6
	totpSecretSize = 20 // bytes (160 bits, RFC 4226 §4 recommendation)
	totpSkew       = 1  // accept the previous and next step for clock drift
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a fresh random shared secret.
func newTOTPSecret() []byte {
	b := make([]byte, totpSecretSize)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return b
}

// hotp is RFC 4226 HOTP with HMAC-SHA-1 and dynamic truncation.
func hotp(key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// totpStep is the RFC 6238 time step T for t.
func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// totpCode is the 6-digit code for the step containing t.
func totpCode(key []byte, t time.Time) string {
	return hotp(key, uint64(totpStep(t)), totpDigits)
}

// matchTOTP checks code against the steps now-1 … now+1 that are later than
// lastStep and returns the matching step. Every candidate is compared in
// constant time and the loop never exits early, so timing does not reveal
// which step matched.
func matchTOTP(key []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = normalizeOTP(code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := totpStep(now)
	var found int64
	ok := 0
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		step := cur + d
		if step <= 0 {
			continue
		}
		eq := subtle.ConstantTimeCompare([]byte(hotp(key, uint64(step), totpDigits)), []byte(code))
		if eq == 1 && step > lastStep && ok == 0 {
			found, ok = step, 1
		}
	}
	return found, ok == 1
}

// normalizeOTP drops the spaces and dashes people type in codes.
func normalizeOTP(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == ' ' {
			return -1
		}
		// Arabic-Indic and Extended Arabic-Indic digits typed on Arabic keyboards.
		if r >= '٠' && r <= '٩' {
			return '0' + (r - '٠')
		}
		if r >= '۰' && r <= '۹' {
			return '0' + (r - '۰')
		}
		return r
	}, strings.TrimSpace(s))
}

// formatTOTPSecret is the base32 secret in groups of four for manual entry.
func formatTOTPSecret(secret []byte) string {
	s := totpEncoding.EncodeToString(secret)
	var b strings.Builder
	for i := 0; i < len(s); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(s[i:min(i+4, len(s))])
	}
	return b.String()
}

// totpURI is the otpauth:// key URI (Google Authenticator key-uri format)
// that authenticator apps import.
func totpURI(secret []byte, issuer, account string) string {
	if issuer == "" {
		issuer = "GoRun"
	}
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", totpEncoding.EncodeToString(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
