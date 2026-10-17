package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

const keyUsage = "usage: gorun key list | create --name N --user EMAIL --scopes a,b|* [--kind api|agent] [--rate N] [--expires YYYY-MM-DD|RFC3339] | revoke <id> | rotate <id> [--grace 1h] [--data dir]"

// cliKey is a key as the CLI prints it (never the hash; the token only once).
type cliKey struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Prefix      string   `json:"prefix"`
	Kind        string   `json:"kind"`
	UserID      string   `json:"user_id"`
	Scopes      []string `json:"scopes"`
	RatePerMin  int      `json:"rate_per_min"`
	ExpiresAt   string   `json:"expires_at"`
	LastUsedAt  string   `json:"last_used_at"`
	UseCount    int64    `json:"use_count"`
	CreatedAt   string   `json:"created_at"`
	RevokedAt   string   `json:"revoked_at"`
	RotatedFrom string   `json:"rotated_from"`
}

func toCLIKey(k auth.APIKey) cliKey {
	return cliKey{k.ID, k.Name, k.Prefix, k.Kind, k.UserID, k.Scopes, k.RatePerMin, k.ExpiresAt, k.LastUsedAt, k.UseCount, k.CreatedAt, k.RevokedAt, k.RotatedFrom}
}

func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// cliExpiry accepts YYYY-MM-DD (valid through that day, UTC) or RFC3339.
func cliExpiry(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return t.Add(24*time.Hour - time.Second).UTC().Format(time.RFC3339), nil
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return "", errors.New("--expires is YYYY-MM-DD or RFC3339")
	}
	return v, nil
}

// keyCmd is the key console on the command line: list, create, revoke and
// rotate API/agent keys. Tokens are printed once and never stored.
func keyCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(keyUsage)
	}
	sub, args := args[0], args[1:]
	var id string
	if sub == "revoke" || sub == "rotate" {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return errors.New(keyUsage)
		}
		id, args = args[0], args[1:]
	}
	flags := map[string]string{}
	for _, f := range []string{"name", "user", "scopes", "kind", "rate", "expires", "grace"} {
		v, rest, err := stringFlag(args, f)
		if err != nil {
			return err
		}
		flags[f], args = v, rest
	}
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, Modules(cfg), kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	keys, ok := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	users, ok2 := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if !ok || !ok2 {
		return errors.New("key: auth services missing")
	}
	switch sub {
	case "list":
		list, err := keys.List(ctx)
		if err != nil {
			return err
		}
		outKeys := []cliKey{}
		for _, k := range list {
			outKeys = append(outKeys, toCLIKey(k))
		}
		return printJSON(out, outKeys)
	case "create":
		if flags["user"] == "" || flags["name"] == "" || flags["scopes"] == "" {
			return errors.New(keyUsage)
		}
		u, err := users.GetByEmail(ctx, flags["user"])
		if err != nil {
			return fmt.Errorf("key: user %q: %w", flags["user"], err)
		}
		kind := flags["kind"]
		if kind == "" {
			kind = auth.KeyKindAPI
		}
		rate := 0
		if flags["rate"] != "" {
			if rate, err = strconv.Atoi(flags["rate"]); err != nil || rate < 1 || rate > 10000 {
				return errors.New("--rate is 1 to 10000 per minute")
			}
		}
		expires, err := cliExpiry(flags["expires"])
		if err != nil {
			return err
		}
		scopes := strings.Split(flags["scopes"], ",")
		for i := range scopes {
			scopes[i] = strings.TrimSpace(scopes[i])
		}
		plain, k, err := keys.CreateKind(ctx, kind, flags["name"], u.ID, scopes, rate, expires)
		if err != nil {
			var ve *auth.ValidationError
			if errors.As(err, &ve) {
				return fmt.Errorf("key: invalid %v", ve.Fields)
			}
			return err
		}
		return printJSON(out, map[string]any{"key": toCLIKey(*k), "token": plain})
	case "revoke":
		if err := keys.Revoke(ctx, id); err != nil {
			return err
		}
		fmt.Fprintf(out, "revoked %s\n", id)
	case "rotate":
		var grace time.Duration
		if flags["grace"] != "" {
			if grace, err = time.ParseDuration(flags["grace"]); err != nil {
				return errors.New("--grace is a duration such as 0, 30m or 24h")
			}
		}
		plain, k, err := keys.Rotate(ctx, id, grace)
		if err != nil {
			return err
		}
		return printJSON(out, map[string]any{"key": toCLIKey(*k), "token": plain})
	default:
		return errors.New(keyUsage)
	}
	return nil
}
