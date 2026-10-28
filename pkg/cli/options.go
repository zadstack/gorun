package cli

import (
	"io"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/diagnostics"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/site"
)

// ModulesFunc receives the site config and the module list built so far
// (CoreModules first) and returns the list to boot. Use InsertAfter to keep
// a module at its BootOrder position.
type ModulesFunc func(cfg config.Config, mods []kernel.Module) []kernel.Module

// Command is an extra top-level command. Usage is the exact help text for
// it (indented like the core lines, ending in a newline); commands are listed
// in registration order before api-docs. Run gets the arguments after Name.
type Command struct {
	Name  string
	Usage string
	Run   func(args []string, in io.Reader, out io.Writer) error
}

// Option configures the command line (see Configure).
type Option func(*settings)

type settings struct {
	modules  []ModulesFunc
	commands []Command
	addons   []string
}

// active holds the process-wide options: the CLI is one process per command.
var active settings

// Configure resets the CLI to the core build, then applies opts. Main calls
// it; tests and downstream binaries may call it before Run or Modules.
// Process-wide registries (boot order, log categories, policy directory)
// are idempotent and are not reset.
func Configure(opts ...Option) {
	active = settings{}
	for _, opt := range opts {
		opt(&active)
	}
}

// WithVersion sets the version printed by "gorun version" and reported as
// the API core version (api.CoreVersion).
func WithVersion(v string) Option {
	return func(*settings) {
		version = v
		api.CoreVersion = v
	}
}

// WithModules adds a ModulesFunc. Funcs run in the order given.
func WithModules(fn ModulesFunc) Option {
	return func(s *settings) { s.modules = append(s.modules, fn) }
}

// WithCommands adds top-level commands. A name that a core command already
// uses, or an earlier extra command, is ignored (core behavior never changes).
func WithCommands(cmds ...Command) Option {
	return func(s *settings) {
		for _, c := range cmds {
			if c.Name == "" || c.Run == nil || coreCommand(c.Name) || slices.ContainsFunc(s.commands, func(o Command) bool { return o.Name == c.Name }) {
				continue
			}
			s.commands = append(s.commands, c)
		}
	}
}

// WithAddons names the optional addons that "api-docs --addon NAME" accepts
// (each is booted with config Addons[NAME] = "enabled").
func WithAddons(names ...string) Option {
	return func(s *settings) {
		for _, n := range names {
			if n != "" && !slices.Contains(s.addons, n) {
				s.addons = append(s.addons, n)
			}
		}
	}
}

// WithBootOrder places extra module names in kernel.BootOrder right after
// the module named after, so Boot validates their position and Health lists
// them (see kernel.ExtendBootOrder).
func WithBootOrder(after string, names ...string) Option {
	return func(*settings) { kernel.ExtendBootOrder(after, names...) }
}

// WithLogCategory adds an error-inventory category: error logs whose message
// starts with "name:" or "name " are filed under it (see
// diagnostics.RegisterCategory). before positions it in the list.
func WithLogCategory(name, before string) Option {
	return func(*settings) { diagnostics.RegisterCategory(name, before) }
}

// WithSitePolicyDirectory sets the root-owned directory the site module
// trusts for GORUN_SITE_POLICY files (see site.SetPolicyDirectory).
func WithSitePolicyDirectory(dir string) Option {
	return func(*settings) { site.SetPolicyDirectory(dir) }
}

var coreCommands = []string{"doctor", "start", "serve", "migrate", "user", "healthcheck", "reindex", "mcp", "backup-preview", "backup-inspect", "backup-restore", "backup-decrypt", "backup", "staging", "settings", "ai", "mail", "info", "starter", "content", "theme", "key", "webhook", "import-wxr", "api-docs", "release-index", "version", "help", "-h", "--help"}

func coreCommand(name string) bool { return slices.Contains(coreCommands, name) }

func extraCommand(name string) (Command, bool) {
	for _, c := range active.commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// helpText is the core usage with the extra commands listed before api-docs.
func helpText() string {
	var b strings.Builder
	b.WriteString(usageHead)
	for _, c := range active.commands {
		b.WriteString(c.Usage)
	}
	b.WriteString(usageTail)
	return b.String()
}
