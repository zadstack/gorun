package cli

import (
	"bytes"
	"crypto/sha256"
	backupio "github.com/zadstack/gorun/internal/backup"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBackupDecryptDoesNotOverwriteAndRejectsWrongSecret(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "20261012T120000Z.db.gorb")
	output := filepath.Join(dir, "restored.db")
	key := bytes.Repeat([]byte{2}, 32)
	sealed, err := backupio.Encrypt(bytes.NewReader([]byte("synthetic database")), key, "20261012T120000Z.db")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(input, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = decryptBackupFile(input, output, bytes.Repeat([]byte{3}, 32)); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed decrypt created output")
	}
	if err = decryptBackupFile(input, output, key); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(output)
	if string(got) != "synthetic database" {
		t.Fatal("bad plaintext")
	}
	if err = decryptBackupFile(input, output, key); err == nil {
		t.Fatal("existing destination overwritten")
	}
	st, _ := os.Stat(output)
	if st.Mode().Perm() != 0o600 {
		t.Fatal("plaintext permissions")
	}
}

type zeroRecoveryReader struct{}

func (zeroRecoveryReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestBackupDecryptStreams119MiBAndCreatesNoOutputOnLateTampering(t *testing.T) {
	const size int64 = 119 << 20
	dir := t.TempDir()
	input := filepath.Join(dir, "20261012T120000Z.tar.gz.gorb")
	output := filepath.Join(dir, "restored.tar.gz")
	key := bytes.Repeat([]byte{2}, 32)
	sealed, err := os.OpenFile(input, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backupio.EncryptTo(sealed, io.LimitReader(zeroRecoveryReader{}, size), key, "20261012T120000Z.tar.gz"); err != nil {
		t.Fatal(err)
	}
	end, err := sealed.Seek(-1, io.SeekEnd)
	if err != nil {
		t.Fatal(err)
	}
	tail := make([]byte, 1)
	if _, err = sealed.Read(tail); err != nil {
		t.Fatal(err)
	}
	if _, err = sealed.WriteAt([]byte{tail[0] ^ 1}, end); err != nil {
		t.Fatal(err)
	}
	if err = decryptBackupFile(input, output, key); err == nil {
		t.Fatal("late tampering accepted")
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("authentication failure created plaintext output")
	}
	if _, err = sealed.WriteAt(tail, end); err != nil {
		t.Fatal(err)
	}
	if err = sealed.Close(); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err = decryptBackupFile(input, output, key); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if allocation := after.TotalAlloc - before.TotalAlloc; allocation > 8<<20 {
		t.Fatalf("stream recovery allocated %d bytes", allocation)
	}
	restored, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	actual := sha256.New()
	n, err := io.Copy(actual, restored)
	if err != nil || n != size {
		t.Fatal("recovery size changed", n, err)
	}
	expected := sha256.New()
	if _, err = io.Copy(expected, io.LimitReader(zeroRecoveryReader{}, size)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual.Sum(nil), expected.Sum(nil)) {
		t.Fatal("recovery content changed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatal("unexpected recovery temporary files", entries, err)
	}
	t.Logf("119MiB verified recovery allocated %d bytes", after.TotalAlloc-before.TotalAlloc)
}
