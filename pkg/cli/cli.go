// Package cli is the GoRun command line. cmd/gorun is a thin wrapper around
// Main; a downstream binary can call Main with Options to add modules and
// commands while keeping every core command and flag unchanged.
package cli

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
)

// version is printed by "gorun version" and reported as the API core
// version; set it with WithVersion (cmd/gorun passes its -X main.version).
var version = "dev"

const (
	shutdownTimeout   = 15 * time.Second
	readHeaderTimeout = 10 * time.Second
)

// usageHead and usageTail frame the command list; commands added with
// WithCommands are listed between them (before api-docs).
const usageHead = `usage: gorun <command> [flags]

commands:
  doctor        inspect configuration, SQLite, themes, modules and port without migrations [--json]
  start         prepare and run a local site (LAN preview; --local-only disables LAN)
  serve         run the HTTP server
  migrate       apply database migrations and exit
  user create   create a staff user (--email (--password | --password-stdin) [--name] [--admin])
  user login-link  print a single-use sign-in link (--email [--ttl 2m], at most 15m)
  user status   turn a user off or on (--email --status active|disabled; not the last active admin)
  user password set a user's password and end their sessions (--email, --password or --password-stdin)
  healthcheck   GET /healthz on the local server (for container healthchecks)
  reindex       rebuild the content search index and relation rows ([--no-jobs])
  mcp           serve MCP over stdio as an API key (--key gp_… or GORUN_MCP_KEY); no job
                workers unless --jobs (the running server executes the jobs)
  backup-preview verify and open a disposable localhost-only read-only preview (--input <stamp>.manifest.json)
  backup-inspect inspect a local pair without writes (--input <timestamp>.manifest.json)
  backup-restore restore a local pair into a new directory path that must not exist (--input, --dest; never merges)
  backup-decrypt decrypt a .gorb backup with the original site secret (--input, --output; never overwrite)
  backup        copy the database (VACUUM INTO) and archive media/, overrides/, extensions/
                to <data>/backups/<timestamp>.{db,tar.gz}; secret.key and gorun.toml only
                with --include-secrets (plain text: keep that archive offline); optional
                --retain N keeps the newest N locally verified pairs and deletes older ones
  staging       staging seed|promote (--from <source-data> | --input <stamp>.manifest.json)
                --dest <new-directory-path> [--include-secrets]: backup (optional) + restore
                into a new data dir for a staging site or promote-to-live cutover; never merges
  import-wxr    import a WordPress WXR export: import-wxr <file> [--dry-run] [--source-name NAME]
                [--currency AED] [--slug-policy suffix|skip|overwrite] [--reset-options]
  theme         theme info (JSON) | doctor [slug] [--json] [--warnings] | activate <slug> | show <tpl> |
                save <tpl> [--draft] (stdin) | reset <tpl> | reparent <child> <parent> [--apply]
                (report first, backs up theme.json): active theme + overrides, or filesystem
                contract checks via pkg/themekit (doctor needs no database)
  key           key console: key list | create --name N --user EMAIL --scopes a,b|* [--kind api|agent]
                [--rate N] [--expires DATE] | revoke <id> | rotate <id> [--grace 1h] (tokens printed once)
  webhook       webhook list | events | create --name N --url URL --events a,b|* | update <id> [...] |
                delete <id> | test <id> | secret <id> | deliveries <id> [--limit N] | redeliver <delivery-id>
  content       content list [--type T] [--limit N]: items (no trash) as JSON with their public links
  starter       import the starter content (sample posts and pages) into a site with no content
  info          the site's facts as JSON: title, theme, users (no secrets), content counts, sizes
  ai            ai set --provider openai|anthropic --model M [--base-url URL] [--api-key-stdin] | ai off:
                AI writing in the editor, validated like Settings → AI (the key only from stdin)
  settings      recovery: settings get <key> | set <key> <json-or-string> | unset <key>
                (validated like the admin, audited as system; no workers or themes needed)
`

const usageTail = `  api-docs      generate current OpenAPI operation tables (--check FILE | --update FILE)
  version       print the version
`

// usage is the core help text (no extra commands).
const usage = usageHead + usageTail

// exitError carries a specific process exit code; main prints its message
// without the "gorun:" prefix.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func notInstalled(name string) error {
	return &exitError{code: 2, msg: name + ": not available in this build (module not installed)"}
}

// Main runs the command line with args (without the program name) and returns
// the process exit code. Options are applied first (see Configure).
func Main(args []string, opts ...Option) int {
	Configure(opts...)
	if err := run(args); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			fmt.Fprintln(os.Stderr, ee.msg)
			return ee.code
		}
		fmt.Fprintln(os.Stderr, "gorun:", err)
		return 1
	}
	return 0
}

// Run dispatches one command with the options applied by Configure, reading
// stdin from in and writing command output to out.
func Run(args []string, in io.Reader, out io.Writer) error { return runIO(args, in, out) }

func run(args []string) error { return runIO(args, os.Stdin, os.Stdout) }

func runIO(args []string, in io.Reader, out io.Writer) error {
	api.CoreVersion = version
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, helpText())
		return errors.New("missing command")
	}
	if c, ok := extraCommand(args[0]); ok {
		return c.Run(args[1:], in, out)
	}
	switch args[0] {
	case "doctor":
		return doctorCmd(args[1:], out)
	case "start":
		return start(args[1:], out)
	case "serve":
		return serve(args[1:])
	case "migrate":
		return migrate(args[1:])
	case "user":
		if len(args) >= 2 && args[1] == "create" {
			return userCreate(args[2:])
		}
		if len(args) >= 2 && args[1] == "password" {
			return userPassword(args[2:], in, out)
		}
		if len(args) >= 2 && args[1] == "status" {
			return userStatus(args[2:], out)
		}
		if len(args) >= 2 && args[1] == "login-link" {
			return userLoginLink(args[2:], out)
		}
		return errors.New("usage: gorun user create --email --password [--name] [--admin] | user password --email (--password | --password-stdin)")
	case "healthcheck":
		return healthcheck(args[1:])
	case "reindex":
		return reindex(args[1:], out)
	case "mcp":
		return mcpStdio(args[1:], readCloser(in), writeCloser(out))
	case "backup-preview":
		return backupPreviewCmd(args[1:], out)
	case "backup-inspect":
		return backupInspect(args[1:], out)
	case "backup-restore":
		return backupRestore(args[1:], out)
	case "backup-decrypt":
		return backupDecrypt(args[1:])
	case "backup":
		return backup(args[1:], out)
	case "staging":
		return stagingCmd(args[1:], out)
	case "settings":
		return settingsCmd(args[1:], out)
	case "ai":
		return aiCmd(args[1:], in, out)
	case "info":
		return infoCmd(args[1:], out)
	case "starter":
		return starterCmd(args[1:], out)
	case "content":
		return contentCmd(args[1:], out)
	case "theme":
		return themeCmd(args[1:], in, out)
	case "key":
		return keyCmd(args[1:], out)
	case "webhook":
		return webhookCmd(args[1:], out)
	case "import-wxr":
		return importWXR(args[1:], out)
	case "api-docs":
		return apiDocsCmd(args[1:], out)
	case "version":
		fmt.Fprintf(out, "GoRun %s\n", version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(out, helpText())
		return nil
	default:
		fmt.Fprint(os.Stderr, helpText())
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func serve(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app, err := kernel.Boot(ctx, cfg, Modules(cfg))
	if err != nil {
		return err
	}
	defer app.Close()
	srv := &http.Server{Addr: cfg.Addr, Handler: app.Handler, ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	app.Deps.Log.Info("gorun listening", "addr", cfg.Addr, "env", cfg.Env, "version", version)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func migrate(args []string) error {
	d, err := openDB(args)
	if err != nil {
		return err
	}
	defer d.Close()
	fmt.Println("migrations applied")
	return nil
}

// openDB loads the config from args (e.g. --data), opens its database and
// applies every module's migrations.
func openDB(args []string) (*sql.DB, error) {
	cfg, err := config.Load(args)
	if err != nil {
		return nil, err
	}
	d, err := db.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	if err := kernel.MigrateAll(context.Background(), d, Modules(cfg)); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// reindex boots the app (migrations included) and rebuilds the content
// search index and relation rows of every item, then reports what it did.
// --no-jobs boots without job workers (safe beside a running server).
func reindex(args []string, out io.Writer) error {
	noJobs, args := boolFlag(args, "no-jobs")
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, Modules(cfg), kernel.BootOptions{StartJobs: !noJobs})
	if err != nil {
		return err
	}
	defer app.Close()
	cs, ok := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	if !ok {
		return notInstalled("reindex")
	}
	types, err := cs.Types(ctx)
	if err != nil {
		return err
	}
	var items int
	if err := app.Deps.DB.QueryRowContext(ctx, `SELECT count(*) FROM content`).Scan(&items); err != nil {
		return fmt.Errorf("reindex: count: %w", err)
	}
	start := time.Now()
	if err := cs.Reindex(ctx); err != nil {
		return fmt.Errorf("reindex: %w", err)
	}
	fmt.Fprintf(out, "reindexed %d items across %d content types in %s\n", items, len(types), time.Since(start).Round(time.Millisecond))
	return nil
}

// userCreate creates a staff user: role admin with --admin, else editor.
// Remaining flags (e.g. --data) go to config.Load.
func userCreate(args []string) error {
	fs := flag.NewFlagSet("user create", flag.ContinueOnError)
	email := fs.String("email", "", "email address (required)")
	password := fs.String("password", "", "password, at least 10 characters (required)")
	fromStdin := fs.Bool("password-stdin", false, "read the password from the first line of stdin")
	name := fs.String("name", "", "display name")
	admin := fs.Bool("admin", false, "grant the admin role (default: editor)")
	dataDir := fs.String("data", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fromStdin {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read password: %w", err)
		}
		*password = strings.TrimRight(line, "\r\n")
	}
	if *email == "" || *password == "" {
		return errors.New("--email and --password (or --password-stdin) are required")
	}
	users, closeDB, err := openUsers(*dataDir)
	if err != nil {
		return err
	}
	defer closeDB()
	ctx := context.Background()
	role := "editor"
	if *admin {
		role = auth.RoleAdmin
	}
	u, err := users.Create(ctx, *email, *name, *password, role)
	if err != nil {
		return err
	}
	fmt.Printf("created %s user %s (%s)\n", u.Role, u.Email, u.ID)
	return nil
}

// userPassword sets a user's password (recovery when nobody can sign in).
// Update ends the user's sessions and audits the change as system.
func userPassword(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("user password", flag.ContinueOnError)
	email := fs.String("email", "", "email address of an existing user (required)")
	password := fs.String("password", "", "new password, at least 10 characters")
	fromStdin := fs.Bool("password-stdin", false, "read the new password from the first line of stdin")
	dataDir := fs.String("data", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fromStdin {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read password: %w", err)
		}
		*password = strings.TrimRight(line, "\r\n")
	}
	if *email == "" || *password == "" {
		return errors.New("--email and --password (or --password-stdin) are required")
	}
	users, closeDB, err := openUsers(*dataDir)
	if err != nil {
		return err
	}
	defer closeDB()
	ctx := context.Background()
	u, err := users.GetByEmail(ctx, *email)
	if err != nil {
		return fmt.Errorf("user %s: %w", *email, err)
	}
	if _, err := users.Update(ctx, u.ID, auth.UserPatch{Password: password}); err != nil {
		return err
	}
	fmt.Fprintf(out, "password updated for %s (%s); their sessions were ended\n", u.Email, u.ID)
	return nil
}

// openUsers opens the database of dataDir (default from config) and returns
// the user service with an audit log on stderr.
func openUsers(dataDir string) (auth.Service, func(), error) {
	var args []string
	if dataDir != "" {
		args = []string{"--data", dataDir}
	}
	d, err := openDB(args)
	if err != nil {
		return nil, nil, err
	}
	auditor := audit.New(d, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return auth.NewService(d, auditor), func() { d.Close() }, nil
}

// healthcheck probes /healthz without loading the full config, so it has no
// side effects (no secret generation). Address: --addr > GORUN_ADDR > :8080.
func healthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	addr := fs.String("addr", "", "server listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *addr == "" {
		*addr = os.Getenv("GORUN_ADDR")
	}
	if *addr == "" {
		*addr = ":8080"
	}
	if strings.HasPrefix(*addr, ":") {
		*addr = "127.0.0.1" + *addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + *addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: %s", resp.Status)
	}
	return nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// readCloser and writeCloser keep os.Stdin/os.Stdout (already closers) as
// they are and wrap other streams for mcp's stdio transport.
func readCloser(r io.Reader) io.ReadCloser {
	if rc, ok := r.(io.ReadCloser); ok {
		return rc
	}
	return io.NopCloser(r)
}

func writeCloser(w io.Writer) io.WriteCloser {
	if wc, ok := w.(io.WriteCloser); ok {
		return wc
	}
	return nopWriteCloser{w}
}
