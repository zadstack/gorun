package cli

import (
	"bytes"
	backupio "github.com/zadstack/gorun/internal/backup"
	"os"
	"path/filepath"
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
