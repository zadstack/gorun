package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	gpbackup "github.com/zadstack/gorun/internal/backup"
)

// backupRestore implements `gorun backup-restore --input <stamp>.manifest.json
// --dest <new-directory-path>`: inspects the local pair, then writes into a
// destination that must not already exist. It never merges into a live data directory.
func backupRestore(args []string, out io.Writer) error {
	input, args, err := stringFlag(args, "input")
	if err != nil {
		return err
	}
	dest, args, err := stringFlag(args, "dest")
	if err != nil {
		return err
	}
	if input == "" || dest == "" || len(args) != 0 {
		return errors.New("usage: gorun backup-restore --input <timestamp>.manifest.json --dest <new-directory-path>")
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(dest)), 0o700); err != nil {
		return err
	}
	rep, err := gpbackup.RestoreWith(input, dest, secretNearManifest(input))
	if err != nil {
		if rep != nil {
			_ = encodeInspection(out, rep)
		}
		return err
	}
	if err := encodeInspection(out, rep); err != nil {
		return err
	}
	fmt.Fprintf(out, "restored: %s\n", dest)
	fmt.Fprintln(out, "next: copy secret.key and gorun.toml (or set the original GORUN_SECRET_KEY), stop any process using the old data directory, then serve with --data pointing at the restored directory")
	return nil
}

func encodeInspection(out io.Writer, rep *gpbackup.Inspection) error {
	e := json.NewEncoder(out)
	e.SetIndent("", "  ")
	return e.Encode(rep)
}
