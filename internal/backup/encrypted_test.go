package backup

import (
	"bytes"
	"io"
	"testing"
)

type zeroBackupReader struct{}

func (zeroBackupReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestBufferedEncryptedBackupRejectsOver64MiB(t *testing.T) {
	if _, err := Encrypt(io.LimitReader(zeroBackupReader{}, maxBufferedEncryptedFile+1), bytes.Repeat([]byte{42}, 32), "20261012T120000Z.db"); err == nil {
		t.Fatal("oversized backup encrypted")
	}
}

func TestEncryptedBackupRoundTripAndAuthentication(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	plain := []byte("synthetic backup data")
	name := "20261012T120000Z.db"
	sealed, err := Encrypt(bytes.NewReader(plain), key, name)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("plaintext backup leaked")
	}
	got, err := Decrypt(bytes.NewReader(sealed), key, name)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip: %v", err)
	}
	for _, wrong := range []struct {
		key  []byte
		name string
		data []byte
	}{{bytes.Repeat([]byte{1}, 32), name, sealed}, {key, "20261012T120001Z.db", sealed}, {key, name, sealed[:len(sealed)-1]}} {
		if _, err := Decrypt(bytes.NewReader(wrong.data), wrong.key, wrong.name); err == nil {
			t.Fatal("invalid backup authenticated")
		}
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := Decrypt(bytes.NewReader(sealed), key, name); err == nil {
		t.Fatal("tampering authenticated")
	}
}
