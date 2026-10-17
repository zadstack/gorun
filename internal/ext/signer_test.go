package ext

import (
	"strconv"
	"testing"
	"time"
)

func TestSignerKnownVector(t *testing.T) {
	// hex(hmac_sha256("topsecret", "1700000000\n{\"a\":1}")), computed with
	// `printf '1700000000\n{"a":1}' | openssl dgst -sha256 -hmac topsecret`.
	got := Sign([]byte("topsecret"), "1700000000", []byte(`{"a":1}`))
	const want = "5a5686462a74a9b13d69e0cde0a0b3a76f9c2c54dc84d1ca8d3a0019c1e48d1c"
	if got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}

func TestSignerVerify(t *testing.T) {
	secret := []byte("s3cret")
	now := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"hook":"x"}`)
	sig := Sign(secret, ts, body)
	if err := Verify(secret, ts, body, sig, now, 5*time.Minute); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := Verify(secret, ts, []byte(`{"hook":"y"}`), sig, now, 5*time.Minute); err == nil {
		t.Fatal("tampered body accepted")
	}
	if err := Verify([]byte("other"), ts, body, sig, now, 5*time.Minute); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if err := Verify(secret, ts, body, sig, now.Add(10*time.Minute), 5*time.Minute); err == nil {
		t.Fatal("stale timestamp accepted")
	}
	if err := Verify(secret, "nope", body, sig, now, 5*time.Minute); err == nil {
		t.Fatal("bad timestamp accepted")
	}
}
