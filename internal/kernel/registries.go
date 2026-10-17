package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"sync"
)

// Registries are filled during Register and read at request time. Duplicate
// registrations are programmer errors and panic (like http.ServeMux); Boot
// turns a panic inside Register into an error naming the module.

// NavItem is an admin navigation entry. LabelKey is an i18n key.
type NavItem struct {
	ID, LabelKey, Icon, Href, Permission, Group string
	Position                                    int
}

// Widget is an admin dashboard widget.
type Widget struct {
	ID     string
	Render func(ctx context.Context, r *http.Request) (template.HTML, error)
}

// AdminRegistry collects admin navigation and dashboard widgets.
type AdminRegistry struct {
	mu        sync.RWMutex
	nav       []NavItem
	providers []func(ctx context.Context) []NavItem
	widgets   []Widget
	canvas    []CanvasWidget
	can       func(ctx context.Context, perm string) bool
}

// NewAdminRegistry returns an empty registry.
func NewAdminRegistry() *AdminRegistry { return &AdminRegistry{} }

// AddNav adds a navigation item; IDs must be unique.
func (a *AdminRegistry) AddNav(item NavItem) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if slices.ContainsFunc(a.nav, func(n NavItem) bool { return n.ID == item.ID }) {
		panic(fmt.Sprintf("kernel: duplicate admin nav id %q", item.ID))
	}
	a.nav = append(a.nav, item)
}

// AddNavProvider adds a function evaluated on every Nav call (each admin
// request) for entries that change at runtime, e.g. one per content type.
// Its items go through the same permission filter and sort; an item whose
// ID is already registered with AddNav is skipped.
func (a *AdminRegistry) AddNavProvider(fn func(ctx context.Context) []NavItem) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.providers = append(a.providers, fn)
}

// AddWidget adds a dashboard widget; IDs must be unique.
func (a *AdminRegistry) AddWidget(id string, render func(ctx context.Context, r *http.Request) (template.HTML, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if slices.ContainsFunc(a.widgets, func(w Widget) bool { return w.ID == id }) {
		panic(fmt.Sprintf("kernel: duplicate admin widget id %q", id))
	}
	a.widgets = append(a.widgets, Widget{ID: id, Render: render})
}

// SetPermissionCheck installs the function Nav uses to hide items whose
// Permission the current principal lacks (set by the auth module).
func (a *AdminRegistry) SetPermissionCheck(can func(ctx context.Context, perm string) bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.can = can
}

// Nav returns the items visible in ctx (static and provided), sorted by
// Position then ID.
func (a *AdminRegistry) Nav(ctx context.Context) []NavItem {
	a.mu.RLock()
	items := slices.Clone(a.nav)
	providers := slices.Clone(a.providers)
	can := a.can
	a.mu.RUnlock()
	// Providers run without the lock: they may query the database.
	seen := make(map[string]bool, len(items))
	for _, n := range items {
		seen[n.ID] = true
	}
	for _, p := range providers {
		for _, n := range p(ctx) {
			if !seen[n.ID] {
				seen[n.ID] = true
				items = append(items, n)
			}
		}
	}
	out := make([]NavItem, 0, len(items))
	for _, n := range items {
		if n.Permission != "" && can != nil && !can(ctx, n.Permission) {
			continue
		}
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Widgets returns the dashboard widgets in registration order.
func (a *AdminRegistry) Widgets() []Widget {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return slices.Clone(a.widgets)
}

// KindRequest describes what the theme should render; candidate functions
// map it to an ordered list of template names (most specific first).
type KindRequest struct{ Kind, Type, Slug, Template, Taxonomy, Term string }

// ThemeRegistry collects template funcs and template-hierarchy kinds.
type ThemeRegistry struct {
	mu    sync.RWMutex
	funcs template.FuncMap
	kinds map[string]func(req KindRequest) []string
}

// NewThemeRegistry returns an empty registry.
func NewThemeRegistry() *ThemeRegistry {
	return &ThemeRegistry{funcs: template.FuncMap{}, kinds: map[string]func(KindRequest) []string{}}
}

// AddFunc adds a template func; fn must be a function and name unique.
func (t *ThemeRegistry) AddFunc(name string, fn any) {
	if reflect.TypeOf(fn) == nil || reflect.TypeOf(fn).Kind() != reflect.Func {
		panic(fmt.Sprintf("kernel: theme func %q is not a function", name))
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, dup := t.funcs[name]; dup {
		panic(fmt.Sprintf("kernel: duplicate theme func %q", name))
	}
	t.funcs[name] = fn
}

// AddKind registers the candidate-template function for kind.
func (t *ThemeRegistry) AddKind(kind string, candidates func(req KindRequest) []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, dup := t.kinds[kind]; dup {
		panic(fmt.Sprintf("kernel: duplicate theme kind %q", kind))
	}
	t.kinds[kind] = candidates
}

// Funcs returns a copy of the registered template funcs.
func (t *ThemeRegistry) Funcs() template.FuncMap {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return maps.Clone(t.funcs)
}

// Candidates returns the template names for req.Kind, or nil if the kind is
// not registered.
func (t *ThemeRegistry) Candidates(req KindRequest) []string {
	t.mu.RLock()
	fn := t.kinds[req.Kind]
	t.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(req)
}

// APIParam documents a path/query/header parameter.
type APIParam struct {
	Name, In, Type, Description string
	Required                    bool
}

// APIOperation is one REST operation; Path is relative to the API mount
// ("/v1/content"). Request/Response are example values for OpenAPI schemas.
type APIOperation struct {
	Method, Path, Summary string
	Tags                  []string
	Scopes                []string
	Request, Response     any
	Params                []APIParam
	Handler               http.HandlerFunc
	Contract              *APIContract
}

// APIContract describes transport details that cannot be inferred from Go types.
type APIContract struct {
	SuccessStatuses    []int
	RequestContentType string
	MaxBodyBytes       int64
	Limits             map[string]any
	Errors             []APIErrorResponse
}

type APIErrorResponse struct {
	Status            int
	Code, Description string
}

// APIRegistry collects REST operations (consumed by the api module for
// routing and OpenAPI generation).
type APIRegistry struct {
	mu  sync.RWMutex
	ops []APIOperation
}

// NewAPIRegistry returns an empty registry.
func NewAPIRegistry() *APIRegistry { return &APIRegistry{} }

// Add registers op; Method+Path must be unique.
func (r *APIRegistry) Add(op APIOperation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if slices.ContainsFunc(r.ops, func(o APIOperation) bool { return o.Method == op.Method && o.Path == op.Path }) {
		panic(fmt.Sprintf("kernel: duplicate API operation %s %s", op.Method, op.Path))
	}
	r.ops = append(r.ops, op)
}

// Operations returns the operations in registration order.
func (r *APIRegistry) Operations() []APIOperation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.ops)
}

// MCPTool is one MCP tool. Sensitive tools require human approval.
type MCPTool struct {
	Name, Description string
	Input             any
	Scopes            []string
	Sensitive         bool
	Handler           func(ctx context.Context, args json.RawMessage) (any, error)
}

// MCPRegistry collects MCP tools.
type MCPRegistry struct {
	mu    sync.RWMutex
	tools []MCPTool
}

// NewMCPRegistry returns an empty registry.
func NewMCPRegistry() *MCPRegistry { return &MCPRegistry{} }

// AddTool registers t; names must be unique.
func (r *MCPRegistry) AddTool(t MCPTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if slices.ContainsFunc(r.tools, func(x MCPTool) bool { return x.Name == t.Name }) {
		panic(fmt.Sprintf("kernel: duplicate MCP tool %q", t.Name))
	}
	r.tools = append(r.tools, t)
}

// Tools returns the tools in registration order.
func (r *MCPRegistry) Tools() []MCPTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.tools)
}

// Permission is a declared permission with labels by locale.
type Permission struct {
	Name  string
	Label map[string]string
}

// PermissionRegistry collects permissions declared by modules.
type PermissionRegistry struct {
	mu    sync.RWMutex
	perms map[string]map[string]string
}

// NewPermissionRegistry returns an empty registry.
func NewPermissionRegistry() *PermissionRegistry {
	return &PermissionRegistry{perms: map[string]map[string]string{}}
}

// Declare adds perm; declaring it again merges the labels.
func (p *PermissionRegistry) Declare(perm string, label map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.perms[perm] == nil {
		p.perms[perm] = map[string]string{}
	}
	maps.Copy(p.perms[perm], label)
}

// All returns every permission sorted by name.
func (p *PermissionRegistry) All() []Permission {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Permission, 0, len(p.perms))
	for name, label := range p.perms {
		out = append(out, Permission{Name: name, Label: maps.Clone(label)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ServiceRegistry lets later modules reach earlier modules' services by
// name (e.g. "content" → content.Service) without import cycles.
type ServiceRegistry struct {
	mu       sync.RWMutex
	services map[string]any
}

// NewServiceRegistry returns an empty registry.
func NewServiceRegistry() *ServiceRegistry { return &ServiceRegistry{services: map[string]any{}} }

// Set registers v under name, replacing any previous value.
func (s *ServiceRegistry) Set(name string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services[name] = v
}

// Get returns the service registered under name if it has type T.
func Get[T any](d *Deps, name string) (T, bool) {
	var zero T
	if d == nil || d.Services == nil {
		return zero, false
	}
	d.Services.mu.RLock()
	v, ok := d.Services.services[name]
	d.Services.mu.RUnlock()
	if !ok {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}
