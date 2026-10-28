package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/media"
)

type encryptedBackupTransfer struct {
	driver       *media.S3Storage
	client       *http.Client
	path, object string
	secret       []byte
	expected     backup.FileDigest
}

func (transfer encryptedBackupTransfer) transfer(ctx context.Context) (acknowledged bool, resultErr error) {
	spool, err := transfer.encrypt(ctx)
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, spool.Close(), os.Remove(spool.Name())) }()
	if err = transfer.driver.PutEncryptedBackupReader(ctx, transfer.client, transfer.object, spool); err != nil {
		return false, err
	}
	return true, transfer.readback(ctx)
}

func (transfer encryptedBackupTransfer) encrypt(ctx context.Context) (*os.File, error) {
	before, err := os.Lstat(transfer.path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() != transfer.expected.Size {
		return nil, errors.New("backup: source differs from authenticated manifest")
	}
	source, err := os.Open(transfer.path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, errors.New("backup: source changed before encryption")
	}
	spool, err := os.CreateTemp(filepath.Dir(transfer.path), ".gorun-encrypted-*.gorb")
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	_, err = backup.EncryptTo(spool, io.TeeReader(backupContextReader{ctx, source}, digest), transfer.secret, transfer.expected.Name)
	if err == nil && hex.EncodeToString(digest.Sum(nil)) != transfer.expected.SHA256 {
		err = errors.New("backup: source checksum differs from authenticated manifest")
	}
	if err == nil {
		err = spool.Sync()
	}
	if err != nil {
		spool.Close()
		os.Remove(spool.Name())
		return nil, err
	}
	return spool, nil
}

func (transfer encryptedBackupTransfer) readback(ctx context.Context) error {
	body, err := transfer.driver.OpenEncryptedBackup(ctx, transfer.client, transfer.object)
	if err != nil {
		return err
	}
	defer body.Close()
	size, err := backup.EncryptedSize(transfer.expected.Size)
	if err != nil {
		return err
	}
	digest := sha256.New()
	count, err := backup.DecryptTo(digest, io.LimitReader(body, size+1), transfer.secret, transfer.expected.Name)
	if err != nil {
		return errors.New("backup: remote readback authentication failed")
	}
	if count != transfer.expected.Size || hex.EncodeToString(digest.Sum(nil)) != transfer.expected.SHA256 {
		return errors.New("backup: remote readback checksum mismatch")
	}
	return nil
}

type backupContextReader struct {
	ctx context.Context
	io.Reader
}

func (r backupContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
