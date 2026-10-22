package kernel

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"

	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/settings"
)

// ModuleRegistration is what one module registered while its Register ran,
// observed by the kernel as the difference of every registry before and
// after the call. It is evidence of this boot, not a declaration: a module
// that registers lazily at request time is not seen here.
type ModuleRegistration struct {
	Permissions       []string `json:"permissions" doc:"Declared by this module during Register (a name may also be declared by the auth base catalog)"`
	APIOperations     []string `json:"api_operations" doc:"METHOD /v1/path"`
	MCPTools          []string `json:"mcp_tools"`
	JobKinds          []string `json:"job_kinds"`
	Crons             []string `json:"crons"`
	Nav               []string `json:"nav" doc:"Static admin navigation item IDs"`
	Widgets           []string `json:"widgets"`
	ThemeFuncs        []string `json:"theme_funcs"`
	ThemeKinds        []string `json:"theme_kinds"`
	ServicesPublished []string `json:"services_published"`
	ServicesConsumed  []string `json:"services_consumed" doc:"Service names looked up during Register"`
	HookSubscriptions []string `json:"hook_subscriptions"`
	Catalog           bool     `json:"catalog" doc:"Ships ar/en translations"`
	Migrations        int      `json:"migrations" doc:"Migration files this build knows for the module"`
}

// registrySnapshot is the state of every registry at one point in time.
type registrySnapshot struct {
	perms, apiOps, mcpTools, jobKinds, crons, nav, widgets, themeFuncs, themeKinds, services []string
	hooks                                                                                    map[string]int
}

func snapshot(d *Deps) registrySnapshot {
	s := registrySnapshot{hooks: d.Hooks.Subscriptions()}
	for _, p := range d.Perms.All() {
		s.perms = append(s.perms, p.Name)
	}
	for _, op := range d.API.Operations() {
		s.apiOps = append(s.apiOps, op.Method+" "+op.Path)
	}
	for _, t := range d.MCP.Tools() {
		s.mcpTools = append(s.mcpTools, t.Name)
	}
	s.jobKinds = d.Jobs.Kinds()
	s.crons = d.Jobs.CronNames()
	s.nav = d.Admin.NavIDs()
	for _, w := range d.Admin.Widgets() {
		s.widgets = append(s.widgets, w.ID)
	}
	for name := range d.Theme.Funcs() {
		s.themeFuncs = append(s.themeFuncs, name)
	}
	sort.Strings(s.themeFuncs)
	s.themeKinds = d.Theme.Kinds()
	s.services = d.Services.Names()
	return s
}

func added(before, after []string) []string {
	seen := make(map[string]bool, len(before))
	for _, b := range before {
		seen[b] = true
	}
	out := []string{}
	for _, a := range after {
		if !seen[a] {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// diff returns what was registered between before and after.
func diff(before, after registrySnapshot) ModuleRegistration {
	reg := ModuleRegistration{
		Permissions: added(before.perms, after.perms), APIOperations: added(before.apiOps, after.apiOps),
		MCPTools: added(before.mcpTools, after.mcpTools), JobKinds: added(before.jobKinds, after.jobKinds),
		Crons: added(before.crons, after.crons), Nav: added(before.nav, after.nav), Widgets: added(before.widgets, after.widgets),
		ThemeFuncs: added(before.themeFuncs, after.themeFuncs), ThemeKinds: added(before.themeKinds, after.themeKinds),
		ServicesPublished: added(before.services, after.services), HookSubscriptions: []string{}, ServicesConsumed: []string{},
	}
	for name, n := range after.hooks {
		if n > before.hooks[name] {
			reg.HookSubscriptions = append(reg.HookSubscriptions, name)
		}
	}
	sort.Strings(reg.HookSubscriptions)
	return reg
}

// inventorySettingKey stores a module's last observed job kinds so a later
// boot that disables the module can park its queued work by kind.
func inventorySettingKey(module string) string { return "kernel.inventory." + module + ".job_kinds" }

// rememberJobKinds persists the job kinds of every registered module.
func rememberJobKinds(ctx context.Context, st *settings.Store, regs map[string]ModuleRegistration) error {
	for name, reg := range regs {
		var known []string
		_ = st.Get(ctx, inventorySettingKey(name), &known)
		if slices.Equal(known, reg.JobKinds) {
			continue
		}
		if err := st.Set(ctx, inventorySettingKey(name), reg.JobKinds); err != nil {
			return fmt.Errorf("kernel: remember %s job kinds: %w", name, err)
		}
	}
	return nil
}

// lastKnownJobKinds returns the job kinds module registered at its last
// enabled boot (nil when it never booted with this kernel).
func lastKnownJobKinds(ctx context.Context, st *settings.Store, module string) []string {
	var known []string
	if err := st.Get(ctx, inventorySettingKey(module), &known); err != nil {
		return nil
	}
	return known
}

// parkDisabledWork holds the queued jobs of every disabled module (by its
// last known kinds) and releases the parked jobs of every enabled one.
// Workers never claim an unregistered kind anyway; parking records why for
// operators and keeps the work for the next enable. Nothing is failed or
// deleted.
func parkDisabledWork(ctx context.Context, log *slog.Logger, q *jobs.Queue, states []ModuleState) {
	for _, s := range states {
		if !s.Available {
			continue
		}
		if s.Enabled {
			for _, kind := range s.LastKnownJobKinds {
				if n, err := q.Unpark(ctx, kind); err != nil {
					log.Warn("kernel: unpark jobs of enabled module", "module", s.Name, "kind", kind, "err", err)
				} else if n > 0 {
					log.Info("kernel: released parked jobs of enabled module", "module", s.Name, "kind", kind, "jobs", n)
				}
			}
			continue
		}
		for _, kind := range s.LastKnownJobKinds {
			if n, err := q.Park(ctx, kind, "module "+s.Name+" disabled"); err != nil {
				log.Warn("kernel: park jobs of disabled module", "module", s.Name, "kind", kind, "err", err)
			} else if n > 0 {
				log.Info("kernel: parked queued jobs of disabled module; they wait untouched until it is enabled again", "module", s.Name, "kind", kind, "jobs", n)
			}
		}
	}
}
