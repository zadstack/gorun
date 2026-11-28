package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/services"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

const serviceUsage = "usage: gorun service list | get <name> | set <name> key=value… [--secret-stdin] | test <name> [--to ADDR] | run <name> | off <name>  [--data dir]"

// serviceTimeout bounds one command; a mail test talks to the provider.
const serviceTimeout = 45 * time.Second

// serviceRunTimeout bounds a run: a backup, then its upload.
const serviceRunTimeout = 15 * time.Minute

// serviceModules are the modules a service command boots. The six
// services (mail, storage, chat, analytics, backups, metricool) keep their
// state in settings only: mail.*, the storage.active.* destinations and the
// Connections profiles, chat.*, analytics.*, backup.*, metricool.config. The settings module
// opens that store; the kernel runs every core migration, so audit and the
// settings table are there. No workers start: a CLI change must not replay
// unrelated jobs.
func serviceModules() []kernel.Module { return []kernel.Module{settingsmod.New()} }

// serviceArgs is a parsed service command line.
type serviceArgs struct {
	name        string
	values      map[string]string
	dataDir     string
	to          string
	secretStdin bool
}

// parseServiceArgs reads the service name, key=value pairs (set only) and
// the flags each subcommand takes. Flags may come anywhere after the name.
func parseServiceArgs(sub string, args []string) (serviceArgs, error) {
	a := serviceArgs{values: map[string]string{}}
	usage := errors.New(serviceUsage)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flagName, val, hasVal := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			next := func() (string, error) {
				if hasVal {
					return val, nil
				}
				if i+1 >= len(args) {
					return "", usage
				}
				i++
				return args[i], nil
			}
			var err error
			switch {
			case flagName == "data":
				a.dataDir, err = next()
			case flagName == "to" && sub == "test":
				a.to, err = next()
			case flagName == "secret-stdin" && sub == "set" && !hasVal:
				a.secretStdin = true
			default:
				return a, fmt.Errorf("service: unknown flag %s\n%s", "--"+flagName, serviceUsage)
			}
			if err != nil {
				return a, err
			}
			continue
		}
		if a.name == "" && sub != "list" {
			a.name = arg
			continue
		}
		k, v, ok := strings.Cut(arg, "=")
		if sub != "set" || !ok || strings.TrimSpace(k) == "" {
			return a, usage
		}
		if _, dup := a.values[k]; dup {
			return a, fmt.Errorf("service: %s given twice", k)
		}
		a.values[k] = v
	}
	if (sub == "list") != (a.name == "") {
		return a, usage
	}
	return a, nil
}

// serviceCmd is gorun service: one shape for the site's services, the same
// the MCP toolset and the REST API use. A secret comes from the first line
// of stdin only (--secret-stdin), never from an argument, and no output
// carries it. Changes are audited as service.update via cli.
func serviceCmd(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(serviceUsage)
	}
	sub := args[0]
	switch sub {
	case "list", "get", "set", "test", "run", "off":
	default:
		return errors.New(serviceUsage)
	}
	a, err := parseServiceArgs(sub, args[1:])
	if err != nil {
		return err
	}
	var secret *string
	if a.secretStdin {
		s, err := readServiceSecret(in)
		if err != nil {
			return err
		}
		secret = &s
	}
	timeout := serviceTimeout
	if sub == "run" {
		timeout = serviceRunTimeout
	}
	return withServices(a.dataDir, timeout, func(ctx context.Context, r *services.Registry) error {
		switch sub {
		case "list":
			list, err := r.List(ctx)
			if err != nil {
				return err
			}
			return writeServiceJSON(out, struct {
				Services []services.Status `json:"services"`
			}{list})
		case "get":
			st, err := r.Status(ctx, a.name)
			if errors.Is(err, services.ErrUnknownService) {
				return unknownService(a.name)
			}
			if err != nil {
				return err
			}
			return writeServiceJSON(out, st)
		case "run":
			return runService(ctx, r, a.name, out)
		case "set":
			return setService(ctx, r, a.name, a.values, secret, out)
		case "test":
			return testService(ctx, r, a.name, a.to, out)
		default: // off
			if err := r.Off(ctx, a.name, "cli"); err != nil {
				if errors.Is(err, services.ErrUnknownService) {
					return unknownService(a.name)
				}
				return err
			}
			fmt.Fprintf(out, "service %s: %s\n", a.name, services.StateOff)
			return nil
		}
	})
}

// readServiceSecret is the first line of stdin, trimmed; an empty line is refused.
func readServiceSecret(in io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(in, 8192)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("service: cannot read the secret from stdin")
	}
	s := strings.TrimSpace(line)
	if s == "" {
		return "", errors.New("service: --secret-stdin read an empty secret")
	}
	return s, nil
}

// withServices boots the service modules on dataDir and runs fn over the
// six services within timeout.
func withServices(dataDir string, timeout time.Duration, fn func(context.Context, *services.Registry) error) error {
	var cfgArgs []string
	if dataDir != "" {
		cfgArgs = []string{"--data", dataDir}
	}
	cfg, err := config.Load(cfgArgs)
	if err != nil {
		return err
	}
	return withServicesConfig(cfg, timeout, fn)
}

// withServicesConfig is withServices for a loaded configuration.
func withServicesConfig(cfg config.Config, timeout time.Duration, fn func(context.Context, *services.Registry) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	app, err := kernel.BootWithOptions(ctx, cfg, serviceModules(), kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	return fn(ctx, services.Default(app.Deps))
}

// setService applies values (and secret, when not nil) to the service
// named name and prints its new state. Field errors name the fields only.
func setService(ctx context.Context, r *services.Registry, name string, values map[string]string, secret *string, out io.Writer) error {
	s, ok := r.Get(name)
	if !ok {
		return unknownService(name)
	}
	// A secret never comes as a value, whichever provider it belongs to.
	if fields := services.SecretValueFields(s, values); len(fields) > 0 {
		return invalidFields(fields)
	}
	if secret != nil && !services.TakesSecret(s) {
		return fmt.Errorf("service: %s takes no secret", name)
	}
	st, fields, err := r.Set(ctx, name, values, secret, "cli")
	if err != nil {
		return err
	}
	if len(fields) > 0 {
		return invalidFields(fields)
	}
	line := "service " + name + ": " + st.State
	if len(st.Missing) > 0 {
		line += " (missing " + strings.Join(st.Missing, ", ") + ")"
	}
	fmt.Fprintln(out, line)
	return nil
}

// testService runs the service's safe test and prints the result as JSON;
// a test that did not pass exits non-zero.
func testService(ctx context.Context, r *services.Registry, name, to string, out io.Writer) error {
	s, ok := r.Get(name)
	if !ok {
		return unknownService(name)
	}
	if name == "mail" && to == "" {
		return errors.New("service: test mail needs --to ADDR")
	}
	res, err := r.Test(ctx, s.Name(), to)
	if err != nil {
		return fmt.Errorf("service: test %s: %w", name, err)
	}
	if err := writeServiceJSON(out, res); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("service: test %s did not pass: %s", name, res.Message)
	}
	return nil
}

// runService takes the service's run action now and prints the result as
// JSON; a run that did not pass (a failed upload) exits non-zero.
func runService(ctx context.Context, r *services.Registry, name string, out io.Writer) error {
	res, err := r.Run(ctx, name, "cli")
	switch {
	case errors.Is(err, services.ErrUnknownService):
		return unknownService(name)
	case errors.Is(err, services.ErrNoRun):
		return fmt.Errorf("service: %s has no run action (backups has one)", name)
	case err != nil:
		return fmt.Errorf("service: run %s: %w", name, err)
	}
	if err := writeServiceJSON(out, res); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("service: run %s did not pass: %s", name, res.Message)
	}
	return nil
}

func invalidFields(fields map[string]string) error {
	names := make([]string, 0, len(fields))
	for f := range fields {
		names = append(names, f)
	}
	sort.Strings(names)
	return fmt.Errorf("service: invalid %s", strings.Join(names, ", "))
}

func unknownService(name string) error {
	return fmt.Errorf("service: unknown service %q (mail, storage, chat, analytics, backups, metricool)", name)
}

func writeServiceJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
