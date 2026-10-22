package theme

import (
	"slices"
	"strings"
)

// Design variants. A theme declares the layouts it really implements in
// theme.json "supports" as "<slot>:<value>" (for example "header:rail",
// "single:wide"); the matching theme setting <slot>_style chooses one, and
// templates print the resolved value with {{variant "<slot>" .}}. A stored
// value the theme does not declare — or one that is not a known variant —
// resolves to "native" (the theme's own design), so a theme switch or a
// hand-edited setting can never select a layout that does not exist.
//
// Declarations are not inherited from a parent theme: a child declares what
// its own parts and stylesheets implement, exactly like "identity".

// variantSlots lists every slot and the values a theme may declare for it.
// "native" is implicit and always first.
var variantSlots = map[string][]string{
	"header":  {"rail", "framed", "editorial"},
	"footer":  {"signature", "columns", "minimal"},
	"single":  {"wide", "centered"},
	"archive": {"grid", "compact"},
}

// VariantNative is the fallback value of every slot.
const VariantNative = "native"

// VariantSlots returns the slot names in a stable order.
func VariantSlots() []string {
	out := make([]string, 0, len(variantSlots))
	for k := range variantSlots {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// variantSlotOf maps a theme setting key (<slot>_style) to its slot.
func variantSlotOf(settingKey string) (string, bool) {
	slot, ok := strings.CutSuffix(settingKey, "_style")
	if !ok {
		return "", false
	}
	_, known := variantSlots[slot]
	return slot, known
}

// DeclaredVariants lists the variants info declares for slot, "native"
// first, in the slot's canonical order; nil for an unknown slot.
func DeclaredVariants(info Info, slot string) []string {
	known, ok := variantSlots[slot]
	if !ok {
		return nil
	}
	out := []string{VariantNative}
	for _, v := range known {
		if slices.Contains(info.Supports, slot+":"+v) {
			out = append(out, v)
		}
	}
	return out
}

// ResolveVariant returns stored when info declares it for slot, else
// "native".
func ResolveVariant(info Info, slot, stored string) string {
	if stored == "" || stored == VariantNative {
		return VariantNative
	}
	if slices.Contains(DeclaredVariants(info, slot), stored) {
		return stored
	}
	return VariantNative
}

// variant is the template func: {{variant "header" .}}.
func (f *funcs) variant(slot string, tc *Context) string {
	if tc == nil || tc.Theme.Slug == "" {
		return VariantNative
	}
	return ResolveVariant(tc.Theme, slot, f.setting(SettingKey(tc.Theme.Slug, slot+"_style")))
}
