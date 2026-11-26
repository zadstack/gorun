package cli

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

const mailSetUsage = "usage: gorun mail set --provider smtp|resend [--host H] [--port N] [--tls starttls|tls|none] [--username U] [--from ADDR] [--from-name N] [--reply-to ADDR] [--secret-stdin | --clear-secret] [--data dir]"

// mailSetCmd stores Settings → Mail from the shell, for a manager that sets
// a site's mail without its admin session. It validates with the admin's own
// admin.MailConfig, so it stores nothing the form would refuse. The secret
// (SMTP password or Resend API key) comes from the first line of stdin only,
// never from the command line, is stored encrypted and is never printed.
// Like saving the form, every field is replaced; the stored secret is kept
// unless --secret-stdin or --clear-secret is given.
func mailSetCmd(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("mail set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	provider := fs.String("provider", "", "smtp or resend")
	host := fs.String("host", "", "SMTP server")
	port := fs.Int("port", 0, "SMTP port (587 when 0)")
	tls := fs.String("tls", mailer.TLSStartTLS, "starttls, tls (implicit) or none")
	username := fs.String("username", "", "SMTP user")
	from := fs.String("from", "", "sender address")
	fromName := fs.String("from-name", "", "sender name")
	replyTo := fs.String("reply-to", "", "reply-to address")
	secretStdin := fs.Bool("secret-stdin", false, "read the SMTP password or Resend API key from the first line of stdin")
	clearSecret := fs.Bool("clear-secret", false, "remove the stored secret")
	dataDir := fs.String("data", "", "data directory")
	if err := fs.Parse(args); err != nil || len(fs.Args()) != 0 || *provider == "" || (*secretStdin && *clearSecret) {
		return errors.New(mailSetUsage)
	}
	secret := ""
	if *secretStdin {
		line, err := bufio.NewReader(io.LimitReader(in, 8192)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return errors.New("mail: cannot read the secret from stdin")
		}
		if secret = strings.TrimSpace(line); secret == "" {
			return errors.New("mail: --secret-stdin read an empty secret")
		}
	}
	var cfgArgs []string
	if *dataDir != "" {
		cfgArgs = []string{"--data", *dataDir}
	}
	cfg, err := config.Load(cfgArgs)
	if err != nil {
		return err
	}
	input := admin.MailInput{Provider: *provider, Host: *host, Username: *username, From: *from, FromName: *fromName, ReplyTo: *replyTo, TLS: *tls}
	if *port != 0 {
		input.Port = strconv.Itoa(*port)
	}
	if *provider == "resend" {
		input.ResendAPIKey, input.ClearResendAPIKey = secret, *clearSecret
	} else {
		input.Password, input.ClearPassword = secret, *clearSecret
	}
	vals, fields := admin.MailConfig(input, cfg.SecretKey)
	if len(fields) > 0 {
		names := make([]string, 0, len(fields))
		for f := range fields {
			names = append(names, f)
		}
		sort.Strings(names)
		return fmt.Errorf("mail: invalid %s", strings.Join(names, ", "))
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if err := db.InTx(ctx, app.Deps.DB, func(tx *sql.Tx) error {
		for _, k := range keys {
			if err := app.Deps.Settings.SetTx(ctx, tx, k, vals[k]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	app.Deps.Audit.Record(ctx, "settings.update", "settings", "", map[string]any{"keys": keys, "via": "cli"})
	what := *provider
	if *provider == "smtp" {
		what += " " + strings.TrimSpace(*host)
	}
	fmt.Fprintf(out, "mail: %s\n", what)
	return nil
}
