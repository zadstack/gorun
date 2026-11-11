package auth

import (
	"strings"
	"time"
)

// TOTPCodeForTest computes the current code for a grouped base32 setup key
// as shown on the enrolment page (external tests act as the phone app).
func TOTPCodeForTest(setupKey string, at time.Time) string {
	secret, err := totpEncoding.DecodeString(strings.ReplaceAll(setupKey, " ", ""))
	if err != nil {
		panic(err)
	}
	return totpCode(secret, at)
}
