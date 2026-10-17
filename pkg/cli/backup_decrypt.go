package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	backupio "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
)

func backupDecrypt(args []string) error {
	input, args, err := stringFlag(args, "input")
	if err != nil {
		return err
	}
	output, args, err := stringFlag(args, "output")
	if err != nil {
		return err
	}
	if input == "" || output == "" {
		return errors.New("usage: gorun backup-decrypt --input <original-name>.gorb --output <new-file> [--data <recovery-secret-directory>]")
	}
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	return decryptBackupFile(input, output, cfg.SecretKey)
}
func decryptBackupFile(input, output string, key []byte) error {
	if !strings.HasSuffix(input, ".gorb") {
		return errors.New("backup: encrypted input must retain its .gorb filename")
	}
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	plain, err := backupio.Decrypt(f, key, strings.TrimSuffix(filepath.Base(input), ".gorb"))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := out.Write(plain)
	closeErr = out.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(output)
		return errors.Join(writeErr, closeErr)
	}
	return nil
}
