package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

const aiUsage = "usage: gorun ai set --provider openai|anthropic --model M [--base-url URL] [--api-key-stdin] [--data dir] | ai off [--data dir]"

// aiCmd configures AI writing from the shell (GoRun Manager uses it):
// the same validation as Settings → AI; the API key comes from stdin only,
// never from the command line, and is stored encrypted.
func aiCmd(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || (args[0] != "set" && args[0] != "off") {
		return errors.New(aiUsage)
	}
	fs := flag.NewFlagSet("ai "+args[0], flag.ContinueOnError)
	provider := fs.String("provider", "", "openai or anthropic")
	model := fs.String("model", "", "model name")
	baseURL := fs.String("base-url", "", "explicit OpenAI-compatible base URL (required for openai)")
	keyStdin := fs.Bool("api-key-stdin", false, "read the API key from the first line of stdin")
	dataDir := fs.String("data", "", "data directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if args[0] == "set" && *provider == "" {
		return errors.New(aiUsage)
	}
	key := ""
	if *keyStdin {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read API key: %w", err)
		}
		key = strings.TrimSpace(line)
	}
	var cfgArgs []string
	if *dataDir != "" {
		cfgArgs = []string{"--data", *dataDir}
	}
	cfg, err := config.Load(cfgArgs)
	if err != nil {
		return err
	}
	ctx := context.Background()
	app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		return err
	}
	defer app.Close()
	st := app.Deps.Settings
	vals := map[string]any{"ai.provider": ""}
	if args[0] == "set" {
		keyStored := st.GetString(ctx, "ai.api_key", "") != ""
		var fields map[string]string
		vals, fields = admin.AIConfig(*provider, *baseURL, *model, key, keyStored, cfg.SecretKey)
		if len(fields) > 0 {
			names := make([]string, 0, len(fields))
			for f, msg := range fields {
				names = append(names, f+" ("+strings.TrimPrefix(msg, "admin.settings.error_")+")")
			}
			sort.Strings(names)
			return fmt.Errorf("ai: invalid %s", strings.Join(names, ", "))
		}
	}
	keys := make([]string, 0, len(vals))
	for k, v := range vals {
		if err := st.Set(ctx, k, v); err != nil {
			return err
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	app.Deps.Audit.Record(ctx, "settings.update", "settings", "", map[string]any{"keys": keys, "via": "cli"})
	if args[0] == "off" {
		fmt.Fprintln(out, "ai writing off")
		return nil
	}
	fmt.Fprintf(out, "ai writing: %s %s\n", vals["ai.provider"], vals["ai.model"])
	return nil
}
