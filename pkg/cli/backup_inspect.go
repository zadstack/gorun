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

// secretNearManifest reads an existing site secret from the data directory that
// owns the backups/ folder. It never generates a new secret.key.
func secretNearManifest(manifestPath string) []byte {
	dataDir := filepath.Dir(filepath.Dir(filepath.Clean(manifestPath)))
	raw, err := os.ReadFile(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(raw))
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	}
	if err != nil || len(key) != 32 {
		return nil
	}
	return key
}
