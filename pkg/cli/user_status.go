package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/zadstack/gorun/internal/auth"
)

// userStatus implements `gorun user status --email E --status active|disabled`:
// turns a staff account off or on. The last active administrator cannot
// be disabled (nobody could sign in afterwards).
func userStatus(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("user status", flag.ContinueOnError)
	email := fs.String("email", "", "email address of an existing user (required)")
	status := fs.String("status", "", "active or disabled (required)")
	dataDir := fs.String("data", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" || (*status != "active" && *status != "disabled") {
		return errors.New("--email and --status active|disabled are required")
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
	if *status == "disabled" && u.Role == auth.RoleAdmin && u.Status == "active" {
		list, _, err := users.List(ctx, auth.ListQuery{Page: 1, PerPage: 1000})
		if err != nil {
			return err
		}
		active := 0
		for _, x := range list {
			if x.Role == auth.RoleAdmin && x.Status == "active" {
				active++
			}
		}
		if active <= 1 {
			return errors.New("this is the last active administrator; add another one first")
		}
	}
	if _, err := users.Update(ctx, u.ID, auth.UserPatch{Status: status}); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s is now %s\n", u.Email, *status)
	return nil
}
