package diagnostics

import (
	"slices"
	"sync"
)

// coreCategories are the error-inventory categories of the core build, in
// display order. "application" and "http" are assigned by the handler; the
// rest are message prefixes ("seo: …" or "seo …").
var coreCategories = []string{"application", "http", "admin", "auth", "site", "jobs", "settings", "seo", "media", "mailer", "mcp", "ext", "commerce"}

var (
	categoriesMu sync.RWMutex
	categories   = slices.Clone(coreCategories)
)

// RegisterCategory adds an error-inventory category for a downstream module:
// error logs whose message starts with "name:" or "name " are filed under it.
// It is listed right before the category before (at the end when before is
// empty or unknown). Registering a known name again does nothing.
func RegisterCategory(name, before string) {
	categoriesMu.Lock()
	defer categoriesMu.Unlock()
	if name == "" || slices.Contains(categories, name) {
		return
	}
	at := len(categories)
	if i := slices.Index(categories, before); i >= 0 {
		at = i
	}
	categories = slices.Insert(categories, at, name)
}

// Categories returns every error-inventory category in display order.
func Categories() []string {
	categoriesMu.RLock()
	defer categoriesMu.RUnlock()
	return slices.Clone(categories)
}

// prefixCategories are the categories matched by message prefix.
func prefixCategories() []string {
	all := Categories()
	return slices.DeleteFunc(all, func(c string) bool { return c == "application" || c == "http" })
}
