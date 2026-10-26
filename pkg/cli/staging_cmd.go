package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	gpbackup "github.com/zadstack/gorun/internal/backup"
)

const stagingUsage = "usage: gorun staging seed|promote (--from <source-data> | --input <stamp>.manifest.json) --dest <new-directory-path> [--include-secrets]"

// stagingCmd implements `gorun staging seed|promote`: productized staging /
// promote-to-live helpers on top of backup + confined restore. Both write only
// into a new --dest (must not exist) and never merge into a live data directory.
func stagingCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(stagingUsage)
	}
	mode := args[0]
	switch mode {
	case "seed", "promote":
	default:
		return errors.New(stagingUsage)
	}
	args = args[1:]
	includeSecrets, args := boolFlag(args, "include-secrets")
	from, args, err := stringFlag(args, "from")
	if err != nil {
		return err
	}
	input, args, err := stringFlag(args, "input")
	if err != nil {
		return err
	}
	dest, args, err := stringFlag(args, "dest")
	if err != nil {
		return err
	}
	if dest == "" || len(args) != 0 {
		return errors.New(stagingUsage)
	}
	if (from == "") == (input == "") {
		return errors.New("staging: provide exactly one of --from <source-data> or --input <stamp>.manifest.json")
	}
	if includeSecrets && input != "" {
		return errors.New("staging: --include-secrets only applies with --from (the source backup already chose secrets)")
	}

	var secret []byte
	manifest := input
	if from != "" {
		from = filepath.Clean(from)
		dbPath := filepath.Join(from, "gorun.db")
		// Only the source site's secret.key — never GORUN_SECRET_KEY from the
		// process env, and never generate a new key into the source tree.
		secret = readDataDirSecret(from)
		res, createErr := gpbackup.CreateWith(from, dbPath, time.Now(), gpbackup.Options{IncludeSecrets: includeSecrets, SecretKey: secret})
		if createErr != nil {
			return createErr
		}
		manifest = res.Manifest
		fmt.Fprintf(out, "backup:   %s\n", res.Manifest)
		if includeSecrets {
			fmt.Fprintln(out, "secrets:  secret.key and gorun.toml are in the archive in plain text; keep it offline")
		} else {
			fmt.Fprintln(out, "secrets:  not archived; copy secret.key / GORUN_SECRET_KEY after restore")
		}
	} else {
		secret = secretNearManifest(manifest)
	}

	if err := os.MkdirAll(filepath.Dir(filepath.Clean(dest)), 0o700); err != nil {
		return err
	}
	rep, err := gpbackup.RestoreWith(manifest, dest, secret)
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
	printStagingNext(out, mode, dest)
	return nil
}

func printStagingNext(out io.Writer, mode, dest string) {
	fmt.Fprintln(out, "next:")
	fmt.Fprintln(out, "  1. Copy secret.key and gorun.toml into the restored directory (or set the original GORUN_SECRET_KEY), unless the backup used --include-secrets.")
	fmt.Fprintln(out, "  2. Re-point external credentials (S3, Stripe, mail) as needed; object storage is not in the archive.")
	switch mode {
	case "seed":
		fmt.Fprintf(out, "  3. Serve staging on its own data dir and address, e.g. gorun serve --data %s --addr 127.0.0.1:8081 --env prod with a non-public GORUN_BASE_URL.\n", dest)
		fmt.Fprintln(out, "  4. Keep production on its original --data; two processes must not share one data directory (gorun.lock).")
		fmt.Fprintln(out, "  5. When ready to cut over, run: gorun staging promote --from <staging-data> --dest <new-live-dir> (see docs/STAGING.md).")
	default: // promote
		fmt.Fprintln(out, "  3. Stop every process using the current live data directory (release gorun.lock).")
		fmt.Fprintf(out, "  4. Point the live service at the new directory: gorun serve --data %s (or swap the volume / Coolify mount to that path).\n", dest)
		fmt.Fprintln(out, "  5. Smoke-test /healthz, /readyz, admin login, and a published page before retiring the previous live dir.")
		fmt.Fprintln(out, "  6. Keep the previous live directory until smoke checks pass (rollback = stop + serve the old --data with the same secret).")
	}
	fmt.Fprintln(out, "docs: docs/STAGING.md")
}
