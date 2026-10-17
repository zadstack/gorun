package db

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(7)
	for _, plain := range [][]byte{[]byte("smtp-password"), {}, bytes.Repeat([]byte("x"), 4096)} {
		ct, err := Encrypt(key, plain, "settings:mail.password")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.StdEncoding.DecodeString(ct)
		if err != nil || len(raw) != gcmNonceSize+len(plain)+gcmTagSize {
			t.Fatalf("output must be base64(nonce||ciphertext||tag): len=%d err=%v", len(raw), err)
		}
		got, err := Decrypt(key, ct, "settings:mail.password")
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("round trip: %q %v", got, err)
		}
	}
	a, _ := Encrypt(key, []byte("same"), "aad")
	b, _ := Encrypt(key, []byte("same"), "aad")
	if a == b {
		t.Fatal("random nonce: two encryptions of the same input must differ")
	}
}

func TestEncryptDecryptFailures(t *testing.T) {
	key := testKey(1)
	good, err := Encrypt(key, []byte("secret"), "row:1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(good)
	flipped := bytes.Clone(raw)
	flipped[len(flipped)-1] ^= 0x01
	flippedNonce := bytes.Clone(raw)
	flippedNonce[0] ^= 0x01
	enc := base64.StdEncoding.EncodeToString

	cases := []struct {
		name string
		key  []byte
		ct   string
		aad  string
	}{
		{"wrong aad", key, good, "row:2"},
		{"empty aad", key, good, ""},
		{"wrong key", testKey(2), good, "row:1"},
		{"tampered ciphertext", key, enc(flipped), "row:1"},
		{"tampered nonce", key, enc(flippedNonce), "row:1"},
		{"truncated", key, enc(raw[:gcmNonceSize+gcmTagSize-1]), "row:1"},
		{"empty", key, "", "row:1"},
		{"not base64", key, "%%%not-base64%%%", "row:1"},
		{"short key", make([]byte, 16), good, "row:1"},
		{"31-byte key", make([]byte, 31), good, "row:1"},
		{"33-byte key", make([]byte, 33), good, "row:1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Decrypt(c.key, c.ct, c.aad)
			if err == nil {
				t.Fatalf("want error, got plaintext %q", got)
			}
			if len(c.key) == 32 && !errors.Is(err, ErrDecrypt) {
				t.Fatalf("want ErrDecrypt, got %v", err)
			}
		})
	}
	for _, n := range []int{0, 16, 24, 31, 33, 64} {
		if _, err := Encrypt(make([]byte, n), []byte("x"), "a"); err == nil || !strings.Contains(err.Error(), "32 bytes") {
			t.Errorf("Encrypt with %d-byte key: %v", n, err)
		}
	}
}
