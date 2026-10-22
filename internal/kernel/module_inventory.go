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
	// Registered is what the module registered during this boot (nil when it
	// did not boot). LastKnownJobKinds are the job kinds it registered at its
	// last enabled boot, the basis for parking its queued work while it is off.
	Registered        *ModuleRegistration `json:"registered,omitempty"`
	LastKnownJobKinds []string            `json:"last_known_job_kinds,omitempty"`
}

func moduleInventory(cfg config.Config, enabled, all []Module, regs map[string]ModuleRegistration, lastKnown func(string) []string) []ModuleState {
	active := make(map[string]bool, len(enabled))
	for _, m := range enabled {
		active[m.Name()] = true
	}
	seen := make(map[string]bool, len(all))
	out := make([]ModuleState, 0, len(all))
	for _, m := range all {
		_, optional := m.(OptionalModule)
		state := ModuleState{Name: m.Name(), Available: true, Enabled: active[m.Name()], Optional: optional, Mode: cfg.Addons[m.Name()]}
		if reg, ok := regs[m.Name()]; ok {
			state.Registered = &reg
		}
		if lastKnown != nil {
			state.LastKnownJobKinds = lastKnown(m.Name())
		}
		out = append(out, state)
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
