package cli

import (
	"encoding/json"
	"errors"
	"io"

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
	rep, err := gpbackup.Inspect(input)
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
