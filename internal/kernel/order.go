package kernel

import "slices"

// BootOrder is the canonical module order. A module may only import modules
// earlier in this list (enforced by importorder_test.go); Boot rejects module
// lists that violate it. Packages not listed (kernel, config, hooks, httpx…)
// and the kernel infrastructure (db, settings, i18n, audit, jobs) are
// importable by every module but never import domain modules. A downstream
// build adds its own modules with ExtendBootOrder.
var BootOrder = []string{"db", "settings", "i18n", "auth", "audit", "jobs", "content", "taxonomy", "media", "menus", "redirects", "seo", "theme", "builder", "site", "api", "admin", "webhooks", "commerce", "saas", "membership", "workagents", "wpmigrate", "mcp", "ext"}

// ExtendBootOrder inserts names into BootOrder right after the module named
// after (at the end when after is empty or unknown), keeping their order.
// Names already in BootOrder are skipped, so calling it again is harmless.
// Call it before Boot, typically from a binary's main.
func ExtendBootOrder(after string, names ...string) {
	at := len(BootOrder)
	if i := slices.Index(BootOrder, after); i >= 0 {
		at = i + 1
	}
	for _, name := range names {
		if name == "" || slices.Contains(BootOrder, name) {
			continue
		}
		BootOrder = slices.Insert(BootOrder, at, name)
		at++
	}
}
