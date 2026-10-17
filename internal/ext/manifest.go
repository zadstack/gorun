package ext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/content"
)

// Hook kinds a manifest may subscribe with.
const (
	KindAction = "action" // sync hooks.Do → async delivery (job ext.deliver)
	KindFilter = "filter" // sync round trip; the response replaces the value
	KindEvent  = "event"  // hooks.Emit → async delivery (job ext.deliver)
)

const (
	defaultTimeoutMS = 2000
	maxTimeoutMS     = 10000
	maxManifestSize  = 1 << 20
)

var (
	slugRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	semverRe   = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	hookNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`)
)

// settingTypes are the field types a manifest setting may use (the admin
// form renders them as plain inputs).
var settingTypes = []content.FieldType{content.FieldText, content.FieldTextarea, content.FieldNumber, content.FieldBoolean, content.FieldSelect}

// ContentType is a content type declared by an extension (content.Type
// shaped; System is always forced to false).
type ContentType = content.Type

// SettingField is a setting declared by an extension (content.Field shaped).
type SettingField = content.Field

// HookSpec subscribes the extension to one hook.
type HookSpec struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`                 // action | filter | event
	TimeoutMS int    `json:"timeout_ms,omitempty"` // filters: default 2000, max 10000
}

// NavLink is an admin link the extension offers (shown on its admin page).
type NavLink struct {
	Label map[string]string `json:"label"`
	URL   string            `json:"url"`
}

// Endpoint is where the HTTP bridge delivers hooks.
type Endpoint struct {
	URL  string `json:"url"`
	Auth string `json:"auth"` // "hmac" (the only scheme in v1)
}

// Manifest is extensions/<slug>/manifest.json (spec §4.9).
type Manifest struct {
	Slug         string            `json:"slug"`
	Name         map[string]string `json:"name"`
	Version      string            `json:"version"`
	Description  map[string]string `json:"description,omitempty"`
	Author       string            `json:"author,omitempty"`
	Permissions  []string          `json:"permissions"`
	Hooks        []HookSpec        `json:"hooks"`
	AdminNav     []NavLink         `json:"admin_nav,omitempty"`
	ContentTypes []ContentType     `json:"content_types,omitempty"`
	Settings     []SettingField    `json:"settings,omitempty"`
	Endpoint     Endpoint          `json:"endpoint"`
}

// ParseManifest decodes a manifest strictly (unknown fields are errors) and
// normalizes defaults. It does not validate; Install does.
func ParseManifest(raw []byte) (*Manifest, error) {
	if len(raw) > maxManifestSize {
		return nil, errors.New("ext: manifest too large")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("ext: manifest: %w", err)
	}
	if dec.More() {
		return nil, errors.New("ext: manifest: trailing data")
	}
	m.normalize()
	return &m, nil
}

// normalize fills defaults: hook timeouts, endpoint auth, type System=false.
func (m *Manifest) normalize() {
	m.Slug = strings.TrimSpace(m.Slug)
	m.Version = strings.TrimSpace(m.Version)
	for i := range m.Hooks {
		if m.Hooks[i].TimeoutMS == 0 {
			m.Hooks[i].TimeoutMS = defaultTimeoutMS
		}
	}
	if m.Endpoint.URL != "" && m.Endpoint.Auth == "" {
		m.Endpoint.Auth = "hmac"
	}
	for i := range m.ContentTypes {
		m.ContentTypes[i].System = false
	}
}

// DisplayName returns the name in locale, falling back to en then the slug.
func (m *Manifest) DisplayName(locale string) string {
	return pick(m.Name, locale, m.Slug)
}

func pick(labels map[string]string, locale, fallback string) string {
	if v := labels[locale]; v != "" {
		return v
	}
	if v := labels["en"]; v != "" {
		return v
	}
	for _, v := range labels {
		if v != "" {
			return v
		}
	}
	return fallback
}

// validateEnv is what validation needs from the running system.
type validateEnv struct {
	perms        map[string]bool // declared permissions
	takenTypes   map[string]bool // existing content type slugs
	ownTypes     map[string]bool // types this slug declared in a previous (non-purge) install
	allowPrivate bool
}

// validate returns field errors keyed like "hooks.0.kind" with i18n keys.
func (m *Manifest) validate(env validateEnv) map[string]string {
	errs := map[string]string{}
	if !slugRe.MatchString(m.Slug) {
		errs["slug"] = "ext.validation.slug"
	}
	if pick(m.Name, "en", "") == "" {
		errs["name"] = "ext.validation.required"
	}
	if !semverRe.MatchString(m.Version) {
		errs["version"] = "ext.validation.version"
	}
	for i, p := range m.Permissions {
		if !env.perms[p] {
			errs["permissions."+strconv.Itoa(i)] = "ext.validation.permission"
		}
	}
	m.validateHooks(errs)
	if len(m.Hooks) > 0 || m.Endpoint.URL != "" {
		if msg := validateEndpoint(m.Endpoint.URL, env.allowPrivate); msg != "" {
			errs["endpoint.url"] = msg
		}
		if m.Endpoint.Auth != "hmac" {
			errs["endpoint.auth"] = "ext.validation.endpoint_auth"
		}
	}
	for i, n := range m.AdminNav {
		p := "admin_nav." + strconv.Itoa(i) + "."
		if pick(n.Label, "en", "") == "" {
			errs[p+"label"] = "ext.validation.required"
		}
		if !isNavURL(n.URL) {
			errs[p+"url"] = "ext.validation.url"
		}
	}
	m.validateTypes(env, errs)
	m.validateSettings(errs)
	return errs
}

func (m *Manifest) validateHooks(errs map[string]string) {
	seen := map[string]string{} // name|registry → kind
	for i, h := range m.Hooks {
		p := "hooks." + strconv.Itoa(i) + "."
		if !hookNameRe.MatchString(h.Name) || len(h.Name) > 100 {
			errs[p+"name"] = "ext.validation.hook_name"
		}
		switch h.Kind {
		case KindAction, KindEvent, KindFilter:
		default:
			errs[p+"kind"] = "ext.validation.hook_kind"
			continue
		}
		if h.TimeoutMS < 0 || h.TimeoutMS > maxTimeoutMS {
			errs[p+"timeout_ms"] = "ext.validation.timeout"
		}
		key := h.Name + "|" + registryOf(h.Kind)
		if _, dup := seen[key]; dup {
			errs[p+"name"] = "ext.validation.hook_duplicate"
		}
		seen[key] = h.Kind
	}
}

// registryOf maps a hook kind to the hooks registry it subscribes in:
// actions and events both arrive through hooks.Action.
func registryOf(kind string) string {
	if kind == KindFilter {
		return KindFilter
	}
	return KindAction
}

func validateEndpoint(raw string, allowPrivate bool) string {
	if raw == "" {
		return "ext.validation.required"
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "ext.validation.endpoint_url"
	}
	if allowPrivate {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "ext.validation.endpoint_private"
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return "ext.validation.endpoint_private"
	}
	return ""
}

func isNavURL(raw string) bool {
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (m *Manifest) validateTypes(env validateEnv, errs map[string]string) {
	seen := map[string]bool{}
	for i, t := range m.ContentTypes {
		p := "content_types." + strconv.Itoa(i) + "."
		tt := t
		for k, v := range content.ValidateTypeSchema(&tt) {
			errs[p+k] = v
		}
		if _, bad := errs[p+"slug"]; bad {
			continue
		}
		switch {
		case seen[t.Slug]:
			errs[p+"slug"] = "ext.validation.type_duplicate"
		case env.takenTypes[t.Slug] && !env.ownTypes[t.Slug]:
			errs[p+"slug"] = "ext.validation.type_taken"
		}
		seen[t.Slug] = true
	}
}

func (m *Manifest) validateSettings(errs map[string]string) {
	var schema []content.Field
	for i, f := range m.Settings {
		if !slices.Contains(settingTypes, f.Type) {
			errs["settings."+strconv.Itoa(i)+".type"] = "ext.validation.setting_type"
		}
		schema = append(schema, f)
	}
	if len(schema) == 0 {
		return
	}
	t := content.Type{Slug: "settings", Name: map[string]string{"en": "settings"}, Fields: schema}
	for k, v := range content.ValidateTypeSchema(&t) {
		if strings.HasPrefix(k, "fields.") {
			key := "settings." + strings.TrimPrefix(k, "fields.")
			if _, exists := errs[key]; !exists {
				errs[key] = v
			}
		}
	}
}

// typeSlugs returns the slugs of the declared content types.
func (m *Manifest) typeSlugs() []string {
	out := make([]string, 0, len(m.ContentTypes))
	for _, t := range m.ContentTypes {
		out = append(out, t.Slug)
	}
	return out
}
