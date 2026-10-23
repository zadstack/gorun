package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/kernel"
)

// ModuleCatalogFilter is the hooks filter later modules use to add their
// own entries to the module catalog (GET /system/modules and System →
// Modules). The payload is *[]admin.CatalogEntry; the ext module appends
// one entry per installed external extension.
const ModuleCatalogFilter = "system.catalog.entries"

// Catalog entry kinds and sources.
const (
	CatalogKindCore      = "core"
	CatalogKindFeature   = "feature"
	CatalogKindService   = "service"
	CatalogKindExtension = "extension"

	CatalogSourceBuiltIn  = "built-in"
	CatalogSourceExternal = "external"

	CompatBuiltIn            = "built_in"
	CompatManifestUnverified = "manifest_unverified"
	CompatNotInBuild         = "not_in_build"
)

// CatalogEntry describes one module, core service or external extension:
// what it does, where it comes from, what it depends on, what it registered
// in this process, whether it is on, and whether it needs configuration.
// Every field states what was observed; "unknown" is an honest value.
type CatalogEntry struct {
	ID                   string                     `json:"id"`
	Kind                 string                     `json:"kind" enum:"core,feature,service,extension"`
	Name                 map[string]string          `json:"name" doc:"Display name by locale (ar, en)"`
	Summary              map[string]string          `json:"summary" doc:"What it does, by locale"`
	Source               string                     `json:"source" enum:"built-in,external"`
	Version              string                     `json:"version" doc:"Build version for built-ins, manifest version for extensions"`
	Compatibility        string                     `json:"compatibility" enum:"built_in,manifest_unverified,not_in_build" doc:"built_in: compiled into this release; manifest_unverified: the manifest's version, compatibility with this build not verified; not_in_build: named in config but absent"`
	Owner                string                     `json:"owner,omitempty" doc:"Core module that owns a service"`
	Dependencies         []string                   `json:"dependencies" doc:"Modules it needs (declared)"`
	OptionalDependencies []string                   `json:"optional_dependencies" doc:"Modules it uses when present"`
	Dependents           []string                   `json:"dependents" doc:"Modules that need or use it"`
	Permissions          []string                   `json:"permissions"`
	State                CatalogState               `json:"state"`
	Configuration        CatalogConfiguration       `json:"configuration"`
	Registered           *kernel.ModuleRegistration `json:"registered,omitempty" doc:"Observed registrations of this boot; absent while off"`
	Jobs                 CatalogJobs                `json:"jobs"`
	Messages             []CatalogMessage           `json:"messages"`
	Links                map[string]string          `json:"links,omitempty"`
}

// CatalogState is the activation of an entry: the booted state, the
// desired state (setting or config), and whether a restart is pending.
type CatalogState struct {
	Status         string `json:"status" enum:"enabled,disabled,pending_enable,pending_disable,unavailable,always_on,active,installed,error"`
	Available      bool   `json:"available"`
	Enabled        bool   `json:"enabled" doc:"Booted in this process"`
	Optional       bool   `json:"optional" doc:"Can be switched off"`
	Mode           string `json:"mode,omitempty" doc:"Operator config mode (GORUN_ADDONS), if any"`
	Desired        *bool  `json:"desired" doc:"Activation the next boot will apply; null when not optional"`
	PendingRestart bool   `json:"pending_restart" doc:"Desired differs from the booted state"`
	Switchable     bool   `json:"switchable" doc:"The activation can be changed here (optional and not forced by config)"`
}

// CatalogConfiguration is the module's own verdict on its settings.
type CatalogConfiguration struct {
	Status      string `json:"status" enum:"configured,incomplete,not_required,unknown"`
	Reason      string `json:"reason,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

// CatalogJobs is the background work attributed to the entry.
type CatalogJobs struct {
	Kinds  []string `json:"kinds" doc:"Job kinds registered now, or last known while off"`
	Queued int      `json:"queued" doc:"Queued, claimable by a worker"`
	Parked int      `json:"parked" doc:"Queued but held because the module is off"`
}

// CatalogMessage is an actionable note shown with the entry.
type CatalogMessage struct {
	Level string            `json:"level" enum:"info,warning"`
	Text  map[string]string `json:"text"`
}

// ModuleCatalog is GET /system/modules.
type ModuleCatalog struct {
	ObservedAt string         `json:"observed_at"`
	Build      string         `json:"build"`
	Entries    []CatalogEntry `json:"entries"`
	Coverage   string         `json:"coverage"`
}

// builtin describes a built-in module: its kind and declared dependencies.
// Names must match kernel.BootOrder; moduleCatalogTable is checked by
// TestBuiltinCatalogCoversBootOrder. Summaries are catalog strings
// (modules.desc.<name>) so both languages live with the other admin copy.
type builtin struct {
	kind         string
	dependencies []string
	optional     []string // used when present, through services or hooks
}

var moduleCatalogTable = map[string]builtin{
	"settings":     {kind: CatalogKindCore},
	"auth":         {kind: CatalogKindCore},
	"content":      {kind: CatalogKindCore, dependencies: []string{"auth"}},
	"taxonomy":     {kind: CatalogKindCore, dependencies: []string{"content"}},
	"media":        {kind: CatalogKindCore, dependencies: []string{"auth"}},
	"menus":        {kind: CatalogKindCore, dependencies: []string{"content", "taxonomy"}},
	"redirects":    {kind: CatalogKindCore, dependencies: []string{"auth"}},
	"seo":          {kind: CatalogKindCore, dependencies: []string{"content", "taxonomy", "menus"}},
	"theme":        {kind: CatalogKindCore, dependencies: []string{"content", "media", "menus", "taxonomy", "seo"}, optional: []string{"commerce"}},
	"pattern":      {kind: CatalogKindCore, dependencies: []string{"theme"}},
	"builder":      {kind: CatalogKindCore, dependencies: []string{"content", "media", "menus", "theme"}},
	"site":         {kind: CatalogKindCore, dependencies: []string{"content", "theme", "seo", "redirects", "taxonomy"}},
	"api":          {kind: CatalogKindCore, dependencies: []string{"auth"}},
	"admin":        {kind: CatalogKindCore, dependencies: []string{"auth", "api", "content", "media", "theme"}},
	"webhooks":     {kind: CatalogKindCore, dependencies: []string{"auth"}},
	"commerce":     {kind: CatalogKindFeature, dependencies: []string{"admin", "api", "theme", "media", "redirects", "content"}},
	"saas":         {kind: CatalogKindFeature, dependencies: []string{"admin", "api", "auth"}, optional: []string{"commerce"}},
	"membership":   {kind: CatalogKindFeature, dependencies: []string{"api", "auth", "content"}, optional: []string{"commerce", "saas", "workagents"}},
	"launch":       {kind: CatalogKindFeature, dependencies: []string{"auth"}},
	"workagents":   {kind: CatalogKindFeature, dependencies: []string{"admin", "auth", "content"}},
	"identityai":   {kind: CatalogKindFeature, dependencies: []string{"admin", "auth", "theme"}},
	"identitylogo": {kind: CatalogKindFeature, dependencies: []string{"admin", "auth", "theme", "media"}},
	"wpmigrate":    {kind: CatalogKindFeature, dependencies: []string{"admin", "api", "content", "taxonomy", "media", "menus", "redirects", "theme"}, optional: []string{"commerce"}},
	"mcp":          {kind: CatalogKindFeature, dependencies: []string{"admin", "api", "auth", "content", "media", "taxonomy", "theme"}, optional: []string{"commerce", "wpmigrate"}},
	"ext":          {kind: CatalogKindFeature, dependencies: []string{"admin", "api", "auth", "content"}},
}

// dependencyNotes are the consequences of a dependency being off, shown on
// the dependent (key modules.dep.<dependency>.<dependent>, both locales).
var dependencyNotes = map[[2]string]bool{
	{"workagents", "membership"}: true, {"commerce", "saas"}: true, {"commerce", "membership"}: true, {"commerce", "wpmigrate"}: true, {"commerce", "mcp"}: true, {"commerce", "theme"}: true, {"wpmigrate", "mcp"}: true,
}

// coreServices are general services that are features of core modules, not
// modules of their own: configured by settings, never enabled or disabled.
var coreServices = []struct{ id, owner string }{{"forms", "site"}, {"storage", "media"}, {"backups", "admin"}, {"mail", "admin"}}

func (h *handlers) moduleNames(ctx context.Context, key string) map[string]string {
	return map[string]string{"en": h.d.I18n.T("en", key), "ar": h.d.I18n.T("ar", key)}
}

// moduleCatalog assembles the catalog from the boot inventory, the settings
// that decide the next boot, the capability requirements modules publish,
// the job queue and the entries later modules add through the filter.
func (h *handlers) moduleCatalog(ctx context.Context) (ModuleCatalog, error) {
	requirements, err := h.moduleRequirements(ctx)
	if err != nil {
		return ModuleCatalog{}, err
	}
	queued, err := h.d.Jobs.QueuedByKind(ctx)
	if err != nil {
		return ModuleCatalog{}, err
	}
	parked, err := h.d.Jobs.Parked(ctx)
	if err != nil {
		return ModuleCatalog{}, err
	}
	dependents := map[string][]string{}
	for name, b := range moduleCatalogTable {
		for _, dep := range b.dependencies {
			dependents[dep] = append(dependents[dep], name)
		}
		for _, dep := range b.optional {
			dependents[dep] = append(dependents[dep], name)
		}
	}
	states := kernel.Modules(h.d)
	// Boot order reads better than the inventory's alphabetical order.
	sort.SliceStable(states, func(i, j int) bool {
		pi, pj := slices.Index(kernel.BootOrder, states[i].Name), slices.Index(kernel.BootOrder, states[j].Name)
		if pi < 0 {
			pi = len(kernel.BootOrder)
		}
		if pj < 0 {
			pj = len(kernel.BootOrder)
		}
		return pi < pj
	})
	booted := map[string]kernel.ModuleState{}
	for _, s := range states {
		booted[s.Name] = s
	}
	out := ModuleCatalog{ObservedAt: time.Now().UTC().Format(time.RFC3339), Build: api.CoreVersion, Entries: []CatalogEntry{},
		Coverage: "Built-in modules from the boot inventory (what each registered in this process, observed by the kernel), their declared dependencies, the activation the next boot will apply, module-published configuration requirements, queued and parked jobs by kind, core services described by their settings, and external extensions reported by the ext module. Compatibility of extensions is not verified; absent evidence is reported as unknown."}
	for _, s := range states {
		b, known := moduleCatalogTable[s.Name]
		e := CatalogEntry{ID: s.Name, Kind: b.kind, Source: CatalogSourceBuiltIn, Version: api.CoreVersion, Compatibility: CompatBuiltIn,
			Name: h.moduleNames(ctx, "admin.modules.name."+s.Name), Summary: h.moduleNames(ctx, "admin.modules.desc."+s.Name),
			Dependencies: orEmpty(b.dependencies), OptionalDependencies: orEmpty(b.optional), Dependents: sorted(dependents[s.Name]),
			Permissions: []string{}, Messages: []CatalogMessage{}, Registered: s.Registered, Jobs: CatalogJobs{Kinds: []string{}}}
		if !known {
			e.Kind = CatalogKindFeature
			e.Summary = map[string]string{"en": "No description in this build.", "ar": "لا يوجد وصف في هذا الإصدار."}
			e.Name = map[string]string{"en": s.Name, "ar": s.Name}
		}
		if !s.Available {
			e.Source, e.Version, e.Compatibility = CatalogSourceExternal, "unknown", CompatNotInBuild
		}
		e.State = h.catalogState(ctx, s)
		if s.Registered != nil {
			e.Permissions = s.Registered.Permissions
			e.Jobs.Kinds = s.Registered.JobKinds
		} else if len(s.LastKnownJobKinds) > 0 {
			e.Jobs.Kinds = s.LastKnownJobKinds
		}
		for _, k := range e.Jobs.Kinds {
			e.Jobs.Queued += queued[k]
			e.Jobs.Parked += parked[k]
		}
		e.Configuration = catalogConfiguration(s, requirements)
		e.Messages = h.catalogMessages(ctx, s, e, booted)
		if s.Enabled {
			e.Links = map[string]string{"health": "/admin/system/health"}
		}
		out.Entries = append(out.Entries, e)
	}
	for _, svc := range coreServices {
		out.Entries = append(out.Entries, h.serviceEntry(ctx, svc.id, svc.owner))
	}
	extra := []CatalogEntry{}
	v, err := h.d.Hooks.Apply(ctx, ModuleCatalogFilter, &extra)
	if err != nil {
		return ModuleCatalog{}, err
	}
	if p, ok := v.(*[]CatalogEntry); ok && p != nil {
		out.Entries = append(out.Entries, *p...)
	}
	return out, nil
}

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func sorted(list []string) []string {
	out := slices.Clone(orEmpty(list))
	sort.Strings(out)
	return out
}

// catalogState reads the activation the next boot will apply: operator
// config mode "disabled" wins, otherwise the addons.<name>.enabled setting
// (missing means enabled). Non-optional modules are always on.
func (h *handlers) catalogState(ctx context.Context, s kernel.ModuleState) CatalogState {
	st := CatalogState{Available: s.Available, Enabled: s.Enabled, Optional: s.Optional, Mode: s.Mode}
	switch {
	case !s.Available:
		st.Status = "unavailable"
		return st
	case !s.Optional:
		st.Status = "always_on"
		return st
	}
	desired := true
	if s.Mode == kernel.AddonModeDisabled {
		desired = false
	} else if on, err := kernel.AddonEnabled(ctx, h.d.DB, s.Name); err == nil {
		desired = on
	}
	st.Desired = boolPtr(desired)
	st.Switchable = s.Mode != kernel.AddonModeDisabled
	st.PendingRestart = desired != s.Enabled
	switch {
	case st.PendingRestart && desired:
		st.Status = "pending_enable"
	case st.PendingRestart:
		st.Status = "pending_disable"
	case s.Enabled:
		st.Status = "enabled"
	default:
		st.Status = "disabled"
	}
	return st
}

func catalogConfiguration(s kernel.ModuleState, requirements map[string]ModuleRequirement) CatalogConfiguration {
	if !s.Enabled {
		return CatalogConfiguration{Status: "unknown", Reason: "module_not_booted", Remediation: "Configuration is reported by the module once it boots."}
	}
	req, ok := requirements[s.Name]
	if !ok {
		return CatalogConfiguration{Status: "not_required", Reason: "module_publishes_no_requirements", Remediation: "This module has not published configuration requirements; nothing is assumed."}
	}
	c := CatalogConfiguration{Status: "configured", Reason: req.Reason, Remediation: req.Remediation}
	if !req.Configured {
		c.Status = "incomplete"
	}
	return c
}

// catalogMessages explains what being off (or about to be off) means for
// the entry and for the modules that use it, and where waiting work sits.
func (h *handlers) catalogMessages(ctx context.Context, s kernel.ModuleState, e CatalogEntry, booted map[string]kernel.ModuleState) []CatalogMessage {
	msgs := []CatalogMessage{}
	add := func(level, key string, args ...any) {
		msgs = append(msgs, CatalogMessage{Level: level, Text: map[string]string{"en": h.d.I18n.T("en", key, args...), "ar": h.d.I18n.T("ar", key, args...)}})
	}
	if e.State.PendingRestart {
		add("warning", "admin.modules.msg.pending_restart")
	}
	if s.Available && !s.Enabled {
		add("info", "admin.modules.msg.off_keeps_data")
	}
	if e.Jobs.Parked > 0 {
		add("warning", "admin.modules.msg.parked_jobs", e.Jobs.Parked)
	}
	for _, dep := range append(slices.Clone(e.Dependencies), e.OptionalDependencies...) {
		d, ok := booted[dep]
		if ok && d.Enabled {
			continue
		}
		if dependencyNotes[[2]string{dep, s.Name}] {
			add("warning", "admin.modules.dep."+dep+"."+s.Name)
		} else if slices.Contains(e.Dependencies, dep) {
			add("warning", "admin.modules.msg.dependency_off", dep)
		}
	}
	if s.Enabled && e.State.Optional {
		for _, dependent := range e.Dependents {
			if dependencyNotes[[2]string{s.Name, dependent}] {
				add("info", "admin.modules.msg.if_off_affects", dependent)
			}
		}
	}
	return msgs
}

// serviceEntry classifies a general service by its real design: a feature
// of a core module, configured by settings, never switched on or off.
func (h *handlers) serviceEntry(ctx context.Context, id, owner string) CatalogEntry {
	e := CatalogEntry{ID: id, Kind: CatalogKindService, Source: CatalogSourceBuiltIn, Version: api.CoreVersion, Compatibility: CompatBuiltIn, Owner: owner,
		Name: h.moduleNames(ctx, "admin.modules.name."+id), Summary: h.moduleNames(ctx, "admin.modules.desc."+id),
		Dependencies: []string{owner}, OptionalDependencies: []string{}, Dependents: []string{}, Permissions: []string{}, Messages: []CatalogMessage{}, Jobs: CatalogJobs{Kinds: []string{}},
		State: CatalogState{Status: "always_on", Available: true, Enabled: true}}
	switch id {
	case "mail":
		c := h.mailCapability(ctx)
		e.Configuration = CatalogConfiguration{Status: c.ConfigurationStatus, Reason: c.Reason, Remediation: c.Remediation}
		e.Links = map[string]string{"settings": "/admin/settings/mail"}
	case "storage":
		c := h.storageCapability(ctx)
		e.Configuration = CatalogConfiguration{Status: c.ConfigurationStatus, Reason: c.Reason, Remediation: c.Remediation}
		e.Jobs.Kinds = []string{"media.fetch_url"}
		e.Links = map[string]string{"settings": "/admin/settings/storage"}
	case "backups":
		hours := h.d.Settings.GetString(ctx, "backup.schedule_hours", "")
		e.Jobs.Kinds = []string{"backup.scheduled"}
		e.Links = map[string]string{"settings": "/admin/system/backups"}
		if strings.TrimSpace(hours) == "" || hours == "0" {
			e.Configuration = CatalogConfiguration{Status: "incomplete", Reason: "schedule_off", Remediation: "Manual backups work; set backup.schedule_hours under System → Backups for scheduled ones."}
		} else {
			e.Configuration = CatalogConfiguration{Status: "configured", Reason: "schedule_set", Remediation: "Check the last scheduled backup under System → Backups."}
		}
	case "forms":
		recipients := strings.TrimSpace(h.d.Settings.GetString(ctx, "forms.recipients", ""))
		contact := strings.TrimSpace(h.d.Settings.GetString(ctx, "contact.email", ""))
		e.Links = map[string]string{"inbox": "/admin/reports"}
		if recipients == "" && contact == "" {
			e.Configuration = CatalogConfiguration{Status: "incomplete", Reason: "no_recipients", Remediation: "Submissions are stored; set forms.recipients or contact.email to also mail them (else the first active admin is used)."}
		} else {
			e.Configuration = CatalogConfiguration{Status: "configured", Reason: "recipients_set"}
		}
	}
	return e
}

func (h *handlers) moduleCatalogAPIOperations() {
	h.d.API.Add(api.Op("GET", "/system/modules", "Module catalog: built-in modules, core services and external extensions with function, source, version, dependencies, permissions, observed registrations, activation and configuration state",
		[]string{"system"}, []string{PermHealth}, nil, ModuleCatalog{}, h.moduleCatalogAPI))
	op := api.Op("PUT", "/system/modules/{name}/activation", "Set whether an optional module boots next time (data is kept either way; a restart applies it)",
		[]string{"system"}, []string{PermSettings}, ModuleActivation{}, CatalogEntry{}, h.moduleActivationAPI)
	op.Params = []kernel.APIParam{{Name: "name", In: "path", Type: "string", Required: true, Description: "Module name as listed by GET /system/modules"}}
	op.Contract = &kernel.APIContract{Errors: []kernel.APIErrorResponse{{Status: 422, Code: "validation_failed", Description: "enabled missing, null or not a boolean"}, {Status: 404, Code: "not_found", Description: "No such module in this build"},
		{Status: 409, Code: "not_optional", Description: "The module is always on, or its activation is forced by the operator config"}}}
	h.d.API.Add(op)
}

// ModuleActivation is the body of PUT /system/modules/{name}/activation.
// Enabled is required: a missing or null value is rejected (422) rather
// than read as false, so a malformed request can never switch a module off.
type ModuleActivation struct {
	Enabled *bool `json:"enabled"`
}

func (h *handlers) moduleCatalogAPI(w http.ResponseWriter, r *http.Request) {
	cat, err := h.moduleCatalog(r.Context())
	if err != nil {
		api.Fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteItem(w, cat)
}

var (
	errNotOptional   = errors.New("module is not optional")
	errModuleUnknown = errors.New("module not in this build")
)

// setModuleActivation writes addons.<name>.enabled for an optional module
// that the operator config does not force off, and audits it.
func (h *handlers) setModuleActivation(ctx context.Context, name string, enabled bool) (CatalogEntry, error) {
	cat, err := h.moduleCatalog(ctx)
	if err != nil {
		return CatalogEntry{}, err
	}
	i := slices.IndexFunc(cat.Entries, func(e CatalogEntry) bool {
		return e.ID == name && e.Source == CatalogSourceBuiltIn && e.Kind != CatalogKindService
	})
	if i < 0 {
		return CatalogEntry{}, errModuleUnknown
	}
	e := cat.Entries[i]
	if !e.State.Switchable {
		return e, errNotOptional
	}
	if err := h.d.Settings.Set(ctx, kernel.AddonSettingKey(name), enabled); err != nil {
		return e, err
	}
	h.d.Audit.Record(ctx, "module.activation", "module", name, map[string]any{"enabled": enabled, "restart_required": true})
	cat, err = h.moduleCatalog(ctx)
	if err != nil {
		return CatalogEntry{}, err
	}
	return cat.Entries[slices.IndexFunc(cat.Entries, func(e CatalogEntry) bool { return e.ID == name })], nil
}

func (h *handlers) moduleActivationAPI(w http.ResponseWriter, r *http.Request) {
	var in ModuleActivation
	if err := api.Decode(w, r, &in); err != nil {
		api.Fail(w, r, err)
		return
	}
	if in.Enabled == nil {
		api.WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "enabled must be true or false", map[string]string{"enabled": "required"})
		return
	}
	e, err := h.setModuleActivation(r.Context(), chi.URLParam(r, "name"), *in.Enabled)
	switch {
	case errors.Is(err, errNotOptional):
		api.WriteError(w, http.StatusConflict, "not_optional", "this module is always on or forced by the operator config", nil)
		return
	case errors.Is(err, errModuleUnknown):
		api.WriteError(w, http.StatusNotFound, "not_found", "no such module in this build", nil)
		return
	case err != nil:
		api.Fail(w, r, err)
		return
	}
	api.WriteItem(w, e)
}

// modulesPage is System → Modules.
type modulesPage struct {
	Catalog                       ModuleCatalog
	Builtin, Services, Extensions modulesSection
	Flash                         string
	Error                         string
}

type modulesSection struct {
	Section string
	Entries []CatalogEntry
}

func sections(cat ModuleCatalog) (builtin, services, extensions modulesSection) {
	builtin, services, extensions = modulesSection{Section: "builtin", Entries: []CatalogEntry{}}, modulesSection{Section: "services", Entries: []CatalogEntry{}}, modulesSection{Section: "extensions", Entries: []CatalogEntry{}}
	for _, e := range cat.Entries {
		switch e.Kind {
		case CatalogKindService:
			services.Entries = append(services.Entries, e)
		case CatalogKindExtension:
			extensions.Entries = append(extensions.Entries, e)
		default:
			builtin.Entries = append(builtin.Entries, e)
		}
	}
	return
}

func (h *handlers) modulesRoutes() {
	r := h.d.Router.Admin.With(auth.RequirePerm(PermHealth))
	r.Get("/system/modules", h.modulesPageHTTP)
	h.d.Router.Admin.With(auth.RequirePerm(PermSettings)).Post("/system/modules/{name}/activation", h.modulesActivationHTTP)
}

func (h *handlers) modulesPageHTTP(w http.ResponseWriter, r *http.Request) {
	cat, err := h.moduleCatalog(r.Context())
	if err != nil {
		h.d.Log.Error("admin: modules page", "err", err)
		h.tpl.RenderError(w, r, http.StatusInternalServerError)
		return
	}
	page := modulesPage{Catalog: cat, Flash: r.URL.Query().Get("saved"), Error: r.URL.Query().Get("error")}
	page.Builtin, page.Services, page.Extensions = sections(cat)
	h.tpl.Render(w, r, "pages/system/modules.html", page)
}

func (h *handlers) modulesActivationHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/admin/system/modules?error=invalid", http.StatusSeeOther)
		return
	}
	name := chi.URLParam(r, "name")
	var enabled bool
	switch r.Form.Get("enabled") {
	case "true":
		enabled = true
	case "false":
		enabled = false
	default: // missing or anything else: no setting or audit write
		http.Redirect(w, r, "/admin/system/modules?error=invalid", http.StatusSeeOther)
		return
	}
	_, err := h.setModuleActivation(r.Context(), name, enabled)
	switch {
	case errors.Is(err, errNotOptional):
		http.Redirect(w, r, "/admin/system/modules?error=not_optional", http.StatusSeeOther)
	case errors.Is(err, errModuleUnknown):
		http.Redirect(w, r, "/admin/system/modules?error=not_found", http.StatusSeeOther)
	case err != nil:
		h.d.Log.Error("admin: modules page", "err", err)
		h.tpl.RenderError(w, r, http.StatusInternalServerError)
	default:
		http.Redirect(w, r, fmt.Sprintf("/admin/system/modules?saved=%s", name), http.StatusSeeOther)
	}
}
