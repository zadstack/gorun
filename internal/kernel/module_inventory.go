package kernel

import (
	"slices"
	"sort"

	"github.com/zadstack/gorun/internal/config"
)

const serviceModuleInventory = "kernel.module_inventory"

// ModuleState records the actual selection made for this process. Availability
// means the module was supplied to Boot; enabled means it passed the existing
// OptionalModule gate. Configuration alone never proves activation.
type ModuleState struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	Optional  bool   `json:"optional"`
	Mode      string `json:"mode" doc:"Requested addon mode, or empty when not configured"`
}

func moduleInventory(cfg config.Config, enabled, all []Module) []ModuleState {
	active := make(map[string]bool, len(enabled))
	for _, m := range enabled {
		active[m.Name()] = true
	}
	seen := make(map[string]bool, len(all))
	out := make([]ModuleState, 0, len(all))
	for _, m := range all {
		_, optional := m.(OptionalModule)
		out = append(out, ModuleState{m.Name(), true, active[m.Name()], optional, cfg.Addons[m.Name()]})
		seen[m.Name()] = true
	}
	for name, mode := range cfg.Addons {
		if !seen[name] {
			out = append(out, ModuleState{Name: name, Optional: true, Mode: mode})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Modules returns a copy of the immutable boot inventory, including configured
// addons absent from this binary. It never re-evaluates activation at request time.
func Modules(d *Deps) []ModuleState {
	states, _ := Get[[]ModuleState](d, serviceModuleInventory)
	return slices.Clone(states)
}
