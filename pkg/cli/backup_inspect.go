package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	gpbackup "github.com/zadstack/gorun/internal/backup"
)

func backupInspect(args []string, out io.Writer) error {
	input, args, err := stringFlag(args, "input")
	if err != nil {
		return err
	}
	if input == "" || len(args) != 0 {
		return errors.New("usage: gorun backup-inspect --input <timestamp>.manifest.json")
	}
	rep, err := gpbackup.InspectWith(input, secretNearManifest(input))
	if err != nil {
		return err
	}
	e := json.NewEncoder(out)
	e.SetIndent("", "  ")
	if err := e.Encode(rep); err != nil {
		return err
	}
	if rep.Readiness == "blocked" {
		return errors.New("backup: preflight blocked; inspect the reported error codes")
	}
	return nil
}

// secretNearManifest resolves the 32-byte site secret for a manifest: the
// data directory's secret.key if present, otherwise GORUN_SECRET_KEY. It
// never generates a new secret.key.
func secretNearManifest(manifestPath string) []byte {
	dataDir := filepath.Dir(filepath.Dir(filepath.Clean(manifestPath)))
	if key := decodeSecretBytes(readSecretFile(filepath.Join(dataDir, "secret.key"))); len(key) == 32 {
		return key
	}
	return decodeSecretBytes([]byte(strings.TrimSpace(os.Getenv("GORUN_SECRET_KEY"))))
}

// readDataDirSecret reads secret.key under dataDir only (no env, no generate).
func readDataDirSecret(dataDir string) []byte {
	return decodeSecretBytes(readSecretFile(filepath.Join(dataDir, "secret.key")))
}

func readSecretFile(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return []byte(strings.TrimSpace(string(raw)))
}

func decodeSecretBytes(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	s := string(raw)
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	}
	if err != nil || len(key) != 32 {
		return nil
	}
	return key
}
