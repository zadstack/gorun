package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/webhooks"
)

const webhookUsage = "usage: gorun webhook list | events | create --name N --url URL --events a,b|* | update <id> [--name N] [--url URL] [--events a,b] [--active true|false] | delete <id> | test <id> | secret <id> | deliveries <id> [--limit N] | redeliver <delivery-id> [--data dir]"

func webhookHelpArgs(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

// webhookCmd manages outgoing webhooks like the admin and API do. Secrets
// are printed once (create, secret). Deliveries queued here (redeliver) are
// sent by the running server's job workers; test sends synchronously.
func webhookCmd(args []string, out io.Writer) error {
	if len(args) == 0 || webhookHelpArgs(args) {
		return errors.New(webhookUsage)
	}
	sub, args := args[0], args[1:]
	var id string
	switch sub {
	case "update", "delete", "test", "secret", "deliveries", "redeliver":
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return errors.New(webhookUsage)
		}
		id, args = args[0], args[1:]
	}
	flags := map[string]string{}
	set := map[string]bool{}
	for _, f := range []string{"name", "url", "events", "active", "limit"} {
		v, rest, err := stringFlag(args, f)
		if err != nil {
			return err
		}
		set[f] = len(rest) != len(args)
		flags[f], args = v, rest
	}
	if webhookHelpArgs(args) {
		return errors.New(webhookUsage)
	}
	if sub == "events" {
		return printJSON(out, webhooks.Events())
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
	svc, ok := kernel.Get[webhooks.Service](app.Deps, kernel.ServiceWebhooks)
	if !ok {
		return errors.New("webhook: service missing")
	}
	in := webhooks.Input{}
	if set["name"] {
		in.Name = &[]string{flags["name"]}[0]
	}
	if set["url"] {
		in.URL = &[]string{flags["url"]}[0]
	}
	if set["events"] {
		in.Events = strings.Split(flags["events"], ",")
	}
	if set["active"] {
		b, err := strconv.ParseBool(flags["active"])
		if err != nil {
			return errors.New("--active is true or false")
		}
		in.Active = &b
	}
	var verr *webhooks.ValidationError
	explain := func(err error) error {
		if errors.As(err, &verr) {
			return fmt.Errorf("webhook: invalid %v", verr.Fields)
		}
		return err
	}
	switch sub {
	case "list":
		list, err := svc.List(ctx)
		if err != nil {
			return err
		}
		return printJSON(out, webhooks.Redacted(list)) // query strings may hold tokens
	case "create":
		hook, secret, err := svc.Create(ctx, in)
		if err != nil {
			return explain(err)
		}
		return printJSON(out, map[string]any{"webhook": hook, "secret": secret})
	case "update":
		hook, err := svc.Update(ctx, id, in)
		if err != nil {
			return explain(err)
		}
		return printJSON(out, hook)
	case "delete":
		if err := svc.Delete(ctx, id); err != nil {
			return err
		}
		fmt.Fprintf(out, "deleted %s\n", id)
	case "test":
		d, err := svc.Test(ctx, id)
		if err != nil {
			return err
		}
		if err := printJSON(out, d); err != nil {
			return err
		}
		if d.Status != "delivered" {
			return &exitError{code: 2, msg: "webhook test failed: " + d.Error}
		}
	case "secret":
		secret, err := svc.RotateSecret(ctx, id)
		if err != nil {
			return err
		}
		return printJSON(out, map[string]string{"id": id, "secret": secret})
	case "deliveries":
		limit := 50
		if flags["limit"] != "" {
			if limit, err = strconv.Atoi(flags["limit"]); err != nil || limit < 1 || limit > 200 {
				return errors.New("--limit is 1 to 200")
			}
		}
		list, err := svc.Deliveries(ctx, id, limit)
		if err != nil {
			return err
		}
		return printJSON(out, list)
	case "redeliver":
		d, err := svc.Redeliver(ctx, id)
		if err != nil {
			return err
		}
		return printJSON(out, d)
	default:
		return errors.New(webhookUsage)
	}
	return nil
}
