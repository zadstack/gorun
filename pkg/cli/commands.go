package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	_ "github.com/zadstack/gorun/internal/db" // registers the sqlite driver
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mcp"
	"github.com/zadstack/gorun/internal/wpmigrate"
)

// boolFlag removes --name (or --name=true/false) from args and reports it.
// Command flags are pulled out before the rest goes to config.Load.
func boolFlag(args []string, name string) (bool, []string) {
	var on bool
	rest := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--" + name, "-" + name, "--" + name + "=true", "-" + name + "=true":
			on = true
		case "--" + name + "=false", "-" + name + "=false":
			on = false
		default:
			rest = append(rest, a)
		}
	}
	return on, rest
}

// stringFlag removes --name value (or --name=value) from args.
func stringFlag(args []string, name string) (string, []string, error) {
	var val string
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--"+name || a == "-"+name:
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("flag --%s needs a value", name)
			}
			val = args[i+1]
			i++
		case strings.HasPrefix(a, "--"+name+"="):
			val = strings.TrimPrefix(a, "--"+name+"=")
		case strings.HasPrefix(a, "-"+name+"="):
			val = strings.TrimPrefix(a, "-"+name+"=")
		default:
			rest = append(rest, a)
		}
	}
	return val, rest, nil
}

// mcpStdio implements `gorun mcp [--key gp_…] [--jobs]`: boot every
// module (logs go to stderr; stdout carries the protocol) and serve MCP
// over in/out as the API key (--key, else GORUN_MCP_KEY). No job workers
// run unless --jobs: an MCP client usually starts this beside `gorun
// serve`, whose workers execute what the session enqueues (workers here
// would requeue the server's running jobs and duplicate its cron).
// --no-jobs is still accepted (it was the opt-out before).
func mcpStdio(args []string, in io.ReadCloser, out io.WriteCloser) error {
	key, args, err := stringFlag(args, "key")
	if err != nil {
		return err
	}
	startJobs, args := boolFlag(args, "jobs")
	_, args = boolFlag(args, "no-jobs") // the default now; kept for old configs
	if strings.TrimSpace(key) == "" {
		key = os.Getenv("GORUN_MCP_KEY")
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("mcp: an API key is required (--key gp_… or GORUN_MCP_KEY)")
	}
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app, err := kernel.BootWithOptions(ctx, cfg, Modules(cfg), kernel.BootOptions{StartJobs: startJobs})
	if err != nil {
		return err
	}
	defer app.Close()
	err = mcp.RunIO(ctx, app, key, in, out)
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil // client closed the session or the process was stopped
	}
	return err
}

// backup implements `gorun backup [--data dir] [--include-secrets]`: a
// consistent copy of the database (VACUUM INTO) and a gzip tar of media/,
// overrides/, extensions/ and form-uploads/, both under
// <DataDir>/backups/<UTC timestamp>. secret.key and gorun.toml are archived
// only with --include-secrets.
func backup(args []string, out io.Writer) error {
	includeSecrets, args := boolFlag(args, "include-secrets")
	retainStr, args, err := stringFlag(args, "retain")
	if err != nil {
		return err
	}
	retain := 0
	if retainStr != "" {
		n, atoiErr := strconv.Atoi(retainStr)
		if atoiErr != nil || n < 1 {
			return errors.New("backup: --retain needs an integer >= 1")
		}
		retain = n
	}
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	res, err := gpbackup.CreateWith(cfg.DataDir, cfg.DBPath(), time.Now(), gpbackup.Options{IncludeSecrets: includeSecrets, SecretKey: cfg.SecretKey})
	if err != nil {
		return err
	}
	report, err := gpbackup.InspectWith(res.Manifest, cfg.SecretKey)
	if err != nil {
		return fmt.Errorf("backup: verification: %w", err)
	}
	if report.Readiness == "blocked" || !report.IntegrityOK {
		return fmt.Errorf("backup: local verification failed (%s); previous copies preserved", strings.Join(report.Errors, ", "))
	}
	fmt.Fprintf(out, "database: %s\nfiles:    %s (%d files)\n", res.DB, res.Files, res.Count)
	fmt.Fprintf(out, "integrity: local checks passed (readiness=%s); cutover still needs secrets, themes, stopped writes, and any remote copy\n", report.Readiness)
	if includeSecrets {
		fmt.Fprintln(out, "secrets:  secret.key and gorun.toml are in the archive in plain text; keep it offline and encrypted")
	} else {
		fmt.Fprintln(out, "secrets:  not archived; keep secret.key / GORUN_SECRET_KEY separately (needed to restore encrypted settings and sessions)")
	}
	if retain >= 1 {
		removed, pruneErr := gpbackup.PruneVerifiedWith(cfg.DataDir, retain, cfg.SecretKey)
		if pruneErr != nil {
			return pruneErr
		}
		fmt.Fprintf(out, "retain:   kept %d newest verified pair(s); removed %d file(s)\n", retain, removed)
	}
	return nil
}

// optionsKeptNote is printed when a re-run reused a migration whose stored
// options differ from the flags given.
const optionsKeptNote = "note: re-run of an earlier import: its stored options are kept and the new --currency/--slug-policy are ignored (pass --reset-options to apply them)"

// valueFlags are the config.Load flags that take a value ("--data dir").
var valueFlags = map[string]bool{"env": true, "data": true, "base-url": true, "addr": true, "theme": true}

// importWXR implements `gorun import-wxr <file> [--dry-run]
// [--source-name NAME]`: boot every module (no job workers, so it is safe
// beside a running server), then preview or import the WXR export and
// print a summary. Any failure (including an import that does not end
// validated) is an error, so the process exits 1.
func importWXR(args []string, out io.Writer) error {
	dry, args := boolFlag(args, "dry-run")
	reset, args := boolFlag(args, "reset-options")
	name, args, err := stringFlag(args, "source-name")
	if err != nil {
		return err
	}
	var opts wpmigrate.Options
	if opts.Currency, args, err = stringFlag(args, "currency"); err != nil {
		return err
	}
	opts.Currency = strings.ToUpper(strings.TrimSpace(opts.Currency))
	if opts.SlugPolicy, args, err = stringFlag(args, "slug-policy"); err != nil {
		return err
	}
	var file string
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "-"):
			rest = append(rest, a)
			// Only the config flags that take a value consume the next
			// argument; booleans (--allow-private-net) never swallow the file.
			if valueFlags[strings.TrimLeft(a, "-")] && i+1 < len(args) {
				rest = append(rest, args[i+1])
				i++
			}
		case file == "":
			file = a
		default:
			return fmt.Errorf("import-wxr: unexpected argument %q", a)
		}
	}
	if file == "" {
		return errors.New("usage: gorun import-wxr <file> [--dry-run] [--source-name NAME] [--currency AED] [--slug-policy suffix|skip|overwrite] [--reset-options]")
	}
	abs, err := filepath.Abs(file) // the wizard re-reads the file on later runs
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("import-wxr: cannot read %s", file)
	}
	cfg, err := config.Load(rest)
	if err != nil {
		return err
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, Modules(cfg), kernel.BootOptions{})
	if err != nil {
		return err
	}
	defer app.Close()
	if dry {
		p, err := wpmigrate.PreviewWXR(ctx, app.Deps, wpmigrate.WXRFile{Path: abs, SourceName: name, ResetOptions: reset, Render: app.Handler}, opts)
		if err != nil {
			return fmt.Errorf("import-wxr: %w", err)
		}
		fmt.Fprintf(out, "preview: migration %s (dry run, nothing imported)\n", p.MigrationID)
		if p.OptionsKept {
			fmt.Fprintln(out, optionsKeptNote)
		}
		writeCounts(out, p.Counts)
		fmt.Fprintf(out, "slug conflicts: %d  media: %d files\n", len(p.SlugConflicts), p.MediaCount)
		for _, n := range p.Notes {
			fmt.Fprintf(out, "note: %s\n", n)
		}
		return nil
	}
	res, err := wpmigrate.ImportWXR(ctx, app.Deps, wpmigrate.WXRFile{Path: abs, SourceName: name, ResetOptions: reset, Render: app.Handler}, opts)
	if res != nil {
		fmt.Fprintf(out, "import: migration %s status %s\n", res.MigrationID, res.Status)
		if res.OptionsKept {
			fmt.Fprintln(out, optionsKeptNote)
		}
		writeCounts(out, res.Counts)
		if res.Report != nil {
			fmt.Fprintf(out, "report: broken links %d, missing media %d, render failures %d of %d checked\n",
				len(res.Report.BrokenLinks), len(res.Report.MissingMedia), len(res.Report.RenderFailures), res.Report.RenderChecked)
			fmt.Fprintf(out, "report: /admin/migrations/%s/report.md\n", res.MigrationID)
		}
	}
	if err != nil {
		return fmt.Errorf("import-wxr: %w", err)
	}
	return nil
}

// writeCounts prints kind → status → n, sorted.
func writeCounts(out io.Writer, counts map[string]map[string]int) {
	for _, kind := range slices.Sorted(maps.Keys(counts)) {
		parts := make([]string, 0, len(counts[kind]))
		for _, st := range slices.Sorted(maps.Keys(counts[kind])) {
			parts = append(parts, fmt.Sprintf("%s=%d", st, counts[kind][st]))
		}
		fmt.Fprintf(out, "  %-10s %s\n", kind, strings.Join(parts, " "))
	}
}
