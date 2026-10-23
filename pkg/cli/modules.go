package cli

import (
	"context"
	"errors"
	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/builder"
	"github.com/zadstack/gorun/internal/commerce"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/ext"
	"github.com/zadstack/gorun/internal/identityai"
	identitylogoadmin "github.com/zadstack/gorun/internal/identitylogo/admin"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/launch"
	"github.com/zadstack/gorun/internal/mcp"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/membership"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/pattern"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/saas"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/internal/webhooks"
	"github.com/zadstack/gorun/internal/workagents"
	"github.com/zadstack/gorun/internal/wpmigrate"
)

// CoreModules returns the core domain modules in boot order (see
// kernel.BootOrder). Only the orchestrator edits this list.
func CoreModules() []kernel.Module {
	return []kernel.Module{
		settingsmod.New(),
		auth.New(),
		content.New(),
		taxonomy.New(),
		media.New(),
		menus.New(),
		redirects.New(),
		seo.New(),
		theme.New(),
		pattern.New(),
		builder.New(),
		site.New(),
		api.New(),
		admin.New(),
		webhooks.New(),
		commerce.New(),
		saas.New(),
		membership.New(),
		launch.New(),
		workagents.New(admin.CompleteWorkAgent),
		identityai.New(identityAIHooks()),
		identitylogoadmin.New(),
		wpmigrate.New(),
		mcp.New(),
		ext.New(),
	}
}

// Modules returns the modules every command boots: CoreModules, passed
// through the ModulesFunc options (WithModules) in order. The optional config
// is the loaded site config; without one the hooks get a zero Config.
func Modules(configs ...config.Config) []kernel.Module {
	cfg := config.Config{}
	if len(configs) > 0 {
		cfg = configs[0]
	}
	mods := CoreModules()
	for _, fn := range active.modules {
		mods = fn(cfg, mods)
	}
	return mods
}

// InsertAfter returns a copy of mods with extra inserted right after the
// module named after (appended when after is not present).
func InsertAfter(mods []kernel.Module, after string, extra ...kernel.Module) []kernel.Module {
	out := make([]kernel.Module, 0, len(mods)+len(extra))
	inserted := false
	for _, m := range mods {
		out = append(out, m)
		if !inserted && m.Name() == after {
			out = append(out, extra...)
			inserted = true
		}
	}
	if !inserted {
		out = append(out, extra...)
	}
	return out
}

// identityAIHooks adapts the admin-owned AI bridge to the optional
// identityai module without the admin package importing it.
func identityAIHooks() identityai.Hooks {
	return identityai.Hooks{
		Complete: func(ctx context.Context, d *kernel.Deps, connectionID, model, system, input string, maxTokens int, expect string) (string, error) {
			out, err := admin.CompleteWorkAgentBounded(ctx, d, connectionID, model, system, input, maxTokens, expect)
			if errors.Is(err, admin.ErrAIDestinationChanged) {
				return "", identityai.ErrDestinationChanged
			}
			return out, err
		},
		Destination: func(ctx context.Context, d *kernel.Deps, id string) (identityai.Connection, string, bool) {
			s, fp, ok := admin.AIDestination(ctx, d, id)
			return identityai.Connection{ID: s.ID, Name: s.Name, Provider: s.Provider, Model: s.Model, Host: s.Host, TestStatus: s.TestStatus}, fp, ok
		},
		Connections: func(ctx context.Context, d *kernel.Deps) ([]identityai.Connection, error) {
			rows, err := admin.ListAIConnections(ctx, d)
			if err != nil {
				return nil, err
			}
			out := make([]identityai.Connection, 0, len(rows))
			for _, r := range rows {
				out = append(out, identityai.Connection{ID: r.ID, Name: r.Name, Provider: r.Provider, Model: r.Model, Host: r.Host, TestStatus: r.TestStatus})
			}
			return out, nil
		},
	}
}
