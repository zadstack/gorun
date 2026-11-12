package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// userTwoFactorReset implements `gorun user two-factor-reset --email E`:
// removes a user's second factors (authenticator app and passkeys), recovery
// codes and pending sign-ins when
// nobody who could reset it in the admin can sign in (the only administrator
// lost their phone, or GORUN_SECRET_KEY changed). It works for any user,
// including the last administrator, and is audited as user.2fa_reset with
// {"via":"cli"}. The password and sessions are kept.
func userTwoFactorReset(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("user two-factor-reset", flag.ContinueOnError)
	email := fs.String("email", "", "email address of an existing user (required)")
	dataDir := fs.String("data", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email is required")
	}
	var cfgArgs []string
	if *dataDir != "" {
		cfgArgs = []string{"--data", *dataDir}
	}
	d, err := openDB(cfgArgs)
	if err != nil {
		return err
	}
	defer d.Close()
	ctx := context.Background()
	auditor := audit.New(d, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	u, err := auth.NewService(d, auditor).GetByEmail(ctx, *email)
	if err != nil {
		return fmt.Errorf("user %s: %w", *email, err)
	}
	removed, passkeys, err := auth.ResetTwoFactorFromCLI(ctx, &kernel.Deps{DB: d, Audit: auditor}, u.ID)
	if err != nil {
		return err
	}
	if !removed {
		fmt.Fprintf(out, "%s had no two-factor authentication; nothing changed\n", u.Email)
		return nil
	}
	fmt.Fprintf(out, "two-factor authentication removed for %s (%s): authenticator app, %d passkeys, recovery codes and pending sign-ins deleted, "+
		"password and sessions were kept; audited as user.2fa_reset\n", u.Email, u.ID, passkeys)
	return nil
}
