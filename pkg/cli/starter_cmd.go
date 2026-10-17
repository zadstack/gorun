package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/starter"
)

// starterCmd implements `gorun starter [--data dir]`: imports the starter
// content (sample posts, pages, categories) into a site with no content
// yet. A site that has any content is left alone, so it is safe to repeat.
func starterCmd(args []string, out io.Writer) error {
	d, err := openDB(args)
	if err != nil {
		return err
	}
	var n int
	err = d.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM content WHERE status != 'trash'`).Scan(&n)
	d.Close()
	if err != nil {
		return err
	}
	if n > 0 {
		fmt.Fprintf(out, "starter: the site already has %d items; nothing imported\n", n)
		return nil
	}
	tmp, err := os.MkdirTemp("", "gorun-starter-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "starter.xml")
	if err := os.WriteFile(file, starter.WXR, 0o600); err != nil {
		return err
	}
	return importWXR(append([]string{file, "--source-name", "starter"}, args...), out)
}

// userLoginLink implements `gorun user login-link --email E [--ttl 2m]`:
// prints a single-use sign-in link for an active user (the same link the
// admin's "send sign-in link" mails), for a manager on the server to open.
func userLoginLink(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("user login-link", flag.ContinueOnError)
	email := fs.String("email", "", "email address of an active user (required)")
	ttl := fs.Duration("ttl", 2*time.Minute, "how long the link works (at most 15m)")
	dataDir := fs.String("data", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email is required")
	}
	if *ttl <= 0 || *ttl > auth.DefaultMagicLinkTTL {
		return fmt.Errorf("--ttl must be between 1s and %s", auth.DefaultMagicLinkTTL)
	}
	var cfgArgs []string
	if *dataDir != "" {
		cfgArgs = []string{"--data", *dataDir}
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
	if u.Status != "active" {
		return fmt.Errorf("user %s is disabled", *email)
	}
	d, err := openDB(cfgArgs)
	if err != nil {
		return err
	}
	defer d.Close()
	token, err := auth.NewMagicLink(ctx, &kernel.Deps{DB: d}, u.ID, *ttl)
	if err != nil {
		return err
	}
	base := ""
	var raw string
	if d.QueryRowContext(ctx, `SELECT json_extract(value_json, '$') FROM settings WHERE key = 'site.base_url'`).Scan(&raw) == nil {
		base = raw
	}
	if base == "" {
		cfg, err := config.Load(cfgArgs)
		if err != nil {
			return err
		}
		base = cfg.BaseURL
	}
	fmt.Fprintf(out, "%s/auth/magic/%s\n", base, token)
	return nil
}
