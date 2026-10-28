package cli

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
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
	defer f.Close()
	name := strings.TrimSuffix(filepath.Base(input), ".gorb")
	verified := sha256.New()
	if _, err = backupio.DecryptTo(io.Discard, io.TeeReader(f, verified), key, name); err != nil {
		return err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	owned, err := out.Stat()
	if err != nil {
		out.Close()
		return err
	}
	observed := sha256.New()
	_, decodeErr := backupio.DecryptTo(out, io.TeeReader(f, observed), key, name)
	if decodeErr == nil && !bytes.Equal(verified.Sum(nil), observed.Sum(nil)) {
		decodeErr = errors.New("backup: encrypted input changed during recovery")
	}
	syncErr := out.Sync()
	closeErr := out.Close()
	if err = errors.Join(decodeErr, syncErr, closeErr); err != nil {
		current, statErr := os.Lstat(output)
		if os.IsNotExist(statErr) {
			return err
		}
		if statErr != nil || !os.SameFile(owned, current) {
			return errors.Join(err, errors.New("backup: output path changed; refusing unrelated cleanup"))
		}
		return errors.Join(err, os.Remove(output))
	}
	return nil
}
