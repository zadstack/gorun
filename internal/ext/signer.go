package ext

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// Bridge headers.
const (
	HeaderSignature = "X-GoRun-Signature"
	HeaderTimestamp = "X-GoRun-Timestamp"
	HeaderExtension = "X-GoRun-Extension"
	HeaderDelivery  = "X-GoRun-Delivery"
)

// Sign returns hex(hmac_sha256(secret, ts + "\n" + body)): the
// X-GoRun-Signature of a bridge request whose X-GoRun-Timestamp is ts
// (unix seconds, the same value as the body's "ts").
func Sign(secret []byte, ts string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a bridge signature in constant time and rejects timestamps
// further than skew from now (replay window).
func Verify(secret []byte, ts string, body []byte, sig string, now time.Time, skew time.Duration) error {
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("ext: bad timestamp")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > skew || d < -skew {
		return errors.New("ext: timestamp outside the allowed window")
	}
	want := Sign(secret, ts, body)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("ext: signature mismatch")
	}
	return nil
}
